package conformance_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/internal/testutil/fakes"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// A conformance suite nobody has watched fail is not evidence. It is a list of
// assertions that might all be vacuous, and the usual ways an assertion goes
// vacuous are undramatic: a helper starts skipping, a comparison compares a
// value against itself, a behaviour asserts on state an earlier behaviour
// already established. Every one of those still prints ok.
//
// So this points the suite at a provider that violates exactly one guarantee
// and requires the suite to go RED, in the named behaviour. If it stays green,
// that behaviour was not checking what it claims, and the green run could never
// have told anybody.
//
// It needs a SUBPROCESS. A suite proving it can fail has to actually fail, and
// a failure inside the process asserting on it fails that process too. The
// child below is skipped unless it is the child; the parent re-executes the
// test binary once per fault.
//
// It needs TWO PROVIDERS, and that is the part that took a second pass.
// Nineteen behaviours can be broken with no infrastructure at all, in
// fakes.InMemoryDatabase, and that matters because a negative control which
// needs infrastructure gets skipped, and a skipped negative control is a false
// green rather than a proof. The other five are about isolation, reset, and
// what a branch actually holds. Those are claims about bytes, a fake with no
// bytes can only agree with whatever it was told, and agreeing is the same
// thing as not checking. They run against fakes.NewPostgresDatabase, on the
// same server every other database suite in this repository uses, and CI sets
// AF_REQUIRE_DATABASE so that its absence is a failure rather than a skip.
const (
	childEnv      = "AF_DB_SELFTEST_CHILD"
	faultEnv      = "AF_DB_SELFTEST_FAULT"
	backendEnv    = "AF_DB_SELFTEST_BACKEND"
	prefixEnv     = "AF_DB_SELFTEST_PREFIX"
	urlEnv        = "AF_DB_SELFTEST_URL"
	unverifiedEnv = "AF_DB_SELFTEST_PUBLISH_UNVERIFIED"
)

// The two providers, named so a table can say which one proved a row.
const (
	inMemory = "memory"
	onPG     = "postgres"
)

// notInjected is printed by the child when Break could not put the fault into
// the provider it was given.
//
// Without it the child fails, the parent sees a red, and the red is recorded
// as the suite catching a fault that was never present. That is the exact
// shape of the false proof this file exists to prevent, so it is checked for
// by name rather than left to the failure text.
const notInjected = "THE FAULT WAS NOT INJECTED"

// defaultPostgresURL is the scratch server `just db` starts and the one CI
// starts, on 55432, which is where every other Postgres suite in this
// repository looks.
const defaultPostgresURL = "postgres://postgres:test@127.0.0.1:55432/antifailure"

func postgresURL() string {
	if u := os.Getenv("AF_TEST_DATABASE_URL"); u != "" {
		return u
	}
	return defaultPostgresURL
}

// TestDatabaseSuiteChild is the suite under examination. It runs only when the
// parent re-executes this binary, so an ordinary `go test ./...` does not see
// it fail on purpose.
func TestDatabaseSuiteChild(t *testing.T) {
	if os.Getenv(childEnv) != "1" {
		t.Skip("runs only as the child of the self test")
	}

	fault := fakes.Fault(os.Getenv(faultEnv))
	backend := os.Getenv(backendEnv)
	publishUnverified := os.Getenv(unverifiedEnv) == "1"

	factory := func(t *testing.T) provider.Database {
		var p provider.Database
		switch backend {
		case inMemory:
			m := fakes.NewInMemoryDatabase()
			if publishUnverified {
				m.PublishUnverified()
			}
			p = m
		case onPG:
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			pg, err := fakes.NewPostgresDatabase(ctx, fakes.PostgresOptions{
				AdminURL:          os.Getenv(urlEnv),
				Prefix:            os.Getenv(prefixEnv),
				SeedSQL:           conformance.DefaultSeedSQL,
				PublishUnverified: publishUnverified,
			})
			if err != nil {
				t.Fatalf("build the Postgres backed provider: %v", err)
			}
			p = pg
		default:
			t.Fatalf("no backend named %q", backend)
		}
		if fault == "" {
			return p
		}
		p = fakes.Break(p, fault)
		if u, ok := p.(fakes.Uninjectable); ok {
			t.Fatalf("%s: the provider %q cannot host the fault %q", notInjected, u.Provider, u.Fault)
		}
		return p
	}

	conformance.RunDatabase(t, factory, conformance.Options{Timeout: 3 * time.Minute})
}

// child describes one re-execution.
type child struct {
	backend string
	// behavior is the single subtest to run. Empty runs the whole suite.
	behavior string
	fault    fakes.Fault
	// publishUnverified turns on the affordance that makes an unverified
	// version obtainable. It is not a fault; see the control below.
	publishUnverified bool
}

// runChild executes one behaviour in a subprocess and reports whether it
// passed, along with its output for the failure message.
func runChild(t *testing.T, c child) (bool, string) {
	t.Helper()

	pattern := "TestDatabaseSuiteChild"
	if c.behavior != "" {
		pattern += "/^" + c.behavior + "$"
	}
	cmd := exec.Command(os.Args[0], "-test.run", pattern, "-test.v", "-test.timeout", "20m")
	cmd.Env = append(os.Environ(),
		childEnv+"=1",
		faultEnv+"="+string(c.fault),
		backendEnv+"="+c.backend,
	)
	if c.publishUnverified {
		cmd.Env = append(cmd.Env, unverifiedEnv+"=1")
	}
	if c.backend == onPG {
		cmd.Env = append(cmd.Env, urlEnv+"="+postgresURL(), prefixEnv+"="+newPostgresPrefix(t))
	}
	out, err := cmd.CombinedOutput()
	text := string(out)
	if strings.Contains(text, notInjected) {
		t.Fatalf("the child could not have the fault put into it, so its result says nothing "+
			"about the suite.\n%s", text)
	}
	return err == nil, text
}

// postgresReachable answers once, in the parent, whether the five behaviours
// that read rows can be proved on this machine.
//
// Asked here rather than inside each child because the answer changes what the
// table at the end is allowed to claim. A skip is the false green this whole
// file is against, so it is never silent: on a developer's machine with no
// server the rows that need one say so by name, and CI sets AF_REQUIRE_DATABASE
// so that its absence fails the job instead.
func postgresReachable(t *testing.T) bool {
	t.Helper()

	prefix, err := fakes.NewPrefix()
	if err != nil {
		t.Fatalf("build a database prefix: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	p, err := fakes.NewPostgresDatabase(ctx, fakes.PostgresOptions{
		AdminURL: postgresURL(), Prefix: prefix,
	})
	if err != nil {
		if os.Getenv("AF_REQUIRE_DATABASE") != "" {
			t.Fatalf("AF_REQUIRE_DATABASE is set and there is no usable Postgres at %s: %v",
				postgresURL(), err)
		}
		t.Logf("no Postgres at %s, so the behaviours that read rows cannot be proved here: %v",
			postgresURL(), err)
		return false
	}
	_ = p.Close()
	return true
}

// newPostgresPrefix returns a database name prefix nothing else will use, and
// sweeps it when the test finishes.
func newPostgresPrefix(t *testing.T) string {
	t.Helper()
	prefix, err := fakes.NewPrefix()
	if err != nil {
		t.Fatalf("build a database prefix: %v", err)
	}
	t.Cleanup(func() {
		// The child is a separate process and a behaviour that failed on
		// purpose legitimately leaves its databases behind, so nothing else
		// can sweep. The cluster is shared with every other branch's work.
		c, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := fakes.DropEverything(c, postgresURL(), prefix); err != nil {
			t.Errorf("the self test left databases named %s_* behind: %v", prefix, err)
		}
	})
	return prefix
}

// backendFor picks the cheapest provider that can prove a behaviour.
func backendFor(behavior string) string {
	if fakes.NeedsRows()[behavior] {
		return onPG
	}
	return inMemory
}

// The positive control, and it is the half almost nobody writes. Without it a
// suite can "pass" by skipping itself, and every negative result below would
// then be meaningless: everything fails, including the correct provider.
func TestTheSuitePassesAgainstAProviderThatKeepsItsGuarantees(t *testing.T) {
	for _, b := range conformance.Behaviors() {
		if fakes.NeedsRows()[b.Name] {
			continue // proved against Postgres, in the test below
		}
		t.Run(b.Name, func(t *testing.T) {
			passed, out := runChild(t, child{backend: inMemory, behavior: b.Name})
			requireRan(t, b.Name, passed, out)
		})
	}
}

// The same control for the provider with storage, run as one child over the
// whole suite rather than one child per behaviour.
//
// It is the only place all twenty four run together, which is what proves the
// Postgres fake is a correct provider rather than one that happens to satisfy
// the five behaviours pointed at it.
func TestThePostgresBackedFakeKeepsEveryGuarantee(t *testing.T) {
	if !postgresReachable(t) {
		t.Skipf("skipped: the whole suite needs a Postgres at %s", postgresURL())
	}
	passed, out := runChild(t, child{backend: onPG})
	for _, b := range conformance.Behaviors() {
		requireRan(t, b.Name, passed, out)
	}
}

// requireRan is the assertion a self test needs and a `go test` exit code does
// not give: a run that matched no test, or matched one and skipped it, exits
// zero and reads exactly like a pass.
func requireRan(t *testing.T, behavior string, passed bool, out string) {
	t.Helper()
	if !passed {
		t.Fatalf("%s must pass against a correct provider, or a red result for it proves nothing.\n%s",
			behavior, out)
	}
	if strings.Contains(out, "--- SKIP: TestDatabaseSuiteChild/"+behavior) {
		t.Fatalf("%s SKIPPED rather than ran. A suite that passes by skipping is a false green.\n%s",
			behavior, out)
	}
	if !strings.Contains(out, "--- PASS: TestDatabaseSuiteChild/"+behavior) {
		t.Fatalf("%s never ran. A -test.run that matches nothing exits zero and reads "+
			"exactly like a pass, which is the whole failure this line is here for.\n%s",
			behavior, out)
	}
}

// knownGaps are faults the suite does NOT catch, with the reason.
//
// They are recorded rather than removed, and an entry that starts being caught
// fails this file, for the same reason a stale vulnerability suppression does:
// an exemption describing something that is no longer true reads as protection
// that is not there.
//
// It is empty, and it has been empty since the two entries it was created with
// were closed. Health_ReportsADestroyedBranch accepted an error while its
// catalogue entry said "unreachable rather than erroring", and the catalogue
// was right. Branch_RefusesAnUnverifiedGolden could not reach its branch side
// rule, because a provider that correctly refuses to PUBLISH an unverified
// version never produces one for the suite to hand to Branch; the note said
// closing it needed a way to obtain an unverified version. That way is
// PublishUnverified on the fakes, which is an affordance rather than a fault
// and has its own control below proving it breaks nothing on its own.
//
// The map stays because the next gap needs somewhere to be recorded, and
// recording it is the difference between a known hole and a forgotten one.
var knownGaps = map[fakes.Fault]string{}

// unfalsifiableAssertions record what a green run does NOT prove, at the level
// of the individual assertion rather than the behaviour.
//
// Every behaviour is falsifiable, which is what this file measures. That is a
// weaker statement than every ASSERTION inside every behaviour being
// falsifiable, and the difference is worth writing down rather than leaving
// somebody to assume the stronger one.
var unfalsifiableAssertions = map[string]string{
	"ConnString_IsASecret": "" +
		"The behaviour is falsifiable, by ConnStringIsEmpty. The assertion it is " +
		"NAMED for is not: a provider hands back a secret.Value, whose String, " +
		"GoString and Format all return the redaction marker and whose only field " +
		"is unexported, so no value of that type can render its plaintext. The " +
		"guarantee is enforced by the compiler rather than by the suite. That is " +
		"vacuous in the good way, and it is still a different claim from 'the " +
		"suite would catch it breaking', which here it would not.",
}

// The negative controls, and the table that is this file's whole output.
//
// Twenty four behaviours, and for each one the fault that turns it red. A
// behaviour with no fault is not a behaviour anybody has watched fail, and the
// completeness assertion at the end is what stops one being added without one.
func TestEveryBehaviorIsProvedAbleToFail(t *testing.T) {
	catches := fakes.Catches()
	pgOK := postgresReachable(t)

	type row struct {
		behavior string
		fault    fakes.Fault
		backend  string
		subtest  string
		proved   bool
		note     string
	}
	var rows []row

	for _, f := range fakes.Faults() {
		behavior := catches[f]
		backend := backendFor(behavior)
		t.Run(string(f), func(t *testing.T) {
			if backend == onPG && !pgOK {
				rows = append(rows, row{behavior, f, backend, "", false,
					"NOT PROVED HERE: no Postgres at " + postgresURL()})
				t.Skipf("skipped: %s reads rows, which needs a Postgres at %s",
					behavior, postgresURL())
			}
			c := child{backend: backend, behavior: behavior, fault: f}
			// The one fault whose premise is a version that failed
			// verification and was published anyway. Without the affordance
			// there is nothing for Branch to accept and the fault has no
			// subject; the control below proves the affordance alone leaves
			// the suite green, so a red here is the fault's.
			if f == fakes.BranchAcceptsUnverified {
				c.publishUnverified = true
			}
			passed, out := runChild(t, c)

			if reason, known := knownGaps[f]; known {
				if !passed {
					t.Fatalf("%s is recorded as a gap the suite does not catch, and it "+
						"caught it. Remove the entry: a recorded gap that is no longer "+
						"real describes nothing.\nThe recorded reason was: %s", f, reason)
				}
				rows = append(rows, row{behavior, f, backend, "", false, "recorded gap: " + reason})
				t.Skipf("known gap, recorded rather than hidden: %s", reason)
			}

			if passed {
				t.Fatalf("the suite PASSED against a provider that %s.\n"+
					"%s is therefore not checking what it claims, and no green run could ever say so.\n%s",
					f, behavior, out)
			}
			// It has to fail in the named behaviour rather than anywhere at
			// all. A child that failed to build, or failed in setup, would
			// otherwise count as the suite catching the fault.
			marker := "--- FAIL: TestDatabaseSuiteChild/" + behavior
			if !strings.Contains(out, marker) {
				t.Fatalf("the child failed, but not in %s, so this proves nothing about that behaviour.\n%s",
					behavior, out)
			}
			rows = append(rows, row{behavior, f, backend, marker, true, firstAssertion(out, behavior)})
		})
	}

	// The table, and then the count. The count is the claim; the table is what
	// makes it checkable by somebody who does not trust the count.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].behavior != rows[j].behavior {
			return rows[i].behavior < rows[j].behavior
		}
		return rows[i].fault < rows[j].fault
	})
	proved := map[string]bool{}
	var b strings.Builder
	fmt.Fprintf(&b, "\nEvery row is a subtest of TestDatabaseSuiteChild that went red under the break beside it.\n")
	fmt.Fprintf(&b, "%-46s  %-46s  %-8s  %s\n", "BEHAVIOR", "THE BREAK THAT PROVES IT", "BACKEND", "WHAT WENT RED")
	for _, r := range rows {
		if r.proved {
			proved[r.behavior] = true
		}
		fmt.Fprintf(&b, "%-46s  %-46s  %-8s  %s\n", r.behavior, r.fault, r.backend, r.note)
	}
	all := conformance.Behaviors()
	fmt.Fprintf(&b, "\n%d of %d behaviors proved able to fail.\n", len(proved), len(all))
	for name, why := range unfalsifiableAssertions {
		fmt.Fprintf(&b, "\nNot proved, at the assertion level, in %s:\n  %s\n", name, why)
	}
	t.Log(b.String())

	if len(rows) < len(fakes.Faults()) {
		// A -test.run naming one fault is how somebody debugs a red row, and
		// a completeness claim made from one row would fail every such run
		// for a reason that has nothing to do with what they are looking at.
		// The claim is not weakened: an unfiltered run is what CI does, and
		// fakes.TestEveryConformanceBehaviorHasAFault checks the same
		// completeness structurally, without running anything.
		t.Logf("a filtered run: %d of %d faults ran, so the completeness claim is not made here",
			len(rows), len(fakes.Faults()))
		return
	}

	var missing, needServer []string
	for _, beh := range all {
		if proved[beh.Name] {
			continue
		}
		missing = append(missing, beh.Name)
		if fakes.NeedsRows()[beh.Name] {
			needServer = append(needServer, beh.Name)
		}
	}
	if len(missing) == 0 {
		return
	}
	if !pgOK && len(needServer) == len(missing) {
		// Said out loud rather than counted as proved. CI sets
		// AF_REQUIRE_DATABASE, which turns the missing server into a failure
		// before this line, so this is a developer's machine and not the gate.
		t.Logf("%d of %d proved on this machine. The other %d read rows and there is no "+
			"Postgres at %s: %s", len(all)-len(missing), len(all), len(missing),
			postgresURL(), strings.Join(missing, ", "))
		return
	}
	t.Fatalf("%d of %d behaviors have never been watched fail: %s.\n"+
		"A behaviour with no break is a row in a list, not a check. Add a fault in "+
		"internal/testutil/fakes that violates exactly the property it names.",
		len(missing), len(all), strings.Join(missing, ", "))
}

// firstAssertion pulls the line the child failed on, so the table says what
// went red rather than only that something did.
func firstAssertion(out, behavior string) string {
	lines := strings.Split(out, "\n")
	started := false
	for _, l := range lines {
		if strings.Contains(l, "=== RUN   TestDatabaseSuiteChild/"+behavior) {
			started = true
			continue
		}
		if !started {
			continue
		}
		trimmed := strings.TrimSpace(l)
		if i := strings.Index(trimmed, ".go:"); i > 0 {
			rest := strings.TrimSpace(trimmed[strings.Index(trimmed, ": ")+1:])
			if len(rest) > 96 {
				rest = rest[:96]
			}
			return strings.TrimSpace(rest)
		}
		if strings.HasPrefix(trimmed, "--- FAIL") {
			break
		}
	}
	return "failed in " + behavior
}

// The control for the affordance that closed the last known gap.
//
// PublishUnverified makes a provider record a version whose verification
// failed rather than withholding it, which the suite explicitly permits:
// Refresh_RefusesToPublishWhenVerificationFails asserts on the Verified flag,
// not on the absence of a version. If that were not true, the red under
// BranchAcceptsUnverified would be attributable to the affordance rather than
// to the fault, and the gap would be closed only on paper.
func TestTheUnverifiedAffordanceBreaksNothingOnItsOwn(t *testing.T) {
	for _, behavior := range []string{
		"Refresh_RefusesToPublishWhenVerificationFails",
		"Branch_RefusesAnUnverifiedGolden",
		"Refresh_ProducesAVerifiedGolden",
	} {
		t.Run(behavior, func(t *testing.T) {
			passed, out := runChild(t, child{
				backend: inMemory, behavior: behavior, publishUnverified: true,
			})
			requireRan(t, behavior, passed, out)
		})
	}
}

// Every behaviour that needs rows is named, and every behaviour that does not
// is proved without a server.
//
// The split is a claim about the suite, so it is checked rather than trusted:
// a behaviour wrongly listed as needing rows would silently stop being proved
// on a laptop, and one wrongly left out would fail there for a reason that
// looks like a bug in the provider.
func TestTheBehaviorsThatNeedARealDatabaseAreNamed(t *testing.T) {
	known := map[string]bool{}
	for _, b := range conformance.Behaviors() {
		known[b.Name] = true
	}
	var needs []string
	for name := range fakes.NeedsRows() {
		if !known[name] {
			t.Errorf("NeedsRows names %q, which the suite does not have", name)
		}
		needs = append(needs, name)
	}
	sort.Strings(needs)
	t.Logf("%d of %d behaviors are proved able to fail with no server at all; %d need Postgres: %s",
		len(known)-len(needs), len(known), len(needs), strings.Join(needs, ", "))
}
