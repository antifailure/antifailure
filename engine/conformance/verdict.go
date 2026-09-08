package conformance

// The suite had two answers, and one of the claims it checks can honestly be
// given neither of them.
//
// CopyOnWrite_BranchTimeMatchesTheDeclaration decides by stopwatch: a branch of
// a large golden that is no slower than a branch of a small one is shared
// storage, and one that is slower is a copy. That reading is only as good as
// the storage underneath the run. Over a single local Postgres the only way a
// fake control plane can hand back a branch carrying the golden's data is
// CREATE DATABASE ... TEMPLATE, which copies files, so the stopwatch reports a
// copy no matter what the provider would do against the real service. A
// provider that really does clone in constant time, declaring so truthfully,
// FAILS on that harness.
//
// Three of the four ways out are worse than the problem.
//
// Declare CopyOnWrite false. Green, and false about the product.
//
// Skip the behaviour in the provider's own conformance test. That turns off the
// one instrument that can refuse this category's central commercial claim, on
// exactly the runs where somebody wanted a green.
//
// Loosen the allowance until a copy fits inside it. That leaves the behaviour
// in the output, running, printing a verdict, and unable to refuse anything,
// which is the defect this repository keeps finding in its own instruments.
//
// The fourth is what the rest of the repository already does. tools/sitesmoke
// prints COULD NOT TELL and says in its own output that this is not a pass.
// Mutation cells are classified on the "=== RUN" count, so a break that stopped
// the package compiling is reported as the instrument being unable to look
// rather than as a catch. The egress report is closed, open or unproven, and
// unproven is never counted as closed. Verdicts here are three valued too.
//
// WHAT UNPROVEN IS NOT.
//
// It is not a pass. Answer.Publishable is false for it, RunDatabase prints a
// block at the end of the run naming every behaviour that reached it, and
// CopyOnWriteClaim renders the provider's declaration as the word "unproven"
// rather than as the value the provider declared.
//
// It is not something a provider can ask for. Nothing on provider.Caps reaches
// it and no method of provider.Database is consulted. The only input is
// Options.RealService, which is the FIXTURE saying what its stopwatch is
// pointed at.
//
// And it is not a flag that admits simulation, which is the shape this was
// first written in and the shape that was wrong.
//
// THE DEFAULT IS UNPROVEN, AND THAT INVERSION IS THE WHOLE DESIGN.
//
// A field that a fake sets to excuse itself is a field a fake can simply never
// set. The next lane writes a control plane, never learns the declaration
// exists because nothing forced it to say anything, and collects a measured
// verdict again, which rebuilds the silent pass one level up inside the
// mechanism written to prevent it. So the declaration is an assertion of
// REALITY rather than an admission of simulation. Forgetting it produces the
// safe answer, and the only way to a decided verdict is to actively say what
// the run drives.
//
// It also puts the burden where somebody is present to carry it. A credentialed
// run against a real service has a person in the loop who can say what it is
// pointed at. An unattended fake has nobody. Requiring the assertion from the
// attended side is the only arrangement in which forgetting is safe.
//
// IT IS SYMMETRIC, AND THE SIDE THAT LOOKS SAFE IS THE DANGEROUS ONE.
//
// The first version of this raised the third verdict only when the measurement
// FAILED, which reads as the cautious choice and is not. The assertion is two
// sided: false requires the branch time to GROW with the data, and against a
// harness that copies every branch that holds comfortably. So every snapshot
// restore provider in the wave, every one that honestly declares CopyOnWrite
// false, would collect a GREEN copy on write gate for a reason that has nothing
// to do with the service it ships against, the moment it reused the wave's
// prescribed fake. And nobody would ever look at it again, because green checks
// do not get read.
//
// So a run that asserts nothing is unproven in BOTH directions, whatever the
// measurement would have said. A passing measurement on a simulator is not
// reportable as a pass.
//
// THE MEASUREMENT IS NOT TAKEN, AND THAT IS DELIBERATE.
//
// The verdict is decided before the behaviour runs, so building two goldens and
// timing six branches could not change it. Half a gibibyte of transient
// databases per fake backed run, for a number that cannot move the answer, is
// cost with no evidence in it.
//
// There is a second and better reason. Publishing the timings a simulator
// produced invites exactly the reading the ruling forbids: somebody quotes "the
// fake branched flat" as though it were about the product. Withholding the
// number is the stronger position, and it is the one sitesmoke takes when it
// says COULD NOT TELL rather than describing what it half saw.
//
// WHAT SEPARATES IT FROM A SKIP, since mechanically this does not run either.
//
// The suite already skips a behaviour a provider cannot support, by name, with
// a stated reason, and that line is what a reviewer reads. A skip and an
// unproven are different claims and must not share a word. A skip says this
// provider makes no such claim, so there was nothing to check. An unproven says
// the provider DOES make the claim and this run could not reach it. The first
// is a fact about the provider and the second is a fact about the run, and a
// reader who confuses them reads an unmeasured commercial claim as a supported
// one.
//
// So the two are separate paths. The word "skipped" is never used for this, the
// verdict is collected and reprinted at the end of the run where a reader of
// the last few lines finds it, and it travels out of the test binary: the
// ledger records it per provider and CopyOnWriteClaim renders it, so the cell
// in the wave's published table reads unproven rather than blank. A blank cell
// is taken for a pass by every reader in a hurry, and this whole verdict exists
// because green and blank do not get read.

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
)

// Answer is the verdict a behaviour reached, and there are three of them.
//
// Proved and Refuted are the two the suite has always had, spelled with a
// passing subtest and with t.Fatalf. They are named here so that the third one
// is a member of a set rather than a special case bolted onto a boolean, which
// is the difference between a three valued verdict and a pass with an excuse.
type Answer string

const (
	// Proved means the behaviour ran and the declaration survived it.
	Proved Answer = "PROVED"
	// Refuted means the behaviour ran and the declaration did not survive it.
	Refuted Answer = "REFUTED"
	// Unproven means the behaviour ran, in full, and the harness underneath it
	// structurally cannot exhibit the property either way, so both of the
	// other answers would be a statement the readings do not support.
	Unproven Answer = "UNPROVEN"
)

// Publishable reports whether a customer facing surface may print the
// declaration this answer was checking as the value the provider declared.
//
// Only Proved. Refuted is a declaration the suite refused and Unproven is one
// it could not reach, and the two of them are different from each other and
// identical in this one respect: neither is evidence for the sentence a buyer
// would read.
func (a Answer) Publishable() bool { return a == Proved }

// Finding is one behaviour's answer together with the reason for it.
type Finding struct {
	// Provider is the provider the behaviour ran against.
	Provider string
	// Behavior is the subtest name.
	Behavior string
	// Answer is the verdict.
	Answer Answer
	// Because is why, in prose, and it is printed rather than stored. An
	// unproven verdict whose reason nobody can read is a skip wearing a
	// longer word.
	Because string
}

// findings collects what one run of the suite settled and what it did not.
//
// Behaviours run under subtests that may run in parallel, so this is written
// from several goroutines and read once at the end.
type findings struct {
	mu   sync.Mutex
	rows []Finding
}

func newFindings() *findings { return &findings{} }

func (f *findings) add(row Finding) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, row)
}

// unproven returns the findings that reached the third answer, sorted.
func (f *findings) unproven() []Finding {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Finding, 0, len(f.rows))
	for _, r := range f.rows {
		if r.Answer == Unproven {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Behavior < out[j].Behavior })
	return out
}

// reportUnproven writes the block a reader has to see, and reports whether it
// wrote anything.
//
// Written to a plain io.Writer, and RunDatabase passes it os.Stderr rather than
// t.Logf alone, because t.Logf on a subtest that did not fail is invisible
// without -v. An unproven verdict that only appears under a flag is a skip that
// reads as a pass, which is the exact failure this file exists to avoid, and
// the whole point of the third answer is that somebody sees it.
func (f *findings) reportUnproven(w io.Writer) bool {
	rows := f.unproven()
	if len(rows) == 0 {
		return false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nNOT PROVED BY THIS RUN. This is not a pass.\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "  %-9s %s  %s\n", string(r.Answer), r.Provider, r.Behavior)
		for _, line := range strings.Split(strings.TrimSpace(r.Because), "\n") {
			fmt.Fprintf(&b, "            %s\n", strings.TrimSpace(line))
		}
	}
	fmt.Fprintf(&b, "            The declaration stays as the provider wrote it and stays\n"+
		"            unpublishable: a surface printing this provider's copy on write\n"+
		"            value prints %q, never the declared value. conformance.CopyOnWriteClaim\n"+
		"            is the only rendering of it a customer facing page may use.\n\n",
		notProved)
	_, _ = io.WriteString(w, b.String())
	return true
}

// notProved is the word every surface prints in place of a declaration the
// suite did not reach.
const notProved = "unproven"

// CopyOnWriteClaim renders a provider's copy on write declaration for
// publication, and it is the only shape a customer facing surface may print it
// in.
//
// The second condition of the ruling that produced the third answer is that an
// unproven CopyOnWrite: true is not publishable as a proved claim anywhere a
// customer reads it. A condition of that shape is kept by a function every
// emission site calls, not by a sentence in a plan: the wave publishes one
// shared table of branch time per provider, a buyer chooses a vendor from it,
// and a cell that says true because somebody typed true is the same defect as a
// capability nothing can refuse, one surface further out.
//
// So the value and the verdict are rendered together and cannot be separated by
// the caller. There is no argument order that gets "true" out of an unproven
// run.
func CopyOnWriteClaim(declared bool, a Answer) string {
	switch a {
	case Unproven:
		return notProved
	case Refuted:
		return "refuted"
	case Proved:
		if declared {
			return "true"
		}
		return "false"
	default:
		// An answer this function does not know is not a pass, for the reason
		// sitesmoke gives about a verdict word it cannot read. The zero value
		// of Answer lands here, so a Finding somebody forgot to fill in
		// publishes as unproven rather than as the declared value.
		return notProved
	}
}

// serviceOwnedBehaviors names the behaviours whose verdict belongs to the real
// service rather than to the provider's code, and says why for each.
//
// The shape is taken from L-other-reds, which reached the same mechanism
// independently and put the reason on the BEHAVIOUR rather than on the
// provider. That is better than the alternative: the answer to "which
// behaviours does a simulator invalidate" is one list somebody can read,
// instead of a condition spread across five provider packages where the sixth
// one copies a working file and inherits the wrong answer.
//
// One entry today. A second is a candidate and is deliberately NOT here.
// Branch_IsWithinTheDeclaredLatency times a branch against a declared wall
// clock number, and against an httptest server that number is a measurement of
// an httptest server, which is the same defect for the same reason. It is left
// out because adding it would change the verdict for lanes that have not been
// told, and a gate widened at midnight without telling anybody is its own
// failure. It is reported rather than taken.
var serviceOwnedBehaviors = map[string]string{
	"CopyOnWrite_BranchTimeMatchesTheDeclaration": "copy on write is a claim about " +
		"what the vendor's storage does, and this behaviour decides it with a stopwatch. " +
		"Over a simulator the stopwatch measures the simulator: a fake control plane over " +
		"one local Postgres can only hand back a branch carrying the golden's data with " +
		"CREATE DATABASE ... TEMPLATE, which copies files, so a truthful CopyOnWrite true " +
		"is refused and a CopyOnWrite false passes comfortably. Both answers are about the " +
		"harness and neither is about the product",
}

// unprovenReason reports why a behaviour cannot be decided by this run, or the
// empty string when it can.
//
// The condition is the ABSENCE of Options.RealService, never the presence of a
// declaration of simulation, and never anything read from the provider. It is
// checked for every service owned behaviour before the behaviour runs, so the
// answer does not depend on what a measurement would have said.
func unprovenReason(b Behavior, opts Options) string {
	why, owned := serviceOwnedBehaviors[b.Name]
	if !owned {
		return ""
	}
	if strings.TrimSpace(opts.RealService) != "" {
		return ""
	}
	return why
}

// unprovenHere records the third verdict and prints it where the run's own
// reader sees it.
//
// Logged through t as well as collected, because somebody running one behaviour
// with -run reads the subtest's output and should not have to know that the
// summary comes from the end of RunDatabase. The word "skipped" is deliberately
// absent: the existing skip line reads "skipped: <provider> does not declare
// <capability>", and a reader must not confuse a provider that makes no claim
// with a run that could not reach the claim it does make.
func unprovenHere(t *testing.T, f *findings, provider, behavior, why string) {
	t.Helper()
	because := why + ".\nThis run asserted no real service through Options.RealService, and " +
		"the absence is what decides this rather than anything the provider declared or any " +
		"reading a stopwatch took. The measurement was NOT taken, because it could not have " +
		"changed the answer and because publishing what a simulator timed invites somebody " +
		"to quote it as though it were about the product.\nTo decide it, point the run at " +
		"the real service and name it in Options.RealService."
	f.add(Finding{Provider: provider, Behavior: behavior, Answer: Unproven, Because: because})
	t.Logf("\nUNPROVEN. %s is not decided by this run, and this is not a pass.\n%s\n",
		behavior, because)
}
