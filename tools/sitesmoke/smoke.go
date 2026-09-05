// Package main is shared by the files in tools/sitesmoke. This file holds the
// part that decides, which is the part with no browser, no network and no
// subprocess in it.
package main

import (
	"fmt"
	"strings"
)

// Answer is what one origin's run concluded.
//
// Three, not two, and the third is the one this tool exists to keep honest. A
// production smoke that cannot say "I did not find out" says "pass" instead,
// because that is the answer with no work attached to it. Every path below
// that is not a proven pass returns Refused or Undecided, and both are red.
type Answer int

const (
	// Allowed means a person's path through this origin worked, proven by the
	// page showing what it shows when it works.
	Allowed Answer = iota
	// Refused means the page showed something else. The application is broken
	// for a person on this hostname right now.
	Refused
	// Undecided means this run did not find out: the origin could not be
	// reached, the browser could not drive it, or two attempts disagreed.
	Undecided
)

// Exit codes, and the reason there are three of them.
//
// `origincheck` in this repository already settled the convention and this
// follows it: 0 allowed, 1 refused, 2 could not tell. Continuous integration
// treats 1 and 2 alike, because both mean the run did not prove the thing it
// was there to prove. They are separate on the command line because they need
// different actions from whoever reads them: a 1 is a broken deployment and a
// 2 is a broken check.
const (
	exitAllowed   = 0
	exitRefused   = 1
	exitUndecided = 2
)

func (a Answer) String() string {
	switch a {
	case Allowed:
		return "allowed"
	case Refused:
		return "refused"
	default:
		return "could not tell"
	}
}

func (a Answer) exitCode() int {
	switch a {
	case Allowed:
		return exitAllowed
	case Refused:
		return exitRefused
	default:
		return exitUndecided
	}
}

// Finding is what one workflow against one origin concluded.
type Finding struct {
	Origin   string
	Workflow string
	Answer   Answer
	// Said is what the tool tells a person. It carries the page's own sentence
	// whenever there was one, because "exploration failed" is a sentence
	// nobody can act on and "It says: Could not reach the server" is one
	// somebody can take straight to the cause.
	Said string
	// Steps is what the agent did, so a failure can be followed by hand.
	Steps []string
	// Evidence is where the screenshot, video and trace of the failure are.
	Evidence []string
}

// decide turns one runner result into a finding.
//
// THE DEFECT THIS IS SHAPED AROUND. A run whose result cannot be read, whose
// results list is empty, or whose verdict is a word this tool does not know
// must not fall through to a pass. That failure has a name in this repository:
// a report published as a pass over a run that failed. Every branch below
// returns Refused or Undecided unless the verdict is literally "pass".
func decide(origin string, result workflowResult) Finding {
	f := Finding{Origin: origin, Workflow: result.Workflow, Steps: result.Steps}
	f.Evidence = evidenceOf(result)
	switch result.Outcome.Verdict {
	case "pass":
		f.Answer = Allowed
		f.Said = "the page showed what it shows when this works."
	case "fail":
		f.Answer = Refused
		f.Said = result.Outcome.Detail
	case "flaky":
		// Not a pass and not a failure, and rounding it to either would be a
		// lie in a different direction. A form that works on one attempt and
		// not on the next is broken for some fraction of the people using it,
		// and this tool cannot say which fraction.
		f.Answer = Undecided
		f.Said = "the same workflow passed on one attempt and failed on another, so this run " +
			"cannot say whether a person's submission works. " + result.Outcome.Detail
	case "blocked":
		// The agent never got far enough to have an opinion. This is the
		// unreachable case, and it reads differently from the case above on
		// purpose: "could not reach the origin at all" and "reached it and it
		// showed an error" are different failures with different first steps.
		f.Answer = Undecided
		f.Said = "the browser could not drive " + origin + " far enough to find out. " +
			result.Outcome.Detail
	case "unverified":
		f.Answer = Undecided
		f.Said = "the run proved nothing either way. " + result.Outcome.Detail
	case "":
		f.Answer = Undecided
		f.Said = "the runner returned a result with no verdict in it."
	default:
		f.Answer = Undecided
		f.Said = fmt.Sprintf("the runner returned a verdict this tool does not know, %q. "+
			"A word it cannot read is not a pass.", result.Outcome.Verdict)
	}
	return f
}

func evidenceOf(result workflowResult) []string {
	var out []string
	for _, e := range []string{result.Evidence.Screenshot, result.Evidence.Video, result.Evidence.Trace} {
		if e != "" {
			out = append(out, e)
		}
	}
	return out
}

// verdict rolls a run's findings into one answer.
//
// The worst answer wins, and an empty set is Undecided rather than Allowed. A
// run that checked nothing has proved nothing, and the shape of bug that
// produces an empty set, a flag misspelled, a hostname list that failed to
// load, is exactly the shape that would otherwise report a green production.
func verdict(findings []Finding) Answer {
	if len(findings) == 0 {
		return Undecided
	}
	worst := Allowed
	for _, f := range findings {
		if f.Answer == Refused {
			return Refused
		}
		if f.Answer == Undecided {
			worst = Undecided
		}
	}
	return worst
}

// summary is what the tool prints last, and it names every origin.
func summary(findings []Finding) string {
	var b strings.Builder
	for _, f := range findings {
		mark := "ok"
		switch f.Answer {
		case Refused:
			mark = "REFUSED"
		case Undecided:
			mark = "COULD NOT TELL"
		}
		fmt.Fprintf(&b, "  %-9s %s  %s\n", mark, f.Origin, f.Workflow)
		fmt.Fprintf(&b, "            %s\n", f.Said)
		// What the agent did, on anything that is not a pass. A failure
		// somebody cannot follow by hand is a failure somebody reruns.
		if f.Answer != Allowed {
			for i, s := range f.Steps {
				fmt.Fprintf(&b, "            %2d. %s\n", i+1, s)
			}
		}
		for _, e := range f.Evidence {
			fmt.Fprintf(&b, "            evidence %s\n", e)
		}
	}
	return b.String()
}
