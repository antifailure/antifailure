package errors

// A REMEDY THAT CANNOT RUN FROM THE STATE THE REFUSAL LEAVES BEHIND.
//
// AF-MSK-010 refuses masking on a table stored with an access method that
// implements neither the ctid a keyless rewrite addresses a row by nor the
// UPDATE a keyed one runs. That refusal is right, and its message already
// carries the remedy: give the column a rule that preserves it, or move it out
// of a table the engine cannot rewrite.
//
// Its Next line said to run `af mask plan`. From the state the refusal leaves
// you in there is no branch, and `af mask plan` reads a branch's schema, so it
// answers AF-DB-014 and exits 5. Measured on 2026-09-21 in a project whose
// refresh had aborted:
//
//	AF-DB-014 No database branch exists for seedrepro-main-07bf07.
//	  Next: Run 'af up' to create one.
//	PRODUCER rc=5
//
// And on the other path it was worse than unreachable. `af mask plan` raises
// AF-MSK-010 itself, at cli/mask.go, when the plan it just printed is not
// runnable, so the remedy there was the command that had just run.
//
// This matters more than the wording. The argument this product makes is that
// a refusal is more useful than a wrong answer, and the Next line is the whole
// of what makes a refusal useful rather than merely correct. One that sends
// somebody to a command which then fails for an unrelated reason teaches them
// that the error messages are not to be trusted, which costs more than the
// sentence saved.
//
// What this test can and cannot do: it cannot work out which paths reach which
// code, so it cannot discover the next one of these on its own. It holds the
// pairs that were established by running the commands, so that the sentence
// cannot come back, and so that anybody adding a code has the list in front of
// them. The finding itself came from a sweep of every next_step in the catalog
// and is recorded in the commit message.

import (
	"strings"
	"testing"
)

// needsAnEnvironment names commands that read a branch, and can therefore only
// answer once `af up` has made one. `af mask plan` is the one this test was
// written for: its command builds an orchestrator and calls MaskPlan, which
// reaches the provider's ConnString for the environment's branch, and that is
// where AF-DB-014 is raised.
var needsAnEnvironment = []string{"af mask plan"}

// refusedBeforeAnEnvironmentExists are codes raised on a path that has not
// made a branch, or that has already given up on the one it was making.
//
// AF-MSK-010 is raised from three places and none of them has a branch to
// offer: two inside a golden refresh, which works on a candidate that is
// destroyed when the plan turns out not to be runnable, and one inside
// `af mask plan` itself.
var refusedBeforeAnEnvironmentExists = []Code{AFMSK010}

func TestARemedyDoesNotNameACommandTheRefusalHasMadeUnrunnable(t *testing.T) {
	for _, code := range refusedBeforeAnEnvironmentExists {
		entry := Lookup(code)
		if entry.Area == "UNK" {
			t.Fatalf("%s is not in the catalog", code)
		}
		for _, cmd := range needsAnEnvironment {
			// Naming the command is allowed, and saying when it can be run is
			// the point of allowing it. Telling somebody to RUN it is not,
			// because from here it answers AF-DB-014 rather than answering
			// them.
			for _, phrase := range []string{
				"Run '" + cmd + "'", "run '" + cmd + "'",
				"Run `" + cmd + "`", "run `" + cmd + "`",
			} {
				if strings.Contains(entry.NextStep, phrase) {
					t.Errorf("%s tells the reader to %q, and there is no environment "+
						"for it to read when this refusal is raised, so it answers "+
						"AF-DB-014 and exits 5", code, phrase)
				}
			}
		}
	}
}

// TestTheMaskingRefusalStillCarriesARemedyAtAll.
//
// The other half, and the reason the test above is not simply a ban. Removing
// the sentence would also pass a ban, and an empty Next line is the failure
// this whole catalog exists to prevent: a refusal with nothing after it is
// correct and useless. The remedy the message body already carries is the one
// that works from here, so the Next line has to carry it too.
func TestTheMaskingRefusalStillCarriesARemedyAtAll(t *testing.T) {
	entry := Lookup(AFMSK010)
	if entry.Area == "UNK" {
		t.Fatal("AF-MSK-010 is not in the catalog")
	}
	if len(strings.TrimSpace(entry.NextStep)) == 0 {
		t.Fatal("AF-MSK-010 has no next step, so the refusal says only that it refused")
	}
	for _, want := range []string{"rule that preserves it", "address a row"} {
		if !strings.Contains(entry.NextStep, want) {
			t.Errorf("AF-MSK-010's next step does not carry %q, which is the remedy that "+
				"works from the state this refusal leaves the reader in", want)
		}
	}
}
