// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package cloudsql_test

// The live proof against Google Cloud SQL, and the counterpart of azurepg's
// TestLivePrivateAzureRestoreMaskBranchAndDelete. It is skipped unless its
// runner sets AF_CLOUDSQL_LIVE=1, and that runner is a Cloud Run job inside the
// disposable private network that drive.sh builds and deletes.
//
// Every provider call is the customer's code path: cloudsql.New with the real
// Admin API endpoint, the Google identity from the metadata server or
// GOOGLE_APPLICATION_CREDENTIALS, and the provider's own default TLS
// verification. Two things are added and both only READ:
//
//   - The HTTP transport is the provider's own guarded client wrapped in an
//     observer. It keeps the first successful read of each instance and every
//     finished operation, and changes no byte of any request or response. The
//     first read of a clone happens straight after the clone operation and
//     before the provider writes a single label, so it is the only place a test
//     can see what Cloud SQL copied onto a clone. Whether Cloud SQL copies user
//     labels on a clone is NOT established, and this run records the answer
//     rather than depending on it.
//   - The test reads instances itself through the same client and identity, to
//     check the network shape and that deleted instances are gone.
//
// What this cannot prove: that a clone took the fast workflow. The database is
// a few rows, so a standard clone and a fast clone take the same time here. The
// clone operations' own start and end times are logged for the record, not as a
// verdict about copy on write.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/cloudauth"
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
	// liveHostVariable is the source's PRIVATE address, used only to seed it.
	liveHostVariable = "AF_CLOUDSQL_LIVE_HOST"
	// livePasswordVariable is the source's postgres password, used only to
	// seed it. The provider never receives it.
	livePasswordVariable = "AF_CLOUDSQL_LIVE_PASSWORD"
	// liveSourcePrefix is the naming convention of an owned disposable source.
	liveSourcePrefix = "af-proof-src-"
	// liveSeedLabel is set on the source by the runner, so that its presence
	// on a clone's first read answers whether Cloud SQL copies user labels.
	liveSeedLabel = "af-proof-seed"
	liveSeedValue = "source"
	// liveReader is a login the test creates on the source. A branch must
	// refuse it and the source must keep accepting it.
	liveReaderRole     = "af_proof_reader"
	liveReaderPassword = "AF_FAKE_INHERITED_PASSWORD"
	liveAdminAPI       = "https://sqladmin.googleapis.com"
)

func TestLivePrivateCloudSQLCloneMaskBranchAndDelete(t *testing.T) {
	if os.Getenv(liveVariable) != "1" {
		t.Skip("requires the disposable private Cloud SQL proof runner")
	}
	project := os.Getenv(cloudsql.ProjectVariable)
	region := os.Getenv(cloudsql.RegionVariable)
	sourceName := os.Getenv(liveSourceVariable)
	host := os.Getenv(liveHostVariable)
	password := os.Getenv(livePasswordVariable)
	branchKey := os.Getenv(cloudsql.DefaultVariable)
	require.NotEmpty(t, project)
	require.NotEmpty(t, region)
	require.True(t, strings.HasPrefix(sourceName, liveSourcePrefix), "live proof requires an owned disposable source instance")
	require.NotEmpty(t, host)
	require.NotEmpty(t, password)
	require.NotEmpty(t, branchKey)
	require.Empty(t, os.Getenv(cloudsql.EndpointVariable), "the live proof must reach the real Admin API")
	require.Empty(t, os.Getenv(cloudsql.TLSModeVariable), "the live proof must take the provider's default verification")

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
	token, err := liveToken()
	require.NoError(t, err)
	reader := &liveReader{client: client, project: project, token: token}

	sourceState, status, err := reader.instance(ctx, sourceName)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status, "the source instance could not be read")
	require.Equal(t, liveSeedValue, sourceState.Settings.UserLabels[liveSeedLabel], "the source does not carry the seed label, so the label inheritance observation would mean nothing")
	require.False(t, sourceState.Settings.IPConfiguration.IPv4Enabled, "the source has a public address; this proof is for a private one")
	t.Logf("AF_OBSERVED source %s", describeInstance(sourceState))

	p, err := cloudsql.New(ctx, cloudsql.Options{
		Project:        project,
		Region:         region,
		SourceInstance: sourceName,
		BranchKey:      secret.New(branchKey),
		Variable:       cloudsql.DefaultVariable,
		Database:       "postgres",
		Getenv:         os.Getenv,
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

	t.Log("source seeded; starting private golden clone")
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

	branchState, status, err := reader.instance(ctx, branch.ProviderRef)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.False(t, branchState.Settings.IPConfiguration.IPv4Enabled, "the branch has a public address")
	require.NotEmpty(t, branchState.Settings.IPConfiguration.PrivateNetwork, "the branch is not attached to a private network")
	private := ""
	for _, address := range branchState.IPAddresses {
		require.NotEqual(t, "PRIMARY", address.Type, "the branch answers on a public address")
		if address.Type == "PRIVATE" {
			private = address.IPAddress
		}
	}
	require.NotEmpty(t, private, "the branch has no private address")
	if mode == "verify-ca" {
		require.Equal(t, private, branchURL.Hostname(), "the branch connection string does not point at the branch's private address")
	}
	t.Logf("AF_OBSERVED branch_connection sslmode=%s host_is_private_address=%t", mode, branchURL.Hostname() == private)

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
	t.Log("private branch served masked rows; source unchanged; branch and golden deleted")
}

// liveInstance is the part of the instance resource this proof reads. It is
// the test's own decoding, so a field the provider does not read can still be
// recorded.
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
			IPv4Enabled    bool   `json:"ipv4Enabled"`
			PrivateNetwork string `json:"privateNetwork"`
			ServerCAMode   string `json:"serverCaMode"`
			SSLMode        string `json:"sslMode"`
		} `json:"ipConfiguration"`
	} `json:"settings"`
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

// liveObserver wraps the provider's transport and records, without altering
// anything, the first successful read of each instance and each finished
// operation.
type liveObserver struct {
	inner      *http.Client
	mu         sync.Mutex
	first      map[string][]byte
	operations []liveOperation
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
		if _, seen := o.first[segments[4]]; !seen {
			o.first[segments[4]] = body
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

// liveToken resolves the Google identity the way the provider's own client
// does: a key file when GOOGLE_APPLICATION_CREDENTIALS names one, otherwise the
// metadata server.
func liveToken() (func(context.Context) (string, error), error) {
	var account *cloudauth.GCPServiceAccount
	if path := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		account, err = cloudauth.ParseGCPServiceAccount(raw)
		if err != nil {
			return nil, err
		}
	}
	return cloudauth.NewGCPTokenSource(account, cloudauth.ScopeGoogleCloudPlatform).Token, nil
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
// established: CA mode, TLS mode, deletion protection, public address, flags.
func describeInstance(in liveInstance) string {
	flags := make([]string, 0, len(in.Settings.DatabaseFlags))
	for _, flag := range in.Settings.DatabaseFlags {
		flags = append(flags, flag.Name+"="+flag.Value)
	}
	sort.Strings(flags)
	return fmt.Sprintf("name=%s state=%s tier=%s edition=%s disk=%s server_ca_mode=%s ssl_mode=%s ipv4_enabled=%t private_network_set=%t deletion_protection=%t flags=[%s]",
		in.Name, in.State, in.Settings.Tier, in.Settings.Edition, in.Settings.DataDiskType,
		in.Settings.IPConfiguration.ServerCAMode, in.Settings.IPConfiguration.SSLMode,
		in.Settings.IPConfiguration.IPv4Enabled, in.Settings.IPConfiguration.PrivateNetwork != "",
		in.Settings.DeletionProtectionEnabled, strings.Join(flags, " "))
}
