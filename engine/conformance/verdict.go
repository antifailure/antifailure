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
// It is not a skip. The measurement runs in full: two goldens are built, both
// arms are timed, the sizes are checked, the ballast is weighed in the branch,
// and every number is printed. Unproven changes the VERDICT the readings are
// turned into, never whether they were taken.
//
// It is not a pass. Answer.Publishable is false for it, RunDatabase prints a
// block at the end of the run naming every behaviour that reached it, and
// CopyOnWriteClaim renders the provider's declaration as the word "unproven"
// rather than as the value the provider declared.
//
// It is not something a provider can ask for. The only thing that raises it is
// Options.HarnessCopiesEveryBranch, which is a claim by the TEST FIXTURE about
// the storage it built, and the suite refuses that claim in three ways rather
// than accepting the sentence. Options.HarnessCopiesEveryBranch says why.

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
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
