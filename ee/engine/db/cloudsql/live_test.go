// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package cloudsql_test

// The live proof against Google Cloud SQL, and the counterpart of azurepg's
// TestLivePrivateAzureRestoreMaskBranchAndDelete. It is skipped unless its
// driver sets AF_CLOUDSQL_LIVE=1, and that driver runs it from one machine
// against a public IP source whose ONLY authorized network is that machine's
// egress address as a /32.
//
// Every provider call is the customer's code path, with two stated exceptions:
//
//   - The Google identity. The provider takes a key file or the metadata
//     server (api.go newAdminAPI), and neither exists here: key creation is
//     blocked by organization policy and a laptop has no metadata server. So
//     Options.Token is set to an access token from gcloud for the proof
//     account, held in memory. Options.Token is the provider's own documented
//     seam for an externally managed identity.
//   - The HTTP transport is the provider's own guarded client wrapped in an
//     observer. It keeps the first successful read of each instance and every
//     finished operation, and changes nothing, with one exception that only
//     ever makes the run fail: a clone whose first read is not network isolated
//     (exactly one authorized network, a single IPv4 /32, public IPv4 on, TLS
//     required) is refused before the provider can connect to it, so the
//     provider's own cleanup removes it.
//
// The first read of a clone happens straight after the clone operation and
// before the provider writes a label or opens a connection, so it is where this
// test sees what Cloud SQL copied onto a clone: user labels, authorized
// networks, TLS mode, CA mode. The provider sets none of those on a clone, so
// every one of them is inherited or defaulted by Google, and none of that is
// established by documentation.
//
// What this cannot prove: that a clone took the fast workflow. The database is
// a few rows, so a standard clone and a fast clone take the same time here.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/db/cloudsql"
	"github.com/antifailure/antifailure/engine/pkg/airgap"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

const (
	// liveVariable opts in. Nothing else about the environment does.
	liveVariable = "AF_CLOUDSQL_LIVE"
	// liveSourceVariable names the disposable source instance.
	liveSourceVariable = "AF_CLOUDSQL_LIVE_SOURCE"
	// liveHostVariable is the source's public address, used only to seed it.
	liveHostVariable = "AF_CLOUDSQL_LIVE_HOST"
	// liveAccountVariable is the gcloud account the access token is issued for.
	liveAccountVariable = "AF_CLOUDSQL_LIVE_ACCOUNT"
	// livePasswordFileVariable names a file holding the source's postgres
	// password, used only to seed it. The provider never receives it.
	livePasswordFileVariable = "AF_CLOUDSQL_LIVE_PASSWORD_FILE"
	// liveBranchKeyFileVariable names a file holding the branch key.
	liveBranchKeyFileVariable = "AF_CLOUDSQL_LIVE_BRANCH_KEY_FILE"
	// liveSourcePrefix is the naming convention of an owned disposable source.
	liveSourcePrefix = "af-proof-src-"
	// liveSeedLabel is set on the source by the driver, so that its presence
	// on a clone's first read answers whether Cloud SQL copies user labels.
	liveSeedLabel = "af-proof-seed"
	liveSeedValue = "source"
	// liveSSLMode is the server side TLS requirement every instance must carry.
	liveSSLMode = "ENCRYPTED_ONLY"
	// liveReaderRole is a login the test creates on the source. A branch must
	// refuse it and the source must keep accepting it.
	liveReaderRole     = "af_proof_reader"
	liveReaderPassword = "AF_FAKE_INHERITED_PASSWORD"
	liveAdminAPI       = "https://sqladmin.googleapis.com"
)

func TestLiveCloudSQLCloneMaskBranchAndDelete(t *testing.T) {
	if os.Getenv(liveVariable) != "1" {
		t.Skip("requires the disposable Cloud SQL proof driver")
	}
	project := os.Getenv(cloudsql.ProjectVariable)
	region := os.Getenv(cloudsql.RegionVariable)
	sourceName := os.Getenv(liveSourceVariable)
	host := os.Getenv(liveHostVariable)
	account := os.Getenv(liveAccountVariable)
	require.NotEmpty(t, project)
	require.NotEmpty(t, region)
	require.True(t, strings.HasPrefix(sourceName, liveSourcePrefix), "live proof requires an owned disposable source instance")
	require.NotEmpty(t, host)
	require.NotEmpty(t, account)
	require.Empty(t, os.Getenv(cloudsql.EndpointVariable), "the live proof must reach the real Admin API")
	require.Empty(t, os.Getenv(cloudsql.TLSModeVariable), "the live proof must take the provider's default verification")
	require.Empty(t, os.Getenv(cloudsql.DefaultVariable), "the branch key arrives in a file, never in the environment")
	password := liveSecretFile(t, livePasswordFileVariable)
	branchKey := liveSecretFile(t, liveBranchKeyFileVariable)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Minute)
	defer cancel()

	// The source is seeded over TLS without verification. This connection is
	// the test's own and not the provider's, which never connects to a source.
	sourceURL := &url.URL{Scheme: "postgres", Host: host + ":5432", Path: "/postgres", User: url.UserPassword("postgres", password), RawQuery: "sslmode=require"}
	source, err := sql.Open("pgx", sourceURL.String())
	require.NoError(t, err)
	defer func() { _ = source.Close() }()
	_, err = source.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS af_live_proof (id integer PRIMARY KEY, value text NOT NULL); INSERT INTO af_live_proof VALUES (1, 'synthetic-original') ON CONFLICT (id) DO UPDATE SET value=EXCLUDED.value")
	require.NoError(t, err)
	_, err = source.ExecContext(ctx, "DO $proof$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='"+liveReaderRole+"') THEN CREATE ROLE "+liveReaderRole+"; END IF; END $proof$; ALTER ROLE "+liveReaderRole+" LOGIN PASSWORD '"+liveReaderPassword+"'")
	require.NoError(t, err)

	client := airgap.Client(airgap.SiteCloudSQL, 60*time.Second)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	observer := &liveObserver{inner: client, first: map[string][]byte{}}
	tokens := &liveGcloudToken{account: account}
	reader := &liveReader{client: client, project: project, token: tokens.Token}

	sourceState, status, err := reader.instance(ctx, sourceName)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status, "the source instance could not be read")
	require.Equal(t, liveSeedValue, sourceState.Settings.UserLabels[liveSeedLabel], "the source does not carry the seed label, so the label inheritance observation would mean nothing")
	require.Empty(t, liveNetworkProblem(sourceState), "the source is not network isolated")
	t.Logf("AF_OBSERVED source %s", describeInstance(sourceState))

	p, err := cloudsql.New(ctx, cloudsql.Options{
		Project:        project,
		Region:         region,
		SourceInstance: sourceName,
		BranchKey:      secret.New(branchKey),
		Variable:       cloudsql.DefaultVariable,
		Database:       "postgres",
		Getenv:         os.Getenv,
		Token:          tokens.Token,
		HTTPClient:     observer,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	// Best effort removal if an assertion stops the run part way. Registered
	// after Close, so it runs before it. The driver's sweep is the real guard
	// and deletes anything this leaves.
	finished := false
	var createdBranch provider.Branch
	createdGolden := ""
	t.Cleanup(func() {
		if finished {
			return
		}
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Minute)
		defer stop()
		if createdBranch.ProviderRef != "" {
			if err := p.Destroy(cleanup, createdBranch); err != nil {
				t.Logf("cleanup could not destroy the branch %s: %v", createdBranch.ProviderRef, err)
			}
		}
		if createdGolden != "" {
			if err := p.DestroyGolden(cleanup, createdGolden); err != nil {
				t.Logf("cleanup could not destroy the golden %s: %v", createdGolden, err)
			}
		}
	})

	inventory, err := p.Inventory(ctx)
	require.NoError(t, err)
	require.Empty(t, inventory, "the provider already holds instances for this disposable source")

	query := func(ctx context.Context, connection secret.Value, statement string) (string, error) {
		db, err := sql.Open("pgx", connection.Reveal())
		if err != nil {
			return "", err
		}
		defer func() { _ = db.Close() }()
		var value string
		err = db.QueryRowContext(ctx, statement).Scan(&value)
		return value, err
	}

	t.Log("source seeded; starting golden clone")
	goldenStarted := time.Now()
	golden, err := p.RefreshGolden(ctx, provider.GoldenSpec{RulesHash: "live-private-proof", Provenance: "disposable synthetic rows", Mask: func(ctx context.Context, connection secret.Value) error {
		value, err := query(ctx, connection, "UPDATE af_live_proof SET value='masked' RETURNING value")
		if err == nil && value != "masked" {
			return fmt.Errorf("mask did not update the cloned row")
		}
		return err
	}, Verify: func(ctx context.Context, connection secret.Value) (string, error) {
		value, err := query(ctx, connection, "SELECT value FROM af_live_proof WHERE id=1")
		if err != nil {
			return "", err
		}
		if value != "masked" {
			return "", fmt.Errorf("cloned row was not masked")
		}
		return "live-private-row-verified", nil
	}})
	if golden.ID != "" {
		createdGolden = golden.ID
	}
	require.Empty(t, observer.refusals(), "a clone came up without network isolation and was refused")
	require.NoError(t, err)
	require.True(t, golden.Verified)
	t.Logf("AF_MEASURED golden_seconds=%.1f (clone of the source, credential preparation, mask, verify, publish)", time.Since(goldenStarted).Seconds())

	goldenFirst := observer.firstRead(t, golden.ProviderRef)
	_, inherited := goldenFirst.Settings.UserLabels[liveSeedLabel]
	t.Logf("AF_OBSERVED golden_clone_labels_before_relabel %s", describeLabels(goldenFirst.Settings.UserLabels))
	t.Logf("AF_OBSERVED clone_copies_user_labels=%t (the source carries %s=%s)", inherited, liveSeedLabel, liveSeedValue)
	t.Logf("AF_OBSERVED golden_clone_first_read %s", describeInstance(goldenFirst))

	t.Log("golden masked and verified; starting branch clone")
	branchStarted := time.Now()
	branch, err := p.Branch(ctx, golden.ID, "live-private-proof")
	if branch.ProviderRef != "" {
		createdBranch = branch
	}
	require.Empty(t, observer.refusals(), "a clone came up without network isolation and was refused")
	require.NoError(t, err)
	t.Logf("AF_MEASURED branch_seconds=%.1f (Branch returning a published branch)", time.Since(branchStarted).Seconds())

	branchFirst := observer.firstRead(t, branch.ProviderRef)
	t.Logf("AF_OBSERVED branch_clone_labels_before_relabel %s", describeLabels(branchFirst.Settings.UserLabels))
	t.Logf("AF_OBSERVED branch_clone_first_read %s", describeInstance(branchFirst))

	connection, err := p.ConnString(ctx, branch, provider.ConnDirect)
	require.NoError(t, err)
	branchURL, err := url.Parse(connection.Reveal())
	require.NoError(t, err)
	mode := branchURL.Query().Get("sslmode")
	require.Contains(t, []string{"verify-ca", "verify-full"}, mode, "the branch connection string does not verify the server")
	require.NotEmpty(t, branchURL.Query().Get("sslrootcert"), "the branch connection string names no CA bundle")

	// Read again rather than trusting the first read: the network must still
	// be isolated after every patch the provider made.
	branchState, status, err := reader.instance(ctx, branch.ProviderRef)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.Empty(t, liveNetworkProblem(branchState), "the branch is not network isolated")
	public := ""
	for _, address := range branchState.IPAddresses {
		if address.Type == "PRIMARY" {
			public = address.IPAddress
		}
	}
	require.NotEmpty(t, public, "the branch has no public address to reach it on")
	if mode == "verify-ca" {
		require.Equal(t, public, branchURL.Hostname(), "the branch connection string does not point at the branch's public address")
	}
	t.Logf("AF_OBSERVED branch_connection sslmode=%s host_is_public_address=%t", mode, branchURL.Hostname() == public)
	goldenState, status, err := reader.instance(ctx, golden.ProviderRef)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.Empty(t, liveNetworkProblem(goldenState), "the golden is not network isolated")

	value, err := query(ctx, connection, "SELECT value FROM af_live_proof WHERE id=1")
	require.NoError(t, err)
	require.Equal(t, "masked", value)
	// A write to the branch must land there and nowhere else: the source keeps
	// its original row, and the branch reads back what was written.
	written, err := query(ctx, connection, "UPDATE af_live_proof SET value='written-on-branch' WHERE id=1 RETURNING value")
	require.NoError(t, err)
	require.Equal(t, "written-on-branch", written)
	value, err = query(ctx, connection, "SELECT value FROM af_live_proof WHERE id=1")
	require.NoError(t, err)
	require.Equal(t, "written-on-branch", value)
	var original string
	require.NoError(t, source.QueryRowContext(ctx, "SELECT value FROM af_live_proof WHERE id=1").Scan(&original))
	require.Equal(t, "synthetic-original", original, "a write to the branch reached the source")

	// The copied login must be refused BY AUTHENTICATION. Any other failure,
	// a TLS mismatch or a timeout, would pass a bare require.Error while
	// proving nothing about credentials.
	readerURL := *branchURL
	readerURL.User = url.UserPassword(liveReaderRole, liveReaderPassword)
	refused, err := sql.Open("pgx", readerURL.String())
	require.NoError(t, err)
	pingErr := refused.PingContext(ctx)
	_ = refused.Close()
	require.Error(t, pingErr, "a copied source login still authenticates to the branch")
	var databaseErr *pgconn.PgError
	require.True(t, errors.As(pingErr, &databaseErr), "the copied login failed for a reason other than authentication: %v", pingErr)
	require.Contains(t, []string{"28P01", "28000"}, databaseErr.Code, "the copied login failed with a code that is not an authentication refusal")
	// The source is reached with the source's own TLS settings. The branch
	// string verifies against the branch's CA, which the source does not use.
	sourceReaderURL := *sourceURL
	sourceReaderURL.User = url.UserPassword(liveReaderRole, liveReaderPassword)
	sourceReader, err := sql.Open("pgx", sourceReaderURL.String())
	require.NoError(t, err)
	require.NoError(t, sourceReader.PingContext(ctx), "the source login must stay unchanged")
	_ = sourceReader.Close()

	// The label inheritance question, asked of the provider rather than of
	// the labels: with a branch in place exactly one golden is listed.
	listed, err := p.ListGoldens(ctx)
	require.NoError(t, err)
	require.Len(t, listed, 1, "with a branch in place the project must list exactly one golden; a second one is the clone taken for a golden")
	require.Equal(t, golden.ID, listed[0].ID)
	require.Equal(t, golden.ProviderRef, listed[0].ProviderRef)
	inventory, err = p.Inventory(ctx)
	require.NoError(t, err)
	kinds := map[string]string{}
	for _, resource := range inventory {
		kinds[resource.ID] = resource.Kind
	}
	require.Equal(t, map[string]string{golden.ProviderRef: "golden", branch.ProviderRef: "branch"}, kinds)

	require.NoError(t, p.Destroy(ctx, branch))
	_, status, err = reader.instance(ctx, branch.ProviderRef)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, status, "branch must no longer exist")
	createdBranch = provider.Branch{}
	require.NoError(t, p.DestroyGolden(ctx, golden.ID))
	_, status, err = reader.instance(ctx, golden.ProviderRef)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, status, "golden must no longer exist")
	createdGolden = ""
	inventory, err = p.Inventory(ctx)
	require.NoError(t, err)
	require.Empty(t, inventory)
	listed, err = p.ListGoldens(ctx)
	require.NoError(t, err)
	require.Empty(t, listed)

	clones := 0
	for _, op := range observer.completed() {
		if op.OperationType != "CLONE" {
			continue
		}
		clones++
		started, startErr := time.Parse(time.RFC3339Nano, op.StartTime)
		ended, endErr := time.Parse(time.RFC3339Nano, op.EndTime)
		if startErr != nil || endErr != nil {
			t.Logf("AF_OBSERVED clone_operation target=%s start=%q end=%q (times did not parse)", op.TargetID, op.StartTime, op.EndTime)
			continue
		}
		t.Logf("AF_MEASURED clone_operation_seconds=%.1f target=%s (the Admin API's own start and end times)", ended.Sub(started).Seconds(), op.TargetID)
	}
	t.Logf("AF_OBSERVED clone_operations_observed=%d", clones)
	finished = true
	t.Log("branch served masked rows; source unchanged; every clone network isolated; branch and golden deleted")
}

// liveInstance is the part of the instance resource this proof reads. It is
// the test's own decoding, so a field the provider does not read can still be
// checked and recorded.
type liveInstance struct {
	Name        string `json:"name"`
	State       string `json:"state"`
	IPAddresses []struct {
		Type      string `json:"type"`
		IPAddress string `json:"ipAddress"`
	} `json:"ipAddresses"`
	Settings struct {
		UserLabels                map[string]string `json:"userLabels"`
		DeletionProtectionEnabled bool              `json:"deletionProtectionEnabled"`
		Tier                      string            `json:"tier"`
		Edition                   string            `json:"edition"`
		DataDiskType              string            `json:"dataDiskType"`
		DatabaseFlags             []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"databaseFlags"`
		IPConfiguration struct {
			IPv4Enabled        bool   `json:"ipv4Enabled"`
			PrivateNetwork     string `json:"privateNetwork"`
			ServerCAMode       string `json:"serverCaMode"`
			SSLMode            string `json:"sslMode"`
			RequireSSL         bool   `json:"requireSsl"`
			AuthorizedNetworks []struct {
				Value string `json:"value"`
			} `json:"authorizedNetworks"`
		} `json:"ipConfiguration"`
	} `json:"settings"`
}

// liveNetworkProblem names every way an instance falls short of isolation on
// this topology, or returns the empty string. Exactly one authorized network,
// a single IPv4 host, public IPv4 on so the one host can reach it, and TLS
// required at the server. A missing field is a problem, not a pass.
func liveNetworkProblem(in liveInstance) string {
	ip := in.Settings.IPConfiguration
	var problems []string
	if !ip.IPv4Enabled {
		problems = append(problems, "public IPv4 is off")
	}
	values := make([]string, 0, len(ip.AuthorizedNetworks))
	for _, network := range ip.AuthorizedNetworks {
		values = append(values, network.Value)
	}
	if len(values) != 1 {
		problems = append(problems, fmt.Sprintf("%d authorized networks %v, want exactly one IPv4 /32", len(values), values))
	} else if !liveSingleHost(values[0]) {
		problems = append(problems, fmt.Sprintf("authorized network %q is not a single IPv4 /32", values[0]))
	}
	if ip.SSLMode != liveSSLMode {
		problems = append(problems, fmt.Sprintf("sslMode %q, want %s", ip.SSLMode, liveSSLMode))
	}
	return strings.Join(problems, "; ")
}

// liveSingleHost reports whether a CIDR is exactly one IPv4 address.
func liveSingleHost(value string) bool {
	address, network, err := net.ParseCIDR(value)
	if err != nil || address.To4() == nil {
		return false
	}
	ones, bits := network.Mask.Size()
	return ones == 32 && bits == 32
}

// liveOperation is a finished operation as the Admin API reported it. Times
// stay strings so that a shape this test does not expect is logged rather than
// failing the decode.
type liveOperation struct {
	OperationType string `json:"operationType"`
	TargetID      string `json:"targetId"`
	Status        string `json:"status"`
	StartTime     string `json:"startTime"`
	EndTime       string `json:"endTime"`
}

// liveObserver wraps the provider's transport. It records the first
// successful read of each instance and each finished operation, and refuses a
// clone whose first read is not network isolated.
type liveObserver struct {
	inner      *http.Client
	mu         sync.Mutex
	first      map[string][]byte
	operations []liveOperation
	refused    []string
}

func (o *liveObserver) Do(req *http.Request) (*http.Response, error) {
	resp, err := o.inner.Do(req)
	if err != nil || req.Method != http.MethodGet || resp.StatusCode != http.StatusOK {
		return resp, err
	}
	segments := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
	if len(segments) != 5 || segments[0] != "v1" || segments[1] != "projects" {
		return resp, nil
	}
	isInstance := segments[3] == "instances"
	isOperation := segments[3] == "operations"
	if !isInstance && !isOperation {
		return resp, nil
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		// Hand the provider exactly what it would have seen: the bytes that
		// arrived, then the same read error.
		resp.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), liveErrReader{readErr}))
		return resp, nil
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	o.mu.Lock()
	defer o.mu.Unlock()
	if isInstance {
		name := segments[4]
		if _, seen := o.first[name]; seen {
			return resp, nil
		}
		o.first[name] = body
		if !strings.HasPrefix(name, "af-g-") && !strings.HasPrefix(name, "af-b-") {
			return resp, nil
		}
		// A clone's first read comes before any label write and any
		// connection. Refusing it here makes the provider's own deferred
		// cleanup remove the clone, before a single row is reachable on it.
		var in liveInstance
		problem := "its first read did not decode"
		if json.Unmarshal(body, &in) == nil {
			problem = liveNetworkProblem(in)
		}
		if problem != "" {
			o.refused = append(o.refused, name+": "+problem)
			return nil, fmt.Errorf("live proof refused the clone %s before any connection: %s", name, problem)
		}
		return resp, nil
	}
	var op liveOperation
	if json.Unmarshal(body, &op) == nil && op.Status == "DONE" {
		o.operations = append(o.operations, op)
	}
	return resp, nil
}

func (o *liveObserver) firstRead(t *testing.T, name string) liveInstance {
	t.Helper()
	o.mu.Lock()
	body, ok := o.first[name]
	o.mu.Unlock()
	require.True(t, ok, "the provider never read %s back after cloning it", name)
	var in liveInstance
	require.NoError(t, json.Unmarshal(body, &in))
	return in
}

func (o *liveObserver) completed() []liveOperation {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]liveOperation(nil), o.operations...)
}

func (o *liveObserver) refusals() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.refused...)
}

type liveErrReader struct{ err error }

func (r liveErrReader) Read([]byte) (int, error) { return 0, r.err }

// liveReader reads instances with the same client and identity the provider
// uses. It never writes.
type liveReader struct {
	client  *http.Client
	project string
	token   func(context.Context) (string, error)
}

func (r *liveReader) instance(ctx context.Context, name string) (liveInstance, int, error) {
	var in liveInstance
	token, err := r.token(ctx)
	if err != nil {
		return in, 0, err
	}
	endpoint := liveAdminAPI + "/v1/projects/" + url.PathEscape(r.project) + "/instances/" + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return in, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := r.client.Do(req)
	if err != nil {
		return in, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return in, resp.StatusCode, err
	}
	if resp.StatusCode != http.StatusOK {
		return in, resp.StatusCode, nil
	}
	return in, resp.StatusCode, json.Unmarshal(body, &in)
}

// liveGcloudToken issues access tokens for one gcloud account.
//
// The provider asks for a token on every Admin API request (api.go do), so
// the token is kept for five minutes rather than one gcloud process per
// request. Five rather than the token's hour, because gcloud may hand back a
// cached token that is already part way through its life. The token lives
// only in this struct: it is never logged, never an argument and never a file.
type liveGcloudToken struct {
	account string
	mu      sync.Mutex
	token   string
	fetched time.Time
}

func (g *liveGcloudToken) Token(ctx context.Context) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.token != "" && time.Since(g.fetched) < 5*time.Minute {
		return g.token, nil
	}
	var stdout, stderr bytes.Buffer
	command := exec.CommandContext(ctx, "gcloud", "--account="+g.account, "auth", "print-access-token")
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("gcloud could not issue an access token for the proof account: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	token := strings.TrimSpace(stdout.String())
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return "", fmt.Errorf("gcloud returned no usable access token")
	}
	g.token, g.fetched = token, time.Now()
	return token, nil
}

// liveSecretFile reads a secret from the file a variable names. The value is
// never logged; only the variable name appears in a failure.
func liveSecretFile(t *testing.T, variable string) string {
	t.Helper()
	path := os.Getenv(variable)
	require.NotEmpty(t, path, "%s must name a file", variable)
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "reading the file %s names", variable)
	value := strings.TrimSpace(string(raw))
	require.NotEmpty(t, value, "the file %s names is empty", variable)
	return value
}

// describeLabels renders labels for the log. Values that identify what an
// instance is are printed; every other value is printed as its length only.
func describeLabels(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		switch key {
		case "antifailure", "antifailure-kind", "antifailure-golden", "af-proof-run", liveSeedLabel:
			parts = append(parts, key+"="+labels[key])
		default:
			parts = append(parts, fmt.Sprintf("%s=(%d characters)", key, len(labels[key])))
		}
	}
	if len(parts) == 0 {
		return "(none)"
	}
	return strings.Join(parts, " ")
}

// describeInstance renders the settings whose inheritance by a clone is not
// established: authorized networks, TLS mode, CA mode, deletion protection,
// flags.
func describeInstance(in liveInstance) string {
	flags := make([]string, 0, len(in.Settings.DatabaseFlags))
	for _, flag := range in.Settings.DatabaseFlags {
		flags = append(flags, flag.Name+"="+flag.Value)
	}
	sort.Strings(flags)
	networks := make([]string, 0, len(in.Settings.IPConfiguration.AuthorizedNetworks))
	for _, network := range in.Settings.IPConfiguration.AuthorizedNetworks {
		networks = append(networks, network.Value)
	}
	return fmt.Sprintf("name=%s state=%s tier=%s edition=%s disk=%s server_ca_mode=%s ssl_mode=%s require_ssl=%t ipv4_enabled=%t authorized_networks=[%s] private_network_set=%t deletion_protection=%t flags=[%s]",
		in.Name, in.State, in.Settings.Tier, in.Settings.Edition, in.Settings.DataDiskType,
		in.Settings.IPConfiguration.ServerCAMode, in.Settings.IPConfiguration.SSLMode, in.Settings.IPConfiguration.RequireSSL,
		in.Settings.IPConfiguration.IPv4Enabled, strings.Join(networks, " "), in.Settings.IPConfiguration.PrivateNetwork != "",
		in.Settings.DeletionProtectionEnabled, strings.Join(flags, " "))
}
