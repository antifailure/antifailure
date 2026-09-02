// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package secrets

// Against a real Vault, not a fake.
//
// A fake would prove this code agrees with our reading of the API
// documentation, and every mistake worth catching here is a mistake in that
// reading. Whether a KV version 2 read needs the data/ segment. Whether a
// missing path is 404 or 200 with a null. Whether a denied read is 403 or 404,
// which decides whether it is a refusal or a miss and therefore whether it
// stops the chain or falls through it. Whether an AppRole login returns the
// token where we look for it. A stand-in would agree with whatever this code
// assumed about all four.
//
// Vault is the one store in this package that can be run for free, in a
// container, with no account, which is why it gets to be the one that is
// actually proved rather than merely written.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// vaultImage is pinned rather than floating on latest, so that a run today and
// a run in six months are the same run. Vault 1.20 is a current release line.
const vaultImage = "hashicorp/vault:1.20.4"

type liveVault struct {
	address   string
	token     string
	container string
}

// shared is one Vault for the whole package.
//
// One rather than one per test, because starting a container is seconds and
// there are a dozen tests here, and a suite that takes three minutes on an idle
// machine takes ten on a busy one and then trips the package timeout, which is
// what the first version of this file did. Tests that need to destroy the
// server, which means the one that seals it, start their own.
//
// Isolation comes from the path instead: every test writes under its own name,
// so two tests cannot see each other's values even though they share a server.
var (
	shared    *liveVault
	sharedErr error
)

func TestMain(m *testing.M) {
	shared, sharedErr = tryStartVault()
	code := m.Run()
	if shared != nil {
		_ = exec.Command("docker", "rm", "-f", shared.container).Run()
	}
	os.Exit(code)
}

// vault returns the shared server.
//
// It skips only for the one reason that is about the machine rather than about
// the code: there is no Docker to run a Vault in. Every other failure to start
// one is a FAILURE, and that distinction is the whole point of this function.
//
// The first version of this file skipped on any failure, and it hid a real bug
// for a full run: the Vault image needs an explicit command, so every container
// started, published a port, logged nothing, and answered nothing. Twelve tests
// reported SKIP, the package reported ok, and nothing had been proved. A skip
// that can be caused by the code under test is a pass with extra steps.
func vault(t *testing.T) liveVault {
	t.Helper()
	if shared == nil {
		if errors.Is(sharedErr, errNoDocker) {
			t.Skip("skipped: docker is not available, and proving this needs a real Vault")
		}
		t.Fatalf("docker is available and Vault did not start, which is a failure "+
			"rather than a reason to skip: %v", sharedErr)
	}
	return *shared
}

// errNoDocker is the one reason a skip is honest.
var errNoDocker = errors.New("no docker daemon is answering")

// startVault runs a real Vault in development mode, for a test that will
// destroy it.
//
// Development mode is in-memory, unsealed, and gone when the container stops,
// which is the right thing for a test and the wrong thing for anything else.
// Everything the API does is the real thing; only the storage and the seal are
// not.
func startVault(t *testing.T) liveVault {
	t.Helper()
	live, err := tryStartVault()
	if live == nil {
		if errors.Is(err, errNoDocker) {
			t.Skip("skipped: docker is not available, and proving this needs a real Vault")
		}
		t.Fatalf("docker is available and Vault did not start: %v", err)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", live.container).Run() })
	return *live
}

// tryStartVault starts one, or says why it could not.
func tryStartVault() (*liveVault, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, errNoDocker
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		return nil, errNoDocker
	}

	// Assembled at run time. A literal token in the repository is a string a
	// secret scanner has to be taught to ignore, and teaching a scanner to
	// ignore things is how a real one gets through.
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	token := fmt.Sprintf("af-test-%x", raw)

	// The command is given explicitly, and this is the part that is easy to get
	// wrong and silent when you do. Running the image with no arguments starts
	// a container that publishes a port, writes nothing to its log, and answers
	// nothing, so every test skips and the package reports ok. Naming the
	// entrypoint and the arguments makes a failure to start a failure.
	// No --rm. A container that removes itself on exit takes its log with it,
	// and the log is the only thing that explains why it exited: the first CI
	// run of this reported "the container published no port; its log said:
	// Error response from daemon: page not found", which is docker saying the
	// container is already gone rather than anything about Vault. Every path
	// out of here removes it explicitly instead.
	//
	// IPC_LOCK because that is what HashiCorp's own documentation says to run
	// this image with. Vault locks its memory so that secrets cannot be paged
	// to disk, and a container without the capability may refuse to start.
	run := exec.Command("docker", "run", "-d",
		"-p", "0:8200",
		"--cap-add", "IPC_LOCK",
		"-e", "VAULT_DEV_ROOT_TOKEN_ID="+token,
		"--entrypoint", "vault",
		vaultImage,
		"server", "-dev", "-dev-listen-address=0.0.0.0:8200",
	)
	// Standard output only, kept apart from standard error. docker writes the
	// container id to stdout and everything else to stderr, and "everything
	// else" includes the pull progress when the image is not cached. Reading
	// them together gives an id with twenty lines of "Pulling fs layer" in
	// front of it, which every later docker command then rejects.
	//
	// This is invisible on a machine that has run the tests once, because the
	// image is already there and stderr is empty. It fails on every clean
	// machine, which is to say on CI and on a new contributor's laptop and
	// nowhere in between.
	var runErr bytes.Buffer
	run.Stderr = &runErr
	out, err := run.Output()
	if err != nil {
		return nil, fmt.Errorf("docker run: %s", strings.TrimSpace(runErr.String()))
	}
	id := strings.TrimSpace(string(out))
	if len(id) < 12 || strings.ContainsAny(id, " \n") {
		// Belt and braces. If docker ever writes something else to stdout, this
		// says so rather than passing a sentence to `docker port` and reporting
		// its confusion as a Vault problem.
		return nil, fmt.Errorf("docker run did not print a container id; it printed %q", id)
	}

	// Whether it is still running, before asking about its ports. `docker port`
	// on an exited container answers "page not found", which reads as a bug in
	// this harness and is actually the container having died with something to
	// say.
	if state, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", id).Output(); err == nil &&
		strings.TrimSpace(string(state)) != "true" {
		return nil, fmt.Errorf("the container exited immediately; its log said:\n%s\n%s",
			containerLog(id), removeContainer(id))
	}

	portOut, err := exec.Command("docker", "port", id, "8200/tcp").Output()
	if err != nil {
		return nil, fmt.Errorf("the container published no port; its log said:\n%s\n%s",
			containerLog(id), removeContainer(id))
	}
	// docker port prints one line per address family; the port is the same.
	line := strings.TrimSpace(strings.Split(strings.TrimSpace(string(portOut)), "\n")[0])
	colon := strings.LastIndex(line, ":")
	if colon < 0 {
		removeContainer(id)
		return nil, fmt.Errorf("cannot read a port out of %q", line)
	}
	address := "http://127.0.0.1:" + line[colon+1:]

	// Waited for rather than slept through. A fixed sleep is either too short
	// on a loaded machine or wasted on a fast one.
	deadline := time.Now().Add(90 * time.Second)
	for {
		resp, err := http.Get(address + "/v1/sys/health") //nolint:noctx // bounded by the deadline
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return &liveVault{address: address, token: token, container: id}, nil
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("vault did not become healthy in 90s; its log said:\n%s\n%s",
				containerLog(id), removeContainer(id))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// containerLog reads what the container said before it stopped.
//
// Read while the container still exists, which is the whole reason startVault
// does not pass --rm. A log that is deleted at the moment of failure is a log
// that exists for every case except the one that needed it.
func containerLog(id string) string {
	out, err := exec.Command("docker", "logs", id).CombinedOutput()
	if err != nil || len(out) == 0 {
		return "(the container wrote nothing)"
	}
	return string(out)
}

// removeContainer cleans up and returns an empty string, so it can be called
// inside an error message and the cleanup cannot be forgotten on a path out.
func removeContainer(id string) string {
	_ = exec.Command("docker", "rm", "-f", id).Run()
	return ""
}

// call makes a raw request to Vault, so the test seeds and configures through
// the real API rather than through the code under test. Seeding with the thing
// being tested would make a read that agrees with a broken write look correct.
func (v liveVault) call(t *testing.T, method, path string, body any, into any) int {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		require.NoError(t, err)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, v.address+path, bytes.NewReader(payload))
	require.NoError(t, err)
	req.Header.Set("X-Vault-Token", v.token)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	if into != nil {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(into))
	}
	return resp.StatusCode
}

func (v liveVault) writeKV(t *testing.T, path string, data map[string]any) {
	t.Helper()
	status := v.call(t, "POST", "/v1/secret/data/"+path, map[string]any{"data": data}, nil)
	require.Contains(t, []int{200, 204}, status, "seeding %s failed", path)
}

// ---------------------------------------------------------------------------

func TestVaultConformance(t *testing.T) {
	live := vault(t)

	// One secret holding every variable, which is how these are organised in
	// practice: one document per application, keys named after the variables.
	const path = "conformance"
	live.writeKV(t, path, map[string]any{
		"DATABASE_URL":      "postgres://app@db.internal:5432/app",
		"STRIPE_SECRET_KEY": "sk_test_not_a_real_key",
		"BLANK":             "",
		// A number and a boolean, because somebody typing into the Vault UI
		// produces both and an environment variable is a string. A source that
		// refused them would make a working Vault unusable.
		"PORT":     5432,
		"FEATURE":  true,
		"SETTINGS": map[string]any{"retries": 3},
	})

	working := newVaultBackend(VaultConfig{
		Address: live.address, Token: live.token, Path: path,
	})

	// A source whose credential the store refuses, and which can renew it, so
	// the one-refresh rule is exercised against something that really refuses.
	//
	// An AppRole with only the default policy: the login succeeds, so the
	// credential renews, and the read is denied, so renewing does not help.
	// That is exactly the shape of a credential whose permissions were changed
	// underneath a running process, which is the case the rule exists for.
	roleID, secretID := live.setUpDeniedAppRole(t, "conformance")
	rejecting := &countingVault{VaultBackend: newVaultBackend(VaultConfig{
		Address: live.address, RoleID: roleID, SecretID: secretID, Path: path,
	})}
	require.NoError(t, rejecting.Login(t.Context()), "the AppRole login itself must work")

	unreachable := newVaultBackend(VaultConfig{
		// A port nothing is listening on, on a host that resolves immediately,
		// so the test fails fast rather than waiting for a DNS timeout.
		Address: "http://127.0.0.1:1", Token: live.token, Path: path,
	})

	result := Run(t.Context(), t, Harness{
		Name:         "HashiCorp Vault " + vaultImage,
		Working:      New(working),
		Present:      "DATABASE_URL",
		PresentValue: "postgres://app@db.internal:5432/app",
		Empty:        "BLANK",
		Absent:       "NOT_IN_VAULT",
		Rejecting:    New(rejecting),
		Refreshes:    rejecting.count,
		Unreachable:  New(unreachable),
	})
	require.Empty(t, result.Failed)
	t.Logf("passed %d behaviours, skipped %d", len(result.Passed), len(result.Skipped))
	// Nothing may skip. Vault can be put into every state the suite asks about,
	// so a skip here means the harness stopped supplying one rather than that
	// the store cannot do it.
	require.Empty(t, result.Skipped)
}

// countingVault counts renewals for the conformance harness.
//
// In the test rather than on VaultBackend, so that production code does not
// carry an accessor whose only caller is a test. That is the same rule this
// project applies everywhere else: a method with no real call site is a gap,
// and the way to avoid one is not to add the method.
type countingVault struct {
	*VaultBackend
	renewals int
}

func (c *countingVault) Refresh(ctx context.Context) error {
	c.renewals++
	return c.VaultBackend.Refresh(ctx)
}

func (c *countingVault) count() int { return c.renewals }

// setUpDeniedAppRole creates an AppRole that can log in and can read nothing.
//
// The role is named after the test, because the server is shared and two tests
// sharing a role would be two tests that can invalidate each other's secret id.
func (v liveVault) setUpDeniedAppRole(t *testing.T, role string) (roleID, secretID string) {
	t.Helper()
	// 400 means the auth method is already mounted, which it is for every test
	// after the first on a shared server. Tolerated rather than guarded with a
	// read, because the read is a second round trip to learn what the write
	// will tell us.
	require.Contains(t, []int{200, 204, 400},
		v.call(t, "POST", "/v1/sys/auth/approle", map[string]any{"type": "approle"}, nil))
	require.Contains(t, []int{200, 204},
		v.call(t, "POST", "/v1/auth/approle/role/"+role,
			map[string]any{"token_policies": "default", "token_ttl": "10m"}, nil))

	var got struct {
		Data struct {
			RoleID string `json:"role_id"`
		} `json:"data"`
	}
	require.Equal(t, 200, v.call(t, "GET", "/v1/auth/approle/role/"+role+"/role-id", nil, &got))
	require.NotEmpty(t, got.Data.RoleID)

	var secret struct {
		Data struct {
			SecretID string `json:"secret_id"`
		} `json:"data"`
	}
	require.Equal(t, 200,
		v.call(t, "POST", "/v1/auth/approle/role/"+role+"/secret-id", map[string]any{}, &secret))
	require.NotEmpty(t, secret.Data.SecretID)
	return got.Data.RoleID, secret.Data.SecretID
}

// mountKVv1 mounts the older engine once, tolerating it already being there.
func (v liveVault) mountKVv1(t *testing.T) {
	t.Helper()
	require.Contains(t, []int{200, 204, 400}, v.call(t, "POST", "/v1/sys/mounts/kv1",
		map[string]any{"type": "kv", "options": map[string]any{"version": "1"}}, nil))
}

// ---------------------------------------------------------------------------
// The specifics the conformance suite does not cover, because they are about
// Vault rather than about the contract.

func TestVaultRendersNonStringFieldsAsValues(t *testing.T) {
	live := vault(t)
	live.writeKV(t, "render", map[string]any{
		"PORT": 5432, "FEATURE": true, "SETTINGS": map[string]any{"retries": 3}, "NOTHING": nil,
	})
	source := New(newVaultBackend(VaultConfig{
		Address: live.address, Token: live.token, Path: "render",
	}))
	ctx := withFeatures(t.Context(), "enterprise_secrets")

	for name, want := range map[string]string{
		"PORT": "5432", "FEATURE": "true", "SETTINGS": `{"retries":3}`, "NOTHING": "",
	} {
		value, found, err := source.Lookup(ctx, name)
		require.NoError(t, err)
		require.True(t, found, "%s was written and not found", name)
		require.Equal(t, want, value, "%s rendered wrongly", name)
	}
}

func TestVaultReadsOneSecretPerPath(t *testing.T) {
	// The other shape, for organizations whose access policies are per path. A
	// source that could not do this is a source they cannot use.
	live := vault(t)
	live.writeKV(t, "per-path/DATABASE_URL", map[string]any{"value": "postgres://per-path"})
	source := New(newVaultBackend(VaultConfig{
		Address: live.address, Token: live.token, Path: "per-path", PathPerName: true,
	}))
	ctx := withFeatures(t.Context(), "enterprise_secrets")

	value, found, err := source.Lookup(ctx, "DATABASE_URL")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "postgres://per-path", value)

	// And a path that is not there is a miss rather than a failure, so the
	// chain falls through to the next source.
	_, found, err = source.Lookup(ctx, "SOMETHING_ELSE")
	require.NoError(t, err)
	require.False(t, found)
}

func TestVaultReadsTheOlderKVEngine(t *testing.T) {
	live := vault(t)
	// A version 1 KV engine, because a dev server's default mount is version 2
	// and an organization on the older engine is a real organization.
	live.mountKVv1(t)
	require.Contains(t, []int{200, 204}, live.call(t, "POST", "/v1/kv1/older",
		map[string]any{"DATABASE_URL": "postgres://v1"}, nil))

	source := New(newVaultBackend(VaultConfig{
		Address: live.address, Token: live.token, Mount: "kv1", Path: "older", KVv1: true,
	}))
	value, found, err := source.Lookup(withFeatures(t.Context(), "enterprise_secrets"), "DATABASE_URL")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "postgres://v1", value)
}

func TestVaultSaysSoWhenTheMountIsTheOtherVersion(t *testing.T) {
	// The single most common way to misconfigure this, and the reason it is
	// worth catching: neither direction produces an error on its own. Reading a
	// version 2 mount as version 1 returns the envelope instead of the secret,
	// and reading a version 1 mount as version 2 asks for a path with a data/
	// segment that is not there and gets a 404. Both present as "the variable
	// is not set" for a variable that is plainly there in the UI.
	live := vault(t)
	live.writeKV(t, "version-mixup", map[string]any{"DATABASE_URL": "postgres://v2"})
	live.mountKVv1(t)
	require.Contains(t, []int{200, 204}, live.call(t, "POST", "/v1/kv1/version-mixup",
		map[string]any{"DATABASE_URL": "postgres://v1"}, nil))
	ctx := withFeatures(t.Context(), "enterprise_secrets")

	// A version 2 mount, read as version 1.
	asV1 := New(newVaultBackend(VaultConfig{
		Address: live.address, Token: live.token, Path: "version-mixup", KVv1: true,
	}))
	ok, why := asV1.Available(ctx)
	require.False(t, ok, "a mount read as the wrong version reported itself usable")
	require.Contains(t, why, "KV version 2")
	require.Contains(t, why, "report every variable as absent")
	t.Logf("reports: %s (%s)", asV1.Name(), why)

	// A version 1 mount, read as version 2.
	asV2 := New(newVaultBackend(VaultConfig{
		Address: live.address, Token: live.token, Mount: "kv1", Path: "version-mixup",
	}))
	ok, why = asV2.Available(ctx)
	require.False(t, ok)
	require.Contains(t, why, "KV version 1")
	t.Logf("reports: %s (%s)", asV2.Name(), why)
}

func TestVaultCatchesTheVersionMixUpWithoutReadingTheMount(t *testing.T) {
	// The second line of defence, for a token whose policy does not let the
	// mount's own metadata be read. The lookup itself notices the envelope and
	// says so, rather than returning its two keys as if they were the variables
	// and reporting every real one as absent.
	live := vault(t)
	live.writeKV(t, "envelope", map[string]any{"DATABASE_URL": "postgres://v2"})

	backend := newVaultBackend(VaultConfig{
		Address: live.address, Token: live.token, Path: "envelope", KVv1: true,
	})
	// Straight to the read, past Available, which is the position a caller is
	// in when the mount check could not run.
	_, _, err := backend.Fetch(t.Context(), "DATABASE_URL")
	require.Error(t, err, "a mount read as the wrong version reported a miss and said nothing")
	require.Contains(t, err.Error(), "KV version 2")
	t.Logf("reports: %s", err)

	// Every lookup afterwards says the same thing, rather than only the first.
	// A message that appears once is a message the second variable's failure
	// looks unexplained next to.
	_, _, err = backend.Fetch(t.Context(), "SOMETHING_ELSE")
	require.Error(t, err)
	require.Contains(t, err.Error(), "KV version 2")
}

func TestVaultReportsASealedVaultAsSealed(t *testing.T) {
	// Not as "connection refused". A sealed Vault is up, answering, and can
	// serve nothing, and an operator sent to look at the network instead of at
	// the unseal keys is an operator looking in the wrong place.
	// Its own server, because sealing it makes it useless to every other test
	// and the rest of this file shares one.
	live := startVault(t)
	require.Contains(t, []int{200, 204}, live.call(t, "POST", "/v1/sys/seal", nil, nil))

	source := New(newVaultBackend(VaultConfig{
		Address: live.address, Token: live.token, Path: "sealed",
	}))
	ok, why := source.Available(withFeatures(t.Context(), "enterprise_secrets"))
	require.False(t, ok)
	require.Contains(t, why, "sealed")
	t.Logf("reports: %s (%s)", source.Name(), why)
}

func TestVaultReportsADeniedReadAsARefusalAndNotAMiss(t *testing.T) {
	// The distinction that decides whether the chain stops or falls through.
	// A token that cannot read the path must not make the variable look absent,
	// because the next source down would then supply a different secret and
	// nothing would say so.
	live := vault(t)
	live.writeKV(t, "denied", map[string]any{"DATABASE_URL": "postgres://denied"})
	roleID, secretID := live.setUpDeniedAppRole(t, "denied")

	backend := newVaultBackend(VaultConfig{
		Address: live.address, RoleID: roleID, SecretID: secretID, Path: "denied",
	})
	require.NoError(t, backend.Login(t.Context()))
	source := New(backend)

	_, found, err := source.Lookup(withFeatures(t.Context(), "enterprise_secrets"), "DATABASE_URL")
	require.False(t, found)
	require.Error(t, err)

	var rejected *extension.CredentialRejectedError
	require.ErrorAs(t, err, &rejected)
	require.Contains(t, rejected.Source, live.address)
	t.Logf("reports: %s", rejected.Error())
}

func TestVaultRefusesToBeBuiltWithoutWhatItNeeds(t *testing.T) {
	// Refused at construction rather than at first use. A source that is
	// registered and can never answer is a line in the list somebody reads to
	// decide where to put a value.
	for _, tc := range []struct {
		name string
		cfg  VaultConfig
		want string
	}{
		{"no address", VaultConfig{Token: "t", Path: "p"}, "address"},
		{"no credential", VaultConfig{Address: "http://v", Path: "p"}, "token"},
		{"a role id with no secret id", VaultConfig{
			Address: "http://v", RoleID: "r", Path: "p"}, "secret id"},
		{"no path", VaultConfig{Address: "http://v", Token: "t"}, "path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewVault(tc.cfg)
			require.Error(t, err)
			require.ErrorIs(t, err, ErrNotConfigured)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}
