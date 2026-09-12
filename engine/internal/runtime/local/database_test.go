package local_test

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/envcert"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

const relayPostgresImage = "postgres:17-alpine@sha256:18cfe3ef5e6815560c98237d6216d1e5119702fb0f3894c8785dd58b8bbe5d73"
const relayFixturePassword = "AF_FAKE_DATABASE_PASSWORD"
const relayFixtureHostname = "branch.af-remote.invalid"

func relayExec(ctx context.Context, cli *client.Client, id string, command []string, input string) (string, int, error) {
	created, err := cli.ContainerExecCreate(ctx, id, container.ExecOptions{
		Cmd: command, Env: []string{"PGPASSWORD=" + relayFixturePassword},
		AttachStdout: true, AttachStderr: true, AttachStdin: input != "",
	})
	if err != nil {
		return "", -1, err
	}
	stream, err := cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", -1, err
	}
	defer stream.Close()
	if input != "" {
		if _, err := io.WriteString(stream.Conn, input); err != nil {
			return "", -1, err
		}
		if err := stream.CloseWrite(); err != nil {
			return "", -1, err
		}
	}
	var output bytes.Buffer
	if _, err := stdcopy.StdCopy(&output, &output, stream.Reader); err != nil {
		return "", -1, err
	}
	status, err := cli.ContainerExecInspect(ctx, created.ID)
	return output.String(), status.ExitCode, err
}

func relayLeaf(t *testing.T, ca *envcert.Authority) (string, string) {
	t.Helper()
	certBlock, _ := pem.Decode([]byte(ca.CertPEM))
	require.NotNil(t, certBlock)
	issuer, err := x509.ParseCertificate(certBlock.Bytes)
	require.NoError(t, err)
	keyBlock, _ := pem.Decode([]byte(ca.KeyPEM.Reveal()))
	require.NotNil(t, keyBlock)
	issuerKey, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	require.NoError(t, err)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: relayFixtureHostname},
		DNSNames:  []string{relayFixtureHostname},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, issuer, &key.PublicKey, issuerKey)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
}

func relayPostgres(t *testing.T, ctx context.Context, cli *client.Client, edge, owner string, ca *envcert.Authority) string {
	t.Helper()
	name := "af-db-relay-fixture-" + uuid.NewString()[:8]
	// Register cleanup by the known name before Create, including a lost reply.
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		found, err := cli.ContainerInspect(cleanup, name)
		if err == nil && found.Config.Labels["af.test.database-relay"] == owner {
			require.NoError(t, cli.ContainerRemove(cleanup, found.ID, container.RemoveOptions{Force: true, RemoveVolumes: true}))
		}
	})
	created, err := cli.ContainerCreate(ctx, &container.Config{
		Image: relayPostgresImage, Labels: map[string]string{"af.test.database-relay": owner},
		Env:        []string{"POSTGRES_PASSWORD=" + relayFixturePassword},
		Entrypoint: []string{"/bin/sh", "-c"},
		Cmd:        []string{"chown postgres:postgres /tmp/af-relay.key; chmod 600 /tmp/af-relay.key; exec docker-entrypoint.sh postgres -c ssl=on -c ssl_cert_file=/tmp/af-relay.crt -c ssl_key_file=/tmp/af-relay.key -c listen_addresses='*'"},
	}, &container.HostConfig{}, &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{edge: {}}}, nil, name)
	require.NoError(t, err)
	cert, key := relayLeaf(t, ca)
	var files bytes.Buffer
	archive := tar.NewWriter(&files)
	for name, content := range map[string]string{"tmp/af-relay.crt": cert, "tmp/af-relay.key": key} {
		mode := int64(0o644)
		if strings.HasSuffix(name, ".key") {
			mode = 0o600
		}
		require.NoError(t, archive.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(content))}))
		_, err := io.WriteString(archive, content)
		require.NoError(t, err)
	}
	require.NoError(t, archive.Close())
	require.NoError(t, cli.CopyToContainer(ctx, created.ID, "/", &files, container.CopyToContainerOptions{}))
	require.NoError(t, cli.ContainerStart(ctx, created.ID, container.StartOptions{}))
	command := []string{"psql", "-X", "-h", "127.0.0.1", "-U", "postgres", "-d", "postgres", "-Atqc", "SELECT current_setting('ssl')"}
	require.Eventually(t, func() bool {
		out, code, err := relayExec(ctx, cli, created.ID, command, "")
		return err == nil && code == 0 && strings.TrimSpace(out) == "on"
	}, time.Minute, 200*time.Millisecond, "the disposable PostgreSQL TLS server did not become ready")
	// SQL containing the synthetic password travels on stdin, never argv.
	setup := "CREATE ROLE af_relay_app LOGIN PASSWORD '" + relayFixturePassword + "';\n" +
		"CREATE TABLE af_relay_migrated (value text); INSERT INTO af_relay_migrated VALUES ('seed');\n" +
		"GRANT SELECT ON af_relay_migrated TO af_relay_app;\n"
	_, code, err := relayExec(ctx, cli, created.ID, []string{"psql", "-X", "-h", "127.0.0.1", "-U", "postgres", "-d", "postgres", "-v", "ON_ERROR_STOP=1", "-q"}, setup)
	require.NoError(t, err)
	require.Zero(t, code)
	state, err := cli.ContainerInspect(ctx, created.ID)
	require.NoError(t, err)
	for _, endpoint := range state.NetworkSettings.Networks {
		if endpoint.IPAddress != "" {
			return endpoint.IPAddress
		}
	}
	t.Fatal("the disposable database has no edge-network address")
	return ""
}

func relayURL(host string, port int, user, mode string) string {
	query := url.Values{"sslmode": {mode}, "sslrootcert": {provider.DatabaseTrustBundlePath}}
	return (&url.URL{Scheme: "postgres", User: url.User(user), Host: net.JoinHostPort(host, strconv.Itoa(port)), Path: "/postgres", RawQuery: query.Encode()}).String()
}

func relayShell(source string) string { return "'" + strings.ReplaceAll(source, "'", "'\"'\"'") + "'" }

func TestDatabaseRelayRealPostgresTLSAndContainment(t *testing.T) {
	for _, inspectionCA := range []bool{false, true} {
		t.Run(fmt.Sprintf("HTTP_authority_%t", inspectionCA), func(t *testing.T) {
			runtime := requireRuntime(t)
			owner := uuid.NewString()[:8]
			id := envID(t, runtime, "dbrelay"+owner)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			cli, err := dockerutil.Client()
			require.NoError(t, err)
			t.Cleanup(func() { _ = cli.Close() })
			present, err := dockerutil.ImagePresent(ctx, cli, relayPostgresImage)
			require.NoError(t, err)
			if !present {
				pull, err := cli.ImagePull(ctx, relayPostgresImage, image.PullOptions{})
				require.NoError(t, err)
				_, err = io.Copy(io.Discard, pull)
				require.NoError(t, err)
				require.NoError(t, pull.Close())
			}
			_, err = runtime.EnsureNetworks(ctx, id, nil)
			require.NoError(t, err)
			edge, err := cli.NetworkInspect(ctx, "af-edge-"+id, network.InspectOptions{})
			require.NoError(t, err)
			databaseCA, err := envcert.Generate("database-"+owner, time.Now())
			require.NoError(t, err)
			httpCA, err := envcert.Generate("http-"+owner, time.Now())
			require.NoError(t, err)
			upstream := relayPostgres(t, ctx, cli, edge.ID, owner, databaseCA)
			routes := []provider.DatabaseRoute{{Port: 45000, Upstream: net.JoinHostPort(upstream, "5432")}, {Port: 45002, Upstream: net.JoinHostPort(upstream, "5432")}}
			forgedURL := ""
			if inspectionCA {
				forged := relayPostgres(t, ctx, cli, edge.ID, owner, httpCA)
				routes = append(routes, provider.DatabaseRoute{Port: 45001, Upstream: net.JoinHostPort(forged, "5432")})
				forgedURL = relayURL(relayFixtureHostname, 45001, "af_relay_app", "verify-full")
			}
			query := "SELECT value || '|' || current_user || '|' || (SELECT ssl::text FROM pg_stat_ssl WHERE pid=pg_backend_pid()) FROM af_relay_migrated"
			probe := `set -u
if [ "$(id -u)" -ne 0 ]; then echo AF-DB-NONROOT; else echo AF-DB-ROOT; fi
if good=$(psql -X "$DATABASE_URL" -Atqc "$AF_FAKE_QUERY" 2>/dev/null); then :; else good=failed; fi
echo "AF-DB-GOOD:$good"
if alias=$(psql -X "$AF_FAKE_ALIAS_URL" -Atqc "$AF_FAKE_QUERY" 2>/dev/null); then :; else alias=failed; fi
echo "AF-DB-ALIAS:$alias"
if psql -X "$AF_FAKE_DIRECT_URL" -Atqc "$AF_FAKE_QUERY" >/dev/null 2>&1; then echo AF-DB-DIRECT:escaped; else echo AF-DB-DIRECT:contained; fi
if [ -n "$AF_FAKE_FORGED_URL" ]; then
  if psql -X "$AF_FAKE_FORGED_URL" -Atqc "$AF_FAKE_QUERY" >/dev/null 2>&1; then echo AF-DB-FORGERY:accepted; else echo AF-DB-FORGERY:refused; fi
fi
echo "AF-DB-HTTP-TRUST:${SSL_CERT_FILE-unset}"
echo AF-DB-COMPLETE
exec tail -f /dev/null`
			migration := `psql -X "$DATABASE_URL" -v ON_ERROR_STOP=1 -q -c "UPDATE af_relay_migrated SET value='migrated'"`
			spec := provider.EnvSpec{
				EnvID: id, DatabaseRoutes: routes, DatabaseCACertPEM: databaseCA.CertPEM,
				DatabaseURL:          secrets.New(relayURL(relayFixtureHostname, 45000, "af_relay_app", "verify-full")),
				MigrationDatabaseURL: secrets.New(relayURL(relayFixtureHostname, 45002, "postgres", "verify-full")),
				Services: []provider.ServiceSpec{{
					Name: "probe", Kind: "worker", Image: relayPostgresImage,
					Command: "su -p postgres -s /bin/sh -c " + relayShell(probe),
					Migrate: "su -p postgres -s /bin/sh -c " + relayShell(migration),
					Env: map[string]secrets.Value{
						"PGPASSWORD": secrets.New(relayFixturePassword), "PGCONNECT_TIMEOUT": secrets.New("4"),
						"AF_FAKE_QUERY": secrets.New(query), "AF_FAKE_FORGED_URL": secrets.New(forgedURL),
						"AF_FAKE_ALIAS_URL":  secrets.New(relayURL("another.af-remote.invalid", 45000, "af_relay_app", "verify-ca")),
						"AF_FAKE_DIRECT_URL": secrets.New(relayURL(upstream, 5432, "af_relay_app", "verify-ca")),
					},
				}},
			}
			if inspectionCA {
				spec.CACertPEM, spec.CAKeyPEM = httpCA.CertPEM, httpCA.KeyPEM
			}
			_, err = runtime.Up(ctx, spec)
			require.NoError(t, err, "a real PostgreSQL TLS migration did not cross the fixed relay")
			var output string
			require.Eventually(t, func() bool {
				lines, err := runtime.Logs(ctx, id, "probe", 100)
				if err != nil {
					return false
				}
				var text strings.Builder
				for _, line := range lines {
					text.WriteString(line.Text + "\n")
				}
				output = text.String()
				return strings.Contains(output, "AF-DB-COMPLETE")
			}, time.Minute, 200*time.Millisecond)
			assert.Contains(t, output, "AF-DB-NONROOT")
			assert.Contains(t, output, "AF-DB-GOOD:migrated|af_relay_app|true")
			assert.Contains(t, output, "AF-DB-ALIAS:migrated|af_relay_app|true", "changing the client hostname selected another destination")
			assert.Contains(t, output, "AF-DB-DIRECT:contained")
			if inspectionCA {
				assert.Contains(t, output, "AF-DB-FORGERY:refused", "the sidecar's HTTP CA authenticated a database server")
				assert.Contains(t, output, "AF-DB-HTTP-TRUST:"+envcert.BundlePath)
			} else {
				assert.Contains(t, output, "AF-DB-HTTP-TRUST:unset", "database trust changed HTTP trust")
			}
		})
	}
}
