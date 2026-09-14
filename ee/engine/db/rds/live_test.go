// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds_test

// TestAgainstRealRDS runs the provider's whole snapshot and restore path
// against a real Amazon RDS for PostgreSQL instance, through the registration
// the engine opens a manifest's provider with, and records how long every
// control plane request took.
//
// It is skipped unless AF_RDS_LIVE_SOURCE names a source instance, because it
// creates and deletes real instances and snapshots, and that costs money. The
// provider restores privately and connects to what it restores, so the run
// has to start from inside the source's VPC.
//
// What it checks, each against the real service:
//
//   - a golden is a snapshot of the source restored into a candidate that is
//     masked and verified over TLS, and masking the candidate leaves the source
//     untouched;
//   - a branch restored from that golden holds exactly the golden's masked rows,
//     none of the rows written to the source after the golden was taken, and
//     none of them unmasked;
//   - a write to the branch never reaches the source;
//   - the branch refuses the source's master password, because the provider
//     rotated it before anything connected;
//   - destroying the branch and the golden leaves nothing of the provider's in
//     the account.
//
// The variables, all of them what a manifest's environment would supply except
// the three that describe the source for seeding it:
//
//	AF_RDS_LIVE_SOURCE           the source DB instance identifier
//	AF_RDS_LIVE_SOURCE_HOST      the source's endpoint address
//	AF_RDS_LIVE_SOURCE_PASSWORD  the source's master password (user afproof, database proof)
//	AF_RDS_LIVE_ROOTS            a PEM file of the RDS root authorities, for the seeding connection
//	AF_RDS_LIVE_RECORD           where the JSON record of the run is written
//	AWS_REGION, AF_RDS_BRANCH_KEY

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/db/rds"
	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

type liveCall struct {
	Action string  `json:"action"`
	Status int     `json:"status"`
	Millis float64 `json:"ms"`
	At     string  `json:"at"`
	Error  string  `json:"error,omitempty"`
}

type livePhase struct {
	Name    string  `json:"name"`
	Seconds float64 `json:"seconds"`
	Error   string  `json:"error,omitempty"`
}

type liveRecord struct {
	Source   string            `json:"source"`
	Started  string            `json:"started"`
	Finished string            `json:"finished,omitempty"`
	Phases   []livePhase       `json:"phases"`
	Calls    []liveCall        `json:"calls"`
	Progress []string          `json:"progress"`
	Checks   map[string]string `json:"checks"`
}

// timingTransport records each control plane request's action, status and the
// time from sending it to its response headers.
type timingTransport struct {
	base http.RoundTripper
	mu   *sync.Mutex
	rec  *liveRecord
}

func (t *timingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	action := req.URL.Query().Get("Action")
	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		if form, err := url.ParseQuery(string(body)); err == nil && form.Get("Action") != "" {
			action = form.Get("Action")
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
		req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	}
	started := time.Now()
	resp, err := t.base.RoundTrip(req)
	call := liveCall{
		Action: action,
		Millis: float64(time.Since(started).Microseconds()) / 1000,
		At:     started.UTC().Format(time.RFC3339Nano),
	}
	if err != nil {
		call.Error = err.Error()
	} else {
		call.Status = resp.StatusCode
	}
	t.mu.Lock()
	t.rec.Calls = append(t.rec.Calls, call)
	t.mu.Unlock()
	return resp, err
}

func TestAgainstRealRDS(t *testing.T) {
	source := os.Getenv("AF_RDS_LIVE_SOURCE")
	if source == "" {
		t.Skip("skipped: set AF_RDS_LIVE_SOURCE to run against a real RDS account. It creates " +
			"and deletes instances and snapshots, which costs money")
	}
	need := func(name string) string {
		value := os.Getenv(name)
		require.NotEmptyf(t, value, "%s is required for the live run", name)
		return value
	}
	host := need("AF_RDS_LIVE_SOURCE_HOST")
	password := need("AF_RDS_LIVE_SOURCE_PASSWORD")
	roots := need("AF_RDS_LIVE_ROOTS")
	recordPath := need("AF_RDS_LIVE_RECORD")
	need("AWS_REGION")
	need("AF_RDS_BRANCH_KEY")

	ctx := context.Background()
	var mu sync.Mutex
	rec := &liveRecord{Source: source, Started: time.Now().UTC().Format(time.RFC3339), Checks: map[string]string{}}
	save := func() {
		mu.Lock()
		defer mu.Unlock()
		out, err := json.MarshalIndent(rec, "", "  ")
		if err == nil {
			_ = os.WriteFile(recordPath, out, 0o644)
		}
	}
	defer func() {
		rec.Finished = time.Now().UTC().Format(time.RFC3339)
		save()
	}()
	check := func(name, value string) {
		mu.Lock()
		rec.Checks[name] = value
		mu.Unlock()
		t.Logf("check %s: %s", name, value)
	}
	phase := func(name string, fn func() error) error {
		started := time.Now()
		err := fn()
		ph := livePhase{Name: name, Seconds: time.Since(started).Seconds()}
		if err != nil {
			ph.Error = err.Error()
		}
		mu.Lock()
		rec.Phases = append(rec.Phases, ph)
		mu.Unlock()
		t.Logf("phase %q took %.1fs, error %v", name, ph.Seconds, err)
		save()
		return err
	}
	open := func(conn string) *sql.DB {
		db, err := sql.Open("pgx", conn)
		require.NoError(t, err)
		return db
	}
	digest := func(db *sql.DB) (int, string) {
		var n int
		var sum string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*), coalesce(md5(string_agg(
			id::text || ':' || email || ':' || note, ',' ORDER BY id)), '') FROM customers WHERE id <= 5000`).Scan(&n, &sum))
		return n, sum
	}
	count := func(db *sql.DB, where string) int {
		var n int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM customers WHERE `+where).Scan(&n))
		return n
	}
	usesTLS := func(db *sql.DB) bool {
		var ssl bool
		require.NoError(t, db.QueryRowContext(ctx, `SELECT ssl FROM pg_stat_ssl WHERE pid = pg_backend_pid()`).Scan(&ssl))
		return ssl
	}

	// The source, seeded with rows whose emails the golden has to mask.
	sourceURL := (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword("afproof", password),
		Host:     host + ":5432",
		Path:     "/proof",
		RawQuery: url.Values{"sslmode": {"verify-full"}, "sslrootcert": {roots}}.Encode(),
	}).String()
	src := open(sourceURL)
	defer src.Close()
	require.NoError(t, phase("seed the source with 5000 rows", func() error {
		for _, stmt := range []string{
			`DROP TABLE IF EXISTS customers`,
			`CREATE TABLE customers (id int PRIMARY KEY, email text NOT NULL, note text NOT NULL)`,
			`INSERT INTO customers SELECT g, 'person' || g || '@example.com', 'row ' || g FROM generate_series(1, 5000) g`,
		} {
			if _, err := src.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		return nil
	}))
	check("source connection uses TLS with verify-full", fmt.Sprint(usesTLS(src)))
	sourceRows, sourceSum := digest(src)
	require.Equal(t, 5000, sourceRows)
	check("source rows and digest before the refresh", fmt.Sprintf("%d %s", sourceRows, sourceSum))

	// The provider, opened the way the engine opens a manifest's provider.
	lookup := func(_ context.Context, name string) (secret.Value, bool, error) {
		value, found := os.LookupEnv(name)
		return secret.NewFrom(value, "the live run's environment"), found, nil
	}
	var p *rds.Provider
	require.NoError(t, phase("open the provider through its registration (describes the source)", func() error {
		db, err := rds.Registration{}.Open(ctx, extension.DatabaseConfig{
			Database: schema.Database{Provider: schema.DBProvider(rds.Name), Project: source},
			Version:  17,
			StateDir: t.TempDir(),
			Now:      time.Now,
			Lookup:   lookup,
		})
		if err != nil {
			return err
		}
		var ok bool
		if p, ok = db.(*rds.Provider); !ok {
			return fmt.Errorf("the registration returned %T rather than the RDS provider", db)
		}
		return nil
	}))
	defer func() { _ = p.Close() }()
	rds.WrapHTTPForTest(p, func(base http.RoundTripper) http.RoundTripper {
		return &timingTransport{base: base, mu: &mu, rec: rec}
	})
	p.ReportProgressTo(func(line string) {
		mu.Lock()
		rec.Progress = append(rec.Progress, time.Now().UTC().Format(time.RFC3339)+" "+line)
		mu.Unlock()
		t.Log(line)
	})

	// Whatever fails below, nothing the provider created is left billing.
	var golden provider.GoldenVersion
	var branch provider.Branch
	branchGone, goldenGone := false, false
	defer func() {
		cleanup := context.Background()
		if branch.ProviderRef != "" && !branchGone {
			t.Logf("cleanup: destroying the branch %s after a failure", branch.ProviderRef)
			if err := p.Destroy(cleanup, branch); err != nil {
				t.Errorf("cleanup could not destroy the branch %s: %v", branch.ProviderRef, err)
			}
		}
		if golden.ID != "" && !goldenGone {
			t.Logf("cleanup: destroying the golden %s after a failure", golden.ID)
			if err := p.DestroyGolden(cleanup, golden.ID); err != nil {
				t.Errorf("cleanup could not destroy the golden %s: %v", golden.ID, err)
			}
		}
	}()

	var maskedSum string
	require.NoError(t, phase("refresh a golden (snapshot, restore a candidate, rotate, mask, verify, publish)", func() error {
		var err error
		golden, err = p.RefreshGolden(ctx, provider.GoldenSpec{
			Version:    17,
			RulesHash:  "lane-rds-live-proof-rules-v1",
			Provenance: "lane-rds-live-proof",
			Mask: func(ctx context.Context, candidate secret.Value) error {
				check("candidate connection string asks for verify-full",
					fmt.Sprint(strings.Contains(candidate.Reveal(), "sslmode=verify-full")))
				db := open(candidate.Reveal())
				defer db.Close()
				check("candidate connection uses TLS", fmt.Sprint(usesTLS(db)))
				n, sum := digest(db)
				check("candidate rows and digest before masking (equal to the source's)",
					fmt.Sprintf("%d %s, equal %v", n, sum, n == sourceRows && sum == sourceSum))
				_, err := db.ExecContext(ctx, `UPDATE customers SET email = 'masked-' || id || '@example.invalid'`)
				return err
			},
			Verify: func(ctx context.Context, candidate secret.Value) (string, error) {
				db := open(candidate.Reveal())
				defer db.Close()
				total := count(db, "true")
				unmasked := count(db, "email NOT LIKE 'masked-%'")
				if total != 5000 || unmasked != 0 {
					return "", fmt.Errorf("the candidate holds %d rows, %d of them unmasked", total, unmasked)
				}
				_, maskedSum = digest(db)
				return fmt.Sprintf("lane-rds live proof: %d rows, %d unmasked, digest %s", total, unmasked, maskedSum), nil
			},
		})
		return err
	}))
	check("golden", fmt.Sprintf("version %s, snapshot %s, verified %v", golden.ID, golden.ProviderRef, golden.Verified))
	rows, sum := digest(src)
	check("source unchanged by masking the candidate", fmt.Sprint(rows == sourceRows && sum == sourceSum))
	require.Equal(t, sourceSum, sum, "masking the candidate changed the source")
	require.NotEqual(t, sourceSum, maskedSum, "the masked digest equals the source's, so nothing was masked")

	goldens, err := p.ListGoldens(ctx)
	require.NoError(t, err)
	listed := false
	for _, g := range goldens {
		listed = listed || (g.ID == golden.ID && g.Verified)
	}
	check("the golden is listed as verified", fmt.Sprint(listed))
	require.True(t, listed)

	// Written to the source after the golden was taken, so a branch holding it
	// would be a branch of the source rather than of the golden.
	_, err = src.ExecContext(ctx, `INSERT INTO customers VALUES (900001, 'written-after-golden@example.com', 'after')`)
	require.NoError(t, err)

	require.NoError(t, phase("branch (restore the golden, rotate, close inherited logins, mark prepared)", func() error {
		var err error
		branch, err = p.Branch(ctx, golden.ID, "live-proof")
		return err
	}))
	check("branch", branch.ProviderRef)

	var conn secret.Value
	require.NoError(t, phase("connection string", func() error {
		var err error
		conn, err = p.ConnString(ctx, branch, provider.ConnDirect)
		return err
	}))
	check("branch connection string asks for verify-full", fmt.Sprint(strings.Contains(conn.Reveal(), "sslmode=verify-full")))
	bdb := open(conn.Reveal())
	check("branch connection uses TLS", fmt.Sprint(usesTLS(bdb)))
	rows, sum = digest(bdb)
	check("branch holds the golden's masked rows", fmt.Sprintf("%d rows, digest equal to the golden's %v", rows, sum == maskedSum))
	require.Equal(t, 5000, rows)
	require.Equal(t, maskedSum, sum)
	unmasked := count(bdb, "email NOT LIKE 'masked-%'")
	check("unmasked rows in the branch", fmt.Sprint(unmasked))
	require.Zero(t, unmasked)
	after := count(bdb, "id = 900001")
	check("rows written to the source after the golden, seen in the branch", fmt.Sprint(after))
	require.Zero(t, after)

	_, err = bdb.ExecContext(ctx, `INSERT INTO customers VALUES (900002, 'branch-only@example.invalid', 'branch')`)
	require.NoError(t, err)
	leaked := count(src, "id = 900002")
	check("rows written to the branch, seen in the source", fmt.Sprint(leaked))
	require.Zero(t, leaked)
	rows, sum = digest(src)
	check("source's original rows unchanged after writing to the branch", fmt.Sprint(rows == sourceRows && sum == sourceSum))
	require.Equal(t, sourceSum, sum)
	require.NoError(t, bdb.Close())

	// The source's master password, tried against the branch's endpoint.
	cfg, err := pgx.ParseConfig(conn.Reveal())
	require.NoError(t, err)
	inherited := (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword("afproof", password),
		Host:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Path:     "/proof",
		RawQuery: url.Values{"sslmode": {"verify-full"}, "sslrootcert": {roots}, "connect_timeout": {"20"}}.Encode(),
	}).String()
	idb := open(inherited)
	pingErr := idb.PingContext(ctx)
	_ = idb.Close()
	refused := pingErr != nil && strings.Contains(pingErr.Error(), "password authentication failed")
	check("the branch refuses the source's master password", fmt.Sprint(refused))
	require.True(t, refused, "the branch accepted the source's password or failed another way: %v", pingErr)

	health, err := p.Health(ctx, branch)
	require.NoError(t, err)
	check("branch health", fmt.Sprintf("%+v", health))
	inventory, err := p.Inventory(ctx)
	require.NoError(t, err)
	check("inventory while the branch and golden exist", fmt.Sprint(len(inventory)))

	require.NoError(t, phase("destroy the branch", func() error { return p.Destroy(ctx, branch) }))
	branchGone = true
	health, err = p.Health(ctx, branch)
	require.NoError(t, err)
	check("branch health after destroy", fmt.Sprintf("%+v", health))

	require.NoError(t, phase("destroy the golden", func() error { return p.DestroyGolden(ctx, golden.ID) }))
	goldenGone = true
	goldens, err = p.ListGoldens(ctx)
	require.NoError(t, err)
	check("goldens listed after destroy", fmt.Sprint(len(goldens)))
	inventory, err = p.Inventory(ctx)
	require.NoError(t, err)
	check("inventory after destroy", fmt.Sprint(len(inventory)))
	require.Empty(t, inventory, "the provider left resources behind")
	check("control plane requests the provider counted", fmt.Sprint(p.APICalls()))
}
