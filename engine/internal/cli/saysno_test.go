package cli

// The six defect classes the product's verdict claims to refuse, each driven
// through the production line and asserted on the report a person actually
// reads.
//
// WHY THIS FILE EXISTS. Across the 22 most recent merged pull requests in this
// repository, Antifailure's own verdict on itself read "Every check passed.
// 8 passed, 0 failed, 0 flaky, 0 blocked, 0 unverified" twenty one times, and
// "Nothing was verified" twice. The `failed` column has never held a number.
// Twenty one unbroken greens is also exactly what a check that cannot say no
// looks like, and this repository has already shipped that defect twice: a
// database conformance suite that had never refused a provider, and a coverage
// gate that printed a number while skipping the files it could not parse. The
// sentence it keeps writing down is that a check which cannot say no is worse
// than no check, and until this file the sentence had never been pointed at
// the product's own headline.
//
// The pipeline absence path was already proven in production, twice, on #286
// and #288: with nothing to report it said "Nothing was verified" and called
// that a failure of the pipeline rather than a verdict on the diff, instead of
// reporting zero failures as a pass. The ASSERTION path was proven only by
// unit tests over hand built report structures, which is a claim about the
// renderer and not about the product.
//
// WHAT A CASE HERE HAS TO DO, and it is stricter than it sounds:
//
//  1. Break something IN SCOPE. Not the compiler, not a lockfile, not the
//     build. One of the six things the verdict says it decides.
//  2. Drive the REAL production path with a real Postgres, a real browser or
//     a real inventory. A report struct filled in by the test proves that the
//     renderer renders.
//  3. Require the NAMED FINDING in the text the product produces, naming the
//     column, the invariant, the workflow, the lock or the store. An exit code
//     is not a verdict and a red job is not a refusal: a job that fails to
//     build has not said no about anything.
//  4. Restore the break and require the same case to pass, so the case is
//     shown to be LIVE rather than permanently red. A row that only ever fails
//     is the same defect wearing the other sign.
//
// The parent test counts the classes that got all the way through, and refuses
// to report a number it did not measure: on a machine with the Postgres this
// suite needs, a class that did not run is a failure rather than a quiet skip.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/fidelity"
	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/internal/invariant"
	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/internal/verify"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// saysNoClass is one defect class the verdict claims to cover.
//
// Prove returns whether the case actually ran. A case that skipped has not
// proved anything, and the difference between "it refused" and "nothing looked"
// is the entire subject of this file, so it is carried in the return value
// rather than inferred from the absence of a failure.
type saysNoClass struct {
	ID     string
	Break  string
	Expect string
	Prove  func(t *testing.T) bool
}

func saysNoClasses() []saysNoClass {
	return []saysNoClass{
		{
			ID:     "masking_leaks_a_real_value",
			Break:  "a masking rule that does not cover the column holding real addresses",
			Expect: "not verified, with the column named in the comment",
			Prove:  proveMaskingLeakIsNamed,
		},
		{
			ID:     "an_invariant_that_should_fail",
			Break:  "a row the manifest's invariant says cannot exist",
			Expect: "one invariant did not hold, named, and the check fails",
			Prove:  proveInvariantViolationIsNamed,
		},
		{
			ID:     "a_workflow_assertion_that_should_fail",
			Break:  "a page that shows an error instead of what the workflow expects",
			Expect: "1 workflow failed, and which one",
			Prove:  proveWorkflowFailureIsNamed,
		},
		{
			ID:     "a_migration_that_takes_a_blocking_lock",
			Break:  "an ALTER TABLE holding ACCESS EXCLUSIVE past the failing threshold",
			Expect: "refused, with the lock and the table named",
			Prove:  proveBlockingLockIsNamed,
		},
		{
			ID:     "a_golden_that_fails_verification",
			Break:  "a column the verifier cannot read, and one that still holds real data",
			Expect: "unpublishable, and it says which column and why",
			Prove:  proveGoldenRefusalSaysWhy,
		},
		{
			ID:     "a_twin_missing_a_declared_datastore",
			Break:  "a manifest declaring a second store this build has no golden for",
			Expect: "the fidelity score drops and the store is named absent",
			Prove:  proveMissingDatastoreDropsTheScore,
		},
	}
}

// TestSaysNo_TheVerdictRefusesEveryClassItClaimsToCover is the lane's number,
// measured rather than claimed.
//
// It counts the classes that ran AND passed. A skipped subtest reports true
// from t.Run, which is how a suite that examined nothing reports ok, so the
// count comes from the case itself saying it ran.
func TestSaysNo_TheVerdictRefusesEveryClassItClaimsToCover(t *testing.T) {
	classes := saysNoClasses()
	proved := 0
	for _, c := range classes {
		ran := false
		ok := t.Run(c.ID, func(t *testing.T) {
			t.Logf("break:  %s", c.Break)
			t.Logf("expect: %s", c.Expect)
			ran = c.Prove(t)
		})
		if ok && ran {
			proved++
		}
	}
	t.Logf("defect classes the verdict refused by name, with the break restored "+
		"and the case passing again: %d of %d", proved, len(classes))

	// The requirement, on a machine that was supposed to be able to answer.
	// AF_REQUIRE_DATABASE is what CI sets, and it is the same switch every
	// other suite here uses to turn a quiet skip into a loud failure.
	if os.Getenv("AF_REQUIRE_DATABASE") == "" {
		return
	}
	require.Equal(t, len(classes), proved,
		"AF_REQUIRE_DATABASE is set, so every defect class must be proved refusable here; "+
			"a class that could not run has not been shown to say no")
}

// saysNoDatabase makes a database of its own for one case.
//
// A skip when there is no server and a failure when AF_REQUIRE_DATABASE says
// there should be one, which is the rule the rest of this package follows.
func saysNoDatabase(t *testing.T, name string) (*pgx.Conn, secrets.Value, func(), bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	base := gateMaintenanceURL()

	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		cancel()
		if os.Getenv("AF_REQUIRE_DATABASE") != "" {
			t.Fatalf("AF_REQUIRE_DATABASE is set and there is no Postgres at %s: %v",
				redactGateURL(base), err)
		}
		t.Skipf("skipped: no Postgres at %s: %v", redactGateURL(base), err)
		return nil, secrets.Value{}, func() {}, false
	}

	db := "af_saysno_" + name
	_, err = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+db+" WITH (FORCE)")
	require.NoError(t, err)
	_, err = admin.Exec(ctx, "CREATE DATABASE "+db)
	require.NoError(t, err)

	url := secrets.New(gateDatabaseURL(base, db))
	conn, err := pgx.Connect(ctx, url.Reveal())
	require.NoError(t, err)

	return conn, url, func() {
		c, done := context.WithTimeout(context.Background(), 2*time.Minute)
		_ = conn.Close(c)
		_, _ = admin.Exec(c, "DROP DATABASE IF EXISTS "+db+" WITH (FORCE)")
		_ = admin.Close(c)
		done()
		cancel()
	}, true
}

// saysNoRun is the shape of a real pull request comment: the workflows passed,
// so anything the report says no about came from the data, the database or the
// network rather than from a screen.
func saysNoRun(findings []report.Finding) report.Run {
	return report.Run{
		Environment: "shop-pr-1", Branch: "feature/checkout", Commit: "0123456789abcdef",
		Declared:  1,
		Findings:  findings,
		Workflows: []report.Workflow{{Name: "checkout", Verdict: report.VerdictPass}},
	}
}

// ---------------------------------------------------------------------------
// 1. A masking rule that leaks a real value.

const saysNoCustomers = `
CREATE TABLE customers (
  id      bigserial PRIMARY KEY,
  contact text NOT NULL,
  region  text NOT NULL
);
INSERT INTO customers (contact, region)
  SELECT 'dana.whitfield' || g || '@acmecorp.com', 'north'
  FROM generate_series(1, 120) g;
ANALYZE;
`

// proveMaskingLeakIsNamed masks a real database twice with the same code and
// two rule sets, and requires the comment to name the column the second one
// missed.
//
// The break is a rule file, which is the way this actually happens: somebody
// writes a rule for `email`, the column is called `contact`, the plan runs
// clean, the golden looks masked, and every address in it is real.
func proveMaskingLeakIsNamed(t *testing.T) bool {
	conn, _, done, ok := saysNoDatabase(t, "maskingleak")
	if !ok {
		return false
	}
	defer done()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	_, err := conn.Exec(ctx, saysNoCustomers)
	require.NoError(t, err)

	// BROKEN. One rule, for a column this table does not have.
	broken := saysNoMaskAndVerify(t, ctx, conn, masking.Rule{
		Column: "email", Transform: "email", Why: "addresses are personal",
	})
	require.False(t, broken.Clean,
		"the scan read back a column of real addresses and called the branch clean")
	finding := maskingFinding(broken, defaultGate())
	require.NotNil(t, finding, "a branch full of real addresses produced no finding")
	require.Equal(t, ruleMasking, finding.Rule)
	require.Equal(t, report.LevelFail, finding.Level)

	run := saysNoRun([]report.Finding{*finding})
	comment := run.Comment()
	require.Equal(t, report.VerdictFail, run.Verdict())
	require.Contains(t, comment, "public.customers.contact",
		"the comment refuses without naming the column that leaked")
	require.Contains(t, comment, "still parses as real data")
	require.Contains(t, comment, "`masking`")
	require.Error(t, ciExit(run))
	t.Logf("verbatim, the break:\n%s", saysNoHeadlineAndFindings(run))

	// RESTORED. The same code, the same database, one rule that matches.
	restored := saysNoMaskAndVerify(t, ctx, conn, masking.Rule{
		Column: "contact", Transform: "email", Why: "addresses are personal",
	})
	require.True(t, restored.Clean,
		"the same scan still refuses a branch whose addresses were masked: %v", restored.Findings)
	require.Nil(t, maskingFinding(restored, defaultGate()),
		"a clean branch produced a finding, so the case cannot pass and is permanently red")
	clean := saysNoRun(nil)
	require.Equal(t, report.VerdictPass, clean.Verdict())
	require.Contains(t, clean.Comment(), "All 1 workflows passed.")
	t.Logf("verbatim, restored: %s", clean.Headline())
	return true
}

// saysNoMaskAndVerify runs the real masking pipeline and the real read back.
func saysNoMaskAndVerify(
	t *testing.T, ctx context.Context, conn *pgx.Conn, rules ...masking.Rule,
) *report.Verification {
	t.Helper()
	tables, err := masking.ReadCatalog(ctx, conn)
	require.NoError(t, err)
	set, err := masking.NewRuleSet(rules)
	require.NoError(t, err)
	plan := masking.BuildPlan(tables, set.Assign(tables), "saysno")
	require.True(t, plan.Runnable(), masking.DescribeProblems(plan.Problems))

	key, err := masking.NewKey(secrets.New("says-no-project-key"))
	require.NoError(t, err)
	ex, err := masking.NewExecutor(masking.ExecutorOptions{Key: key})
	require.NoError(t, err)
	_, err = ex.Apply(ctx, conn, plan)
	require.NoError(t, err)

	rep, err := verify.Scan(ctx, conn, verify.Options{})
	require.NoError(t, err)
	v, _ := verificationOf(rep)
	return v
}

// ---------------------------------------------------------------------------
// 2. An invariant that should fail.

const saysNoOrders = `
CREATE TABLE orders (
  id          bigserial PRIMARY KEY,
  status      text NOT NULL,
  total_cents int NOT NULL
);
INSERT INTO orders (status, total_cents)
  SELECT 'paid', (g %% 5000) + 100 FROM generate_series(1, 200) g;
ANALYZE;
`

func proveInvariantViolationIsNamed(t *testing.T) bool {
	conn, _, done, ok := saysNoDatabase(t, "invariant")
	if !ok {
		return false
	}
	defer done()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	_, err := conn.Exec(ctx, strings.ReplaceAll(saysNoOrders, "%%", "%"))
	require.NoError(t, err)

	invs := []schema.Invariant{{
		Name:        "no_paid_order_is_negative",
		Description: "a paid order with a negative total is money we cannot collect",
		SQL: "SELECT id, total_cents FROM orders " +
			"WHERE status = 'paid' AND total_cents < 0",
	}}

	// BROKEN. One row the invariant says cannot exist.
	_, err = conn.Exec(ctx, "INSERT INTO orders (status, total_cents) VALUES ('paid', -4200)")
	require.NoError(t, err)

	run := saysNoRun(nil)
	run.Invariants = reportInvariants(
		env.InvariantResults(invariant.Run(ctx, conn, invs, invariant.Options{}), redact.New()))
	require.Len(t, run.Invariants, 1)
	require.Empty(t, run.Invariants[0].Error,
		"the invariant could not be asked, so nothing here is a verdict on the data")
	require.Equal(t, 1, run.InvariantsViolated(),
		"a row the statement selects did not register as a violation")
	require.Equal(t, report.VerdictFail, run.Verdict())
	require.Equal(t, "Every workflow passed and 1 invariant did not hold.", run.Headline())

	comment := run.Comment()
	require.Contains(t, comment, "no_paid_order_is_negative",
		"the comment refuses without naming the invariant")
	require.Contains(t, comment, "-4200",
		"the comment names the invariant without carrying the row that broke it")
	require.Equal(t, aferrors.ExitTestFailure, exitCodeOfSilent(t, ciExit(run)))
	t.Logf("verbatim, the break:\n%s", saysNoHeadlineAndFindings(run))

	// RESTORED. The row goes, and the same statement holds.
	_, err = conn.Exec(ctx, "DELETE FROM orders WHERE total_cents < 0")
	require.NoError(t, err)
	restored := saysNoRun(nil)
	restored.Invariants = reportInvariants(
		env.InvariantResults(invariant.Run(ctx, conn, invs, invariant.Options{}), redact.New()))
	require.Equal(t, 0, restored.InvariantsViolated(),
		"an invariant whose statement returns no rows was reported violated, so the case "+
			"is permanently red and proves nothing")
	require.Equal(t, report.VerdictPass, restored.Verdict())
	require.Equal(t, "All 1 workflows passed, and 1 invariant held.", restored.Headline())
	require.NoError(t, ciExit(restored))
	t.Logf("verbatim, restored: %s", restored.Headline())
	return true
}

// ---------------------------------------------------------------------------
// 3. A workflow assertion that should fail.

// proveWorkflowFailureIsNamed drives the REAL runner, in a real browser,
// against a real page.
//
// The runner is a subprocess with a JSON document in and a JSON document out,
// which is the boundary a person can drive by hand, so this drives it the same
// way engine/internal/env does: node, the runner's entry point, the document on
// standard input. The half that is not production code here is the job
// document this test writes, and the pairing is what defends it: a document
// the runner could not read produces no result at all, so the restored half,
// which requires a PASS from the same document with one word of the page
// changed, fails loudly rather than passing over a boundary nobody exercised.
func proveWorkflowFailureIsNamed(t *testing.T) bool {
	runner, ok := saysNoRunner(t)
	if !ok {
		return false
	}

	// BROKEN. The page shows an error rather than the confirmation.
	broken := saysNoDriveWorkflow(t, runner,
		"Something went wrong. Your payment could not be processed.")
	run := saysNoRun(nil)
	run.Workflows = reportWorkflows(broken.Results)
	require.Len(t, run.Workflows, 1)
	require.Equal(t, report.VerdictFail, run.Workflows[0].Verdict,
		"the real runner read a page showing an error and did not call it a failure; "+
			"detail was %q", run.Workflows[0].Detail)
	require.Contains(t, run.Workflows[0].Detail,
		"The page shows an error rather than what was expected.",
		"the workflow failed for some reason other than the expectation it was given, so "+
			"this case is not about the assertion it claims to be about")
	require.Equal(t, report.VerdictFail, run.Verdict())
	require.Equal(t, "1 workflow failed.", run.Headline())

	comment := run.Comment()
	require.Contains(t, comment, "1 workflow failed.")
	// The table CELL, not the name anywhere in the comment. Two earlier
	// versions of this line could not say no. The first looked for "checkout",
	// which is also in `feature/checkout` in the footer. The second looked for
	// the workflow name anywhere, and the name survives in the trace path
	// inside the folded details block, so the row a person actually reads
	// could say `` | FAILED and the assertion still passed. Mutation testing
	// is what found both: the break that empties the name left this green.
	require.Contains(t, comment, "| `pay_and_confirm` | FAILED |",
		"the comment says one workflow failed without saying which, in the one row "+
			"somebody reads")
	require.Equal(t, aferrors.ExitTestFailure, exitCodeOfSilent(t, ciExit(run)))
	t.Logf("verbatim, the break:\n%s", saysNoHeadlineAndFindings(run))

	// RESTORED. The same workflow, the same document, a page that says what
	// the workflow was told to expect.
	fixed := saysNoDriveWorkflow(t, runner,
		"Your order is confirmed. Confirmation number 88213.")
	restored := saysNoRun(nil)
	restored.Workflows = reportWorkflows(fixed.Results)
	require.Len(t, restored.Workflows, 1)
	require.Equal(t, report.VerdictPass, restored.Workflows[0].Verdict,
		"the same workflow against a page that confirms the order did not pass, so the case "+
			"is permanently red and proves nothing; detail was %q", restored.Workflows[0].Detail)
	require.Equal(t, report.VerdictPass, restored.Verdict())
	require.Equal(t, "All 1 workflows passed.", restored.Headline())
	require.NoError(t, ciExit(restored))
	t.Logf("verbatim, restored: %s", restored.Headline())
	return true
}

// saysNoRunner locates node, the runner and a browser it can open.
//
// Three separate guards rather than one, because "the runner is not installed"
// and "the runner ran and could not open a browser" are different answers and
// the second one must never be reported as the first.
func saysNoRunner(t *testing.T) (string, bool) {
	t.Helper()
	refuse := func(reason string) bool {
		if os.Getenv("AF_REQUIRE_RUNNER") != "" {
			t.Fatalf("AF_REQUIRE_RUNNER is set and %s", reason)
		}
		t.Skipf("skipped: %s; set AF_REQUIRE_RUNNER to make this a failure", reason)
		return false
	}
	if _, err := exec.LookPath("node"); err != nil {
		return "", refuse("node is not on PATH")
	}
	main, err := filepath.Abs(filepath.Join("..", "..", "..", "runner", "src", "main.ts"))
	if err != nil {
		return "", refuse("the runner's entry point could not be resolved")
	}
	if _, err := os.Stat(main); err != nil {
		return "", refuse("the runner source is not in this tree")
	}
	modules, err := filepath.Abs(filepath.Join("..", "..", "..", "runner", "node_modules"))
	if err != nil {
		return "", refuse("runner/node_modules could not be resolved")
	}
	if _, err := os.Stat(modules); err != nil {
		return "", refuse("runner/node_modules is absent; run npm ci in runner")
	}
	// The browser itself, asked of playwright rather than guessed at from a
	// cache directory whose layout is playwright's business.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	probe := exec.CommandContext(ctx, "node", "-e",
		"process.stdout.write(require('playwright').chromium.executablePath())")
	probe.Dir = filepath.Dir(modules)
	out, err := probe.Output()
	if err != nil {
		return "", refuse("playwright could not name a chromium: " + err.Error())
	}
	if _, err := os.Stat(strings.TrimSpace(string(out))); err != nil {
		return "", refuse("chromium is not installed; run npx playwright install chromium")
	}
	return main, true
}

// saysNoDriveWorkflow serves one page and runs one workflow against it.
func saysNoDriveWorkflow(t *testing.T, runner, body string) env.TestReport {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!doctype html><title>Checkout</title><main><h1>Checkout</h1>"+
			"<p>%s</p></main>", body)
	}))
	defer srv.Close()

	artifacts := t.TempDir()
	job := map[string]any{
		"base_url":  srv.URL,
		"artifacts": artifacts,
		"headless":  true,
		"attempts":  1,
		"personas":  []any{},
		"workflows": []any{map[string]any{
			"name":        "pay_and_confirm",
			"description": "A customer pays for a basket and sees the order confirmed.",
			"expect":      []string{"the order is confirmed with a confirmation number"},
			"startPath":   "/",
		}},
	}
	doc, err := json.Marshal(job)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "--experimental-strip-types", runner)
	cmd.Stdin = strings.NewReader(string(doc))
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = filepath.Dir(filepath.Dir(filepath.Dir(runner)))
	// A non zero exit with a readable document is a result, which is what the
	// engine's own invocation says. Only silence is fatal.
	_ = cmd.Run()
	require.NotEmpty(t, stdout.String(),
		"the runner produced no document, which is its own failure and not the page's: %s",
		stderr.String())

	var out env.TestReport
	require.NoError(t, json.Unmarshal([]byte(stdout.String()), &out),
		"the runner's document could not be read: %s", stdout.String())
	return out
}

// ---------------------------------------------------------------------------
// 4. A migration that takes a blocking lock.

func proveBlockingLockIsNamed(t *testing.T) bool {
	if !saysNoHasDatabase(t) {
		return false
	}
	// BROKEN. Three seconds of ACCESS EXCLUSIVE, over the two second default,
	// sampled from a second connection while the statement runs.
	r := rehearseForGate(t, map[string]string{
		"001_slow_alter.sql": "ALTER TABLE orders ADD COLUMN refunded_at timestamp;\n" +
			"SELECT pg_sleep(3);\n",
	}, 2000, "saysnolock")
	require.False(t, r.Failed, r.Error)

	findings, migration := migrationFindings(insights.Full{Rehearsal: &r}, defaultGate())
	var lock *report.Finding
	for i := range findings {
		if findings[i].Rule == ruleMigrationLock && findings[i].Where == "orders" {
			lock = &findings[i]
		}
	}
	require.NotNil(t, lock,
		"three seconds of ACCESS EXCLUSIVE on orders produced no lock finding: %+v", findings)
	require.Equal(t, report.LevelFail, lock.Level,
		"three seconds is over the two second default and must fail the check")
	// And nothing else. The rehearsal writes a row to its own bookkeeping
	// table inside the migration's transaction, so that table, its primary key
	// and its sequence were each reported as a lock the change was told to
	// split, three findings about this product's own scaffolding on somebody
	// else's pull request.
	var locked []string
	for _, f := range findings {
		if f.Rule == ruleMigrationLock {
			locked = append(locked, f.Where)
		}
	}
	require.Equal(t, []string{"orders"}, locked,
		"the comment reports locks on relations the change never touched")

	run := saysNoRun(findings)
	run.Migration = migration
	require.Equal(t, report.VerdictFail, run.Verdict())
	comment := run.Comment()
	require.Contains(t, comment, "AccessExclusiveLock on orders",
		"the comment refuses without naming the lock and the table")
	require.Contains(t, comment, "| `orders` | AccessExclusiveLock |",
		"the measured hold time is missing from the migration table")
	require.Equal(t, aferrors.ExitTestFailure, exitCodeOfSilent(t, ciExit(run)))
	t.Logf("verbatim, the break:\n%s", saysNoHeadlineAndFindings(run))

	// RESTORED. The same statement without the hold, at the same thresholds.
	// The assertion is on the FAILING level rather than on the absence of any
	// lock finding: a machine running five other agents can hold a fast lock
	// past the half second warning, and that warning would be correct and
	// would be answering a different question.
	clean := rehearseForGate(t, map[string]string{
		"001_fast_alter.sql": "ALTER TABLE orders ADD COLUMN refunded_at timestamp;\n",
	}, 2000, "saysnolockclean")
	require.False(t, clean.Failed, clean.Error)
	cleanFindings, cleanMigration := migrationFindings(
		insights.Full{Rehearsal: &clean}, defaultGate())
	for _, f := range cleanFindings {
		require.NotEqual(t, report.LevelFail, f.Level,
			"an ordinary nullable column addition failed the check: %+v", f)
	}
	restored := saysNoRun(cleanFindings)
	restored.Migration = cleanMigration
	require.NotEqual(t, report.VerdictFail, restored.Verdict(),
		"an ordinary nullable column addition made the whole check fail, so the case is "+
			"permanently red and proves nothing")
	require.NoError(t, ciExit(restored))
	t.Logf("verbatim, restored: %s", restored.Headline())
	return true
}

// saysNoHasDatabase answers the same question saysNoDatabase does, for a case
// that gets its database from rehearseForGate.
func saysNoHasDatabase(t *testing.T) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, gateMaintenanceURL())
	if err != nil {
		if os.Getenv("AF_REQUIRE_DATABASE") != "" {
			t.Fatalf("AF_REQUIRE_DATABASE is set and there is no Postgres at %s: %v",
				redactGateURL(gateMaintenanceURL()), err)
		}
		t.Skipf("skipped: no Postgres at %s: %v", redactGateURL(gateMaintenanceURL()), err)
		return false
	}
	require.NoError(t, conn.Close(ctx))
	return true
}

// ---------------------------------------------------------------------------
// 5. A golden that fails verification.

// proveGoldenRefusalSaysWhy covers BOTH ways a scan refuses, because only one
// of them had ever been produced.
//
// A finding is a column the scan read and disliked. A skip is a column it could
// not read at all, which is the stricter of the two: the answer is "I do not
// know", and this package's rule is that not knowing fails rather than passes.
// The refusal for a skip was correct in the error and silent on the page: the
// only lines printed were the findings, of which there were none, above a
// sentence telling the reader to add a rule for each column above.
func proveGoldenRefusalSaysWhy(t *testing.T) bool {
	conn, url, done, ok := saysNoDatabase(t, "goldenverify")
	if !ok {
		return false
	}
	defer done()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	_, err := conn.Exec(ctx, saysNoCustomers)
	require.NoError(t, err)

	// BROKEN, the first way: real data still in the branch.
	leaked, err := verify.Scan(ctx, conn, verify.Options{})
	require.NoError(t, err)
	require.False(t, leaked.Clean())
	err = verifyFailure(leaked)
	require.Error(t, err, "a branch full of real addresses is publishable")
	require.Contains(t, err.Error(), "customers")
	require.Contains(t, err.Error(), "contact",
		"the refusal does not name the column that made it refuse")
	require.Contains(t, saysNoPrinted(t, leaked), "public.customers.contact",
		"the printed refusal names no column, so 'add a rule for each column above' "+
			"points at nothing")
	t.Logf("verbatim, the break:\n%s", saysNoPrinted(t, leaked))

	// Masked, so that the second break is the ONLY thing left wrong. A report
	// carrying a finding as well as a skip would be refused for the finding
	// and would say nothing about the skip.
	masked := saysNoMaskAndVerify(t, ctx, conn, masking.Rule{
		Column: "contact", Transform: "email", Why: "addresses are personal",
	})
	require.True(t, masked.Clean, "%v", masked.Findings)

	// BROKEN, the second way: a column nobody could read.
	unreadable := saysNoUnreadableColumnScan(t, ctx, conn, url)
	require.Empty(t, unreadable.Findings,
		"this case is about a report whose ONLY problem is an unreadable column")
	require.False(t, unreadable.Clean(),
		"a column the scan could not read counted as a column that passed")
	err = verifyFailure(unreadable)
	require.Error(t, err, "a golden with a column nobody could read is publishable")
	require.Contains(t, err.Error(), "secret_note",
		"the refusal does not name the column it could not read")
	printed := saysNoPrinted(t, unreadable)
	require.Contains(t, printed, "secret_note",
		"the printed refusal names nothing, so the reader is told to fix a list "+
			"that was never printed")
	require.Contains(t, printed, "could not be read",
		"the printed refusal does not say that the reason is an unreadable column")
	require.Contains(t, refusalAdvice(unreadable), "could not read",
		"the advice beside a skipped column still tells the reader to add a rule, which "+
			"no rule can fix")
	require.Contains(t, refusalAdvice(leaked), "Add a rule for each column above")
	t.Logf("verbatim, the break:\n%s", printed)

	// RESTORED. Read as the owner, which can see every column, on the same
	// masked database.
	rep, err := verify.Scan(ctx, conn, verify.Options{})
	require.NoError(t, err)
	require.NoError(t, verifyFailure(rep),
		"a masked and readable branch is still refused, so the case is permanently red")
	t.Logf("verbatim, restored: the scan reports %d columns clean across %d tables",
		rep.Columns, rep.Tables)
	return true
}

// saysNoUnreadableColumnScan scans as a role that cannot read one column.
//
// A column level grant rather than a dropped table, because this is how it
// happens: the golden is refreshed by one role and verified by another, and the
// second one was never granted the column. information_schema lists a column
// the role holds ANY privilege on, so the scan finds it, asks for it, and is
// refused by the database.
func saysNoUnreadableColumnScan(
	t *testing.T, ctx context.Context, conn *pgx.Conn, url secrets.Value,
) verify.Report {
	t.Helper()
	const role = "af_saysno_verifier"
	for _, stmt := range []string{
		"ALTER TABLE customers ADD COLUMN secret_note text",
		"UPDATE customers SET secret_note = 'ward 4'",
		"DROP ROLE IF EXISTS " + role,
		"CREATE ROLE " + role + " LOGIN PASSWORD 'saysno'",
		"GRANT CONNECT ON DATABASE " + saysNoDatabaseName(t, ctx, conn) + " TO " + role,
		"GRANT USAGE ON SCHEMA public TO " + role,
		"GRANT SELECT (id, contact, region) ON customers TO " + role,
		"GRANT INSERT (secret_note) ON customers TO " + role,
	} {
		_, err := conn.Exec(ctx, stmt)
		require.NoError(t, err, stmt)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = conn.Exec(c, "REVOKE ALL ON customers FROM "+role)
		_, _ = conn.Exec(c, "REVOKE ALL ON SCHEMA public FROM "+role)
		_, _ = conn.Exec(c, "DROP OWNED BY "+role)
		_, _ = conn.Exec(c, "DROP ROLE IF EXISTS "+role)
	})

	as, err := pgx.Connect(ctx, saysNoURLAs(url.Reveal(), role, "saysno"))
	require.NoError(t, err)
	defer func() { _ = as.Close(context.WithoutCancel(ctx)) }()

	rep, err := verify.Scan(ctx, as, verify.Options{})
	require.NoError(t, err, "the scan itself failed, which is a different answer from a skip")
	return rep
}

func saysNoDatabaseName(t *testing.T, ctx context.Context, conn *pgx.Conn) string {
	t.Helper()
	var name string
	require.NoError(t, conn.QueryRow(ctx, "SELECT current_database()").Scan(&name))
	return name
}

// saysNoURLAs rewrites the credentials of a connection string.
func saysNoURLAs(url, user, password string) string {
	scheme := strings.Index(url, "://")
	at := strings.LastIndex(url, "@")
	if scheme < 0 || at < 0 {
		return url
	}
	return url[:scheme+3] + user + ":" + password + url[at:]
}

// saysNoPrinted renders the refusal the way a terminal shows it.
func saysNoPrinted(t *testing.T, rep verify.Report) string {
	t.Helper()
	var out strings.Builder
	printVerifyRefusal(&Env{Out: NewOutput(&out, &out)}, rep)
	return out.String()
}

// ---------------------------------------------------------------------------
// 6. A twin missing a declared datastore.

func proveMissingDatastoreDropsTheScore(t *testing.T) bool {
	// RESTORED FIRST, because this break lives in the manifest rather than in
	// the environment: the store the twin is missing is one nobody declared
	// until the second inventory, and the pair is what shows the number moves.
	clean := fidelity.Build(saysNoObservation(nil))
	before, ok := clean.Score().Percent()
	require.True(t, ok, "nothing in the base observation could be measured")
	t.Logf("base observation: %s", clean.Headline())
	require.Equal(t, 100, before,
		"the base observation is meant to be a twin with nothing missing in it")

	// BROKEN. The manifest asks for a masked, verified copy of production in a
	// second store, and this build has one golden and it is the Postgres.
	broken := fidelity.Build(saysNoObservation([]schema.Datastore{{
		Name: "events", Engine: "clickhouse", Stance: schema.StanceGolden,
	}}))
	after, ok := broken.Score().Percent()
	require.True(t, ok)
	require.Less(t, after, before,
		"a twin holding masked Postgres metadata and zero events scored the same as one "+
			"with nothing missing, which is the verdict that cannot say no")

	text := broken.Explain()
	require.Contains(t, text, "events", "the report drops the score without naming the store")
	require.Contains(t, text, string(fidelity.Absent),
		"the missing store is not reported absent")
	require.Contains(t, text, fmt.Sprintf("which is %d percent", after))
	t.Logf("verbatim, the break: %s", broken.Headline())
	t.Logf("verbatim, restored: %s", clean.Headline())
	return true
}

// saysNoObservation is a twin whose every measured component is production's
// own, so that the only thing moving the number is the declared store.
func saysNoObservation(stores []schema.Datastore) fidelity.Observation {
	return fidelity.Observation{
		EnvID: "shop-pr-1",
		Manifest: &schema.Manifest{
			Name:       "shop",
			Services:   []schema.Service{{Name: "web", Kind: schema.ServiceWeb}},
			Database:   &schema.Database{Provider: schema.DBDocker},
			Personas:   []schema.Persona{{Name: "buyer", Email: "buyer@example.test"}},
			Datastores: stores,
		},
		Running: []provider.RunningService{
			{Name: "web", Kind: "web", Ready: true, URL: "http://127.0.0.1:8080"},
		},
		Runtime:     "the local runtime, which publishes an address this machine can reach",
		Golden:      "gv_20260907120000_abcd1234",
		Attested:    true,
		Attestation: "41 columns read back over 82000 rows sampled",
		Tables:      12,
		Rows:        184000,
		Personas: []fidelity.Persona{
			{Name: "buyer", Login: schema.LoginPassword, Present: true, Table: "public.users"},
		},
	}
}

// saysNoHeadlineAndFindings is the top of the comment, which is the part a
// person reads in the thirty seconds they give it.
func saysNoHeadlineAndFindings(run report.Run) string {
	body := run.Markdown()
	if i := strings.Index(body, "<details>"); i > 0 {
		return strings.TrimSpace(body[:i])
	}
	return strings.TrimSpace(body)
}
