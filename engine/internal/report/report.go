// Package report renders what a run found, for a person reading a pull
// request.
//
// The audience is somebody who did not ask for this comment and has thirty
// seconds. So the first line is the answer, the detail is folded away, and a
// blocked result is visibly not a failure. A comment that reads as a wall of
// red on a pull request that is fine is a comment people mute, and a muted
// comment is worse than none: it is a check everybody believes is running.
package report

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Run is everything one pull request check produced.
type Run struct {
	Environment string
	URL         string
	Branch      string
	Commit      string
	Golden      string
	Workflows   []Workflow
	// Declared is how many workflows the manifest asked for, which is not the
	// same number as len(Workflows) and was being read as though it were.
	//
	// Workflows holds results. A run whose environment died before the
	// workflows were reached has none, and so does a manifest that declares
	// none, and the gate told both of them "the manifest declares no
	// workflows". The verdict was right in both cases and the reason was false
	// in one, which sends somebody to edit a manifest that was never the
	// problem. It sent somebody there: three example legs of this repository's
	// own nightly were read as declaring no workflows when each declares one,
	// and the runner they could not find was the actual cause.
	Declared   int
	Invariants []Invariant
	// Findings are what the run noticed about the change that is not a
	// workflow or an invariant: migration locks, rewrites, lint, plans, query
	// counts, an unknown destination, unmasked data, a resource left behind.
	// Each carries the level a policy gave it, so the verdict is decided here
	// rather than in the command.
	Findings []Finding
	// Migration is what the rehearsal did, for the section that says it.
	Migration    *Migration
	Load         *Load
	Egress       *Egress
	Verification *Verification
	Cleanup      *Cleanup
	Insights     *Insights
	// Notes say what could not be measured, and why. A report that silently
	// omits a check reads exactly like a check that found nothing.
	Notes []string
	// Skipped is why the check did not run at all, when it did not.
	//
	// A run that was refused before it started is not a run that found
	// nothing, and the difference has to survive as far as the comment. The
	// fork gate is what fills this: a pull request from a fork that no
	// maintainer has approved gets a report saying so, rather than a green
	// tick over an environment that was never created.
	Skipped  string
	Duration string
	// DocsBase is where links point, so a self hosted instance can point at
	// its own copy rather than at ours.
	DocsBase string
}

// Workflow is one agent result.
type Workflow struct {
	Name    string
	Verdict string
	Detail  string
	Steps   []string
	Trace   string
}

// Invariant is what the data said after the workflows ran.
//
// Held and Error are separate for the same reason a workflow's failed and
// blocked are: an invariant that could not be asked has not found anything,
// and printing it as a violation would blame the change for our own gap.
type Invariant struct {
	Name        string
	Description string
	Held        bool
	Columns     []string
	Rows        [][]string
	More        bool
	Error       string
}

// Violated reports whether this invariant was shown to be broken.
func (i Invariant) Violated() bool { return i.Error == "" && !i.Held }

// Load is a traffic result.
type Load struct {
	// Unavailable distinguishes an incomplete experiment from a healthy one.
	Unavailable string
	Source      string
	Routes      []LoadRoute
	Sent        int
	Rate        float64
	ErrorRate   float64
	P95Ms       float64
	Regressed   []string
	// Refused are the routes the generator would not send, because nothing in
	// the manifest named them safe.
	//
	// Without this the comment said the same thing whether the safe list let
	// through every route or one out of forty, so a load run that exercised a
	// fortieth of the application read exactly like one that exercised all of
	// it. The number of requests cannot show it: sending 500 requests at one
	// route looks like sending 500 across forty.
	Refused []string
}

// LoadRoute is the observed request count for one endpoint.
type LoadRoute struct {
	Route  string
	Sent   int
	Errors int
}

// Egress summarises outbound traffic.
type Egress struct {
	Allowed  int
	Refused  int
	Captured int
	Mocked   int
	// Surprises are refused hosts nothing in the manifest mentions, which is
	// usually a dependency somebody added without noticing.
	Surprises []string
	// Sandbox is how many requests a sandbox rule decided.
	Sandbox int
	// Substituted is how many of those had their credential replaced on the
	// way out, which is the whole difference between a sandbox rule and a
	// request that merely says sandbox in the log.
	Substituted int
	// Unsubstituted is how many a sandbox rule let out WITHOUT replacing the
	// credential, so the application's own credential reached the provider.
	//
	// The sidecar substitutes only when a value was configured for the rule's
	// credential name. When none was, it forwards whatever the application
	// sent, and in every other column that request is identical to a working
	// sandbox call: allowed, mode sandbox, the rule named, a normal status.
	// The only evidence is this number, and until it was here nothing counted
	// it, so the report said "4 allowed" and could not say whether those four
	// carried a sandbox credential or a live one.
	Unsubstituted int
	// UnsubstitutedHosts names where those went, so the line is actionable.
	UnsubstitutedHosts []string
}

// Verification is the environment's own branch read back.
//
// It covers the branch rather than the golden it came from, because the branch
// is the database the agents used and it is the one a person is being asked to
// trust. It is taken before the workflows run, so nothing an agent wrote can
// be mistaken for data that came out of the golden unmasked.
type Verification struct {
	Clean       bool
	Columns     int
	RowsSampled int64
	Findings    []string
	// Unavailable says why the branch could not be read back, when it could
	// not. A verification that did not happen is not a verification that
	// passed, and the report says which it was.
	Unavailable string
}

// Verdicts is every word a workflow result may carry, worst first.
//
// The runner decides these and declares them in runner/src/verdict.ts. They are
// two programs in two languages that have to agree on one vocabulary and
// nothing in either compiler can make them, so vocabulary_test.go reads that
// file and this list and fails when they differ, the way the control plane's
// event types are kept in step.
var Verdicts = []string{"fail", "flaky", "blocked", "unverified", "pass"}

// Known reports whether a word is one this engine can read.
//
// Exported because the terminal renders the same words and had its own list.
// One authority, so that a runner ahead of this engine cannot be understood two
// ways in one program.
func Known(verdict string) bool { return slices.Contains(Verdicts, verdict) }

// read is what this engine will treat a runner's word as.
//
// Anything outside Verdicts becomes blocked, because an outcome we cannot read
// is a fact about us rather than about the change. That is the same rule that
// puts a runner failure in blocked and not in fail.
//
// It has to be somewhere, because the alternative was in force and was wrong:
// four separate switches over these words each fell through to a different
// default, and the one in Verdict fell through to pass. A runner one version
// ahead naming a new outcome would have reported the whole run green, exited
// zero, and printed "unverified" beside that workflow in the same comment.
func read(verdict string) string {
	if Known(verdict) {
		return verdict
	}
	return "blocked"
}

// Migration is what the rehearsal did on a throwaway branch of the golden.
//
// It is the narrative half of the migration checks. The half that decides
// anything is in Findings, so a reader who only wants the answer does not have
// to interpret a duration.
type Migration struct {
	// Tool is the migration tool that was recognised.
	Tool string
	// Pending is how many migrations had not been applied to the branch.
	Pending int
	// TotalMS is how long they took against production's row counts.
	TotalMS float64
	// Slowest are the statements worth a line, slowest first.
	Slowest []Statement
	// Locks are the locks that stopped other work.
	Locks []Lock
	// Notes say what could not be measured, and why.
	Notes []string
}

// Statement is one migration statement and what it cost.
type Statement struct {
	SQL     string
	MS      float64
	Rewrote []string
}

// Lock is one table and the strongest lock it was seen under.
type Lock struct {
	Table  string
	Mode   string
	HeldMS float64
	// Blocking is whether another session was ever seen waiting on it.
	Blocking bool
}

// Cleanup is what teardown removed and what it could not.
//
// Nil means teardown was not attempted, which is what --keep asks for. It is
// not the same as a teardown that removed nothing, and the report distinguishes
// them.
type Cleanup struct {
	Removed int
	// Pending is what is still recorded, one line each. The journal remembers
	// them, so af down can finish the job.
	Pending []string
	// Error is the teardown's own failure, when it had one.
	Error string
}

// The six verdicts, worst first. Nothing outside this list is a run verdict,
// and the order here is the order Verdict resolves them in.
const (
	// VerdictFail is a real finding about the change that stops the merge: a
	// workflow that failed, an invariant that did not hold, or a finding a
	// policy puts at fail.
	VerdictFail = "fail"
	// VerdictFlaky is a workflow that passed only sometimes.
	VerdictFlaky = "flaky"
	// VerdictWarn is a real finding about the change that does not stop the
	// merge. It is the level "pass, warning, or block" always described and
	// the engine could not produce.
	VerdictWarn = "warn"
	// VerdictBlocked is the runner or the environment failing to evaluate
	// something. It is NOT a failure and it exits zero: a gap in our tooling
	// must never count against somebody's code.
	VerdictBlocked = "blocked"
	// VerdictUnverified is a workflow that ran and proved nothing either way.
	VerdictUnverified = "unverified"
	// VerdictPass is everything asked and nothing found.
	VerdictPass = "pass"
)

// Insights is what the database noticed while the environment ran.
//
// The part of a change nothing else in a pull request can see. A migration
// reviews as a diff and behaves as a plan, and the plan is only visible
// against real data volume, which is the one thing a preview environment has.
type Insights struct {
	// Sequential is a table the run scanned end to end often enough to be
	// worth naming, with how many rows it holds.
	Sequential []Scan
	// Slowest is the single query that spent the most time, and how much.
	Slowest   string
	SlowestMs float64
	// Unused names indexes nothing read, which on a preview is weaker
	// evidence than a scan and is still the thing somebody wants to know
	// before adding another one.
	Unused []string
	// Missing names the extensions that were not installed, so a section
	// that found nothing is distinguishable from one that could not look.
	Missing []string
}

// Scan is one table read end to end.
type Scan struct {
	Table string
	Scans int64
	Rows  int64
}

// Verdict is the one word answer for the whole run.
//
// A failure outranks everything, then flaky, then warn, then blocked. Blocked
// below all three on purpose: a flaky workflow and a warning are real signals
// about the application, and a blocked one is a signal about us.
//
// Warn sits under flaky rather than over it only because flaky already has a
// headline of its own; the findings section lists everything worst first
// whichever word wins here, so nothing is hidden by the order.
func (r Run) Verdict() string {
	counts := map[string]int{}
	for _, w := range r.Workflows {
		counts[read(w.Verdict)]++
	}
	fail, warn := r.Counts()
	switch {
	case counts[VerdictFail] > 0, r.InvariantsViolated() > 0, fail > 0:
		return VerdictFail
	case r.Load != nil && r.Load.Unavailable != "":
		return VerdictBlocked
	case counts[VerdictFlaky] > 0:
		return VerdictFlaky
	case warn > 0:
		return VerdictWarn
	case counts[VerdictBlocked] > 0:
		return VerdictBlocked
	case counts[VerdictUnverified] > 0:
		return VerdictUnverified
	case len(r.Workflows) == 0:
		return VerdictBlocked
	default:
		return VerdictPass
	}
}

// NothingVerified reports that no workflow reached a verdict about the
// application.
//
// Pass, fail and flaky are verdicts about the application: the run drove it and
// the screen said something. Blocked and unverified are statements about us,
// and a run made only of those has not tested anything, whatever its exit code
// said. A manifest declaring no workflows lands here too, because "nothing was
// tested" is the same fact whether the workflows were missing or unreachable.
//
// Deliberately separate from Verdict. Verdict already resolves to blocked or
// unverified in exactly these cases and is right to; what was missing is
// anybody treating that as a result rather than as an absence of one.
func (r Run) NothingVerified() bool {
	for _, w := range r.Workflows {
		switch read(w.Verdict) {
		case VerdictPass, VerdictFail, VerdictFlaky:
			return false
		}
	}
	return true
}

// Headline is the first line, which is the only line most people read.
func (r Run) Headline() string {
	counts := map[string]int{}
	for _, w := range r.Workflows {
		counts[read(w.Verdict)]++
	}
	_, warns := r.Counts()
	switch r.Verdict() {
	case VerdictPass:
		if len(r.Invariants) > 0 {
			return fmt.Sprintf("All %d workflows passed, and %s held.",
				len(r.Workflows), plural(len(r.Invariants), "invariant", "invariants"))
		}
		return fmt.Sprintf("All %d workflows passed.", len(r.Workflows))
	case VerdictFail:
		// The invariant is named first when the workflows are all green,
		// because "3 workflows passed" above a failing run is the comment
		// people learn to stop believing.
		if counts[VerdictFail] == 0 && r.InvariantsViolated() == 0 {
			// Neither a workflow nor an invariant. Something the database or
			// the network said, and naming it beats reporting a count.
			if worst, ok := r.Worst(); ok {
				return worst.Title
			}
		}
		if counts[VerdictFail] == 0 {
			return fmt.Sprintf("Every workflow passed and %s did not hold.",
				plural(r.InvariantsViolated(), "invariant", "invariants"))
		}
		if v := r.InvariantsViolated(); v > 0 {
			return fmt.Sprintf("%s failed, and %s did not hold.",
				plural(counts[VerdictFail], "workflow", "workflows"),
				plural(v, "invariant", "invariants"))
		}
		return fmt.Sprintf("%s failed.", plural(counts[VerdictFail], "workflow", "workflows"))
	case VerdictFlaky:
		return fmt.Sprintf("%s passed only sometimes.",
			plural(counts[VerdictFlaky], "workflow", "workflows"))
	case VerdictWarn:
		// The count rather than the first title, because a warning headline
		// naming one of four findings reads as though there were one.
		if len(r.Workflows) == 0 {
			return fmt.Sprintf("No workflows ran, and %s to look at.",
				plural(warns, "finding", "findings"))
		}
		return fmt.Sprintf("Nothing failed, and %s to look at.",
			plural(warns, "finding", "findings"))
	case VerdictBlocked:
		if len(r.Workflows) == 0 {
			return "Nothing ran."
		}
		return fmt.Sprintf("%s could not be carried through. Nothing here counts against the change.",
			plural(counts[VerdictBlocked], "workflow", "workflows"))
	default:
		return fmt.Sprintf("%s ran without proving anything either way.",
			plural(counts[VerdictUnverified], "workflow", "workflows"))
	}
}

// InvariantsViolated counts the invariants shown to be broken.
func (r Run) InvariantsViolated() int {
	n := 0
	for _, i := range r.Invariants {
		if i.Violated() {
			n++
		}
	}
	return n
}

// invariantSection is what the data said, for the comment.
//
// One line when everything held, because a run where nothing is wrong should
// cost the reader one line. The violating rows are shown in full when
// something is wrong, since they are the diagnosis and a reader who has to go
// and run the query themselves has been told there is a problem and not what
// it is.
func (r Run) invariantSection() string {
	var b strings.Builder
	violated := r.InvariantsViolated()
	blocked := 0
	for _, i := range r.Invariants {
		if i.Error != "" {
			blocked++
		}
	}

	if violated == 0 && blocked == 0 {
		fmt.Fprintf(&b, "Invariants: %s held.\n\n",
			plural(len(r.Invariants), "invariant", "invariants"))
		return b.String()
	}

	for _, i := range r.Invariants {
		switch {
		case i.Error != "":
			fmt.Fprintf(&b, "Invariant `%s` could not be checked: %s Nothing here counts against the change.\n\n",
				i.Name, oneLine(i.Error))
		case i.Violated():
			fmt.Fprintf(&b, "**Invariant `%s` does not hold.**", i.Name)
			if i.Description != "" {
				fmt.Fprintf(&b, " %s", i.Description)
			}
			b.WriteString("\n\n")
			b.WriteString(evidenceTable(i))
		}
	}
	return b.String()
}

// evidenceTable renders the violating rows.
func evidenceTable(i Invariant) string {
	if len(i.Columns) == 0 || len(i.Rows) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "| %s |\n", strings.Join(i.Columns, " | "))
	b.WriteString("| " + strings.Repeat("--- | ", len(i.Columns)) + "\n")
	for _, row := range i.Rows {
		cells := make([]string, len(row))
		for j, c := range row {
			cells[j] = oneLine(c)
		}
		fmt.Fprintf(&b, "| %s |\n", strings.Join(cells, " | "))
	}
	if i.More {
		b.WriteString("\nMore rows than these. Run the statement against the branch to see them all.\n")
	}
	b.WriteString("\n")
	return b.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// symbol is the mark beside a verdict.
//
// Words rather than coloured circles, because a comment is read in a terminal,
// in an email digest, and by a screen reader, and only one of those renders an
// emoji usefully.
func symbol(verdict string) string {
	switch read(verdict) {
	case VerdictPass:
		return "passed"
	case VerdictFail:
		return "FAILED"
	case VerdictFlaky:
		return "flaky"
	case VerdictWarn:
		return "warning"
	case VerdictBlocked:
		return "blocked"
	default:
		return "unverified"
	}
}

// Markdown renders the comment.
func (r Run) Markdown() string {
	docs := r.DocsBase
	if docs == "" {
		docs = "https://antifailure.dev/docs"
	}
	var b strings.Builder

	fmt.Fprintf(&b, "### Antifailure: %s\n\n", r.Headline())

	// First, and in bold, because everything under it is the report of a run
	// that did not happen. A reader who takes four lines off this comment has
	// to take away that nothing was checked.
	if r.Skipped != "" {
		fmt.Fprintf(&b, "**This check did not run.** %s\n\n", flatten(r.Skipped))
	}

	if r.URL != "" {
		fmt.Fprintf(&b, "Environment `%s` is at %s\n\n", r.Environment, r.URL)
	}

	if len(r.Workflows) > 0 {
		b.WriteString("| Workflow | Result | Detail |\n| --- | --- | --- |\n")
		sorted := append([]Workflow(nil), r.Workflows...)
		// Failures first. Somebody scrolling to find the failure is the same
		// as somebody not seeing it.
		sort.SliceStable(sorted, func(i, j int) bool {
			return rank(sorted[i].Verdict) < rank(sorted[j].Verdict)
		})
		for _, w := range sorted {
			detail := w.Detail
			if w.Verdict == VerdictPass {
				detail = ""
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n", w.Name, symbol(w.Verdict), oneLine(detail))
		}
		b.WriteString("\n")
	}

	// Folded, because the audience did not ask for this comment. Somebody who
	// wants the steps opens it; everybody else reads four lines and moves on.
	for _, w := range r.Workflows {
		if w.Verdict == VerdictPass || len(w.Steps) == 0 {
			continue
		}
		fmt.Fprintf(&b, "<details><summary>How to see <code>%s</code> yourself</summary>\n\n", w.Name)
		for _, step := range w.Steps {
			fmt.Fprintf(&b, "%s\n", step)
		}
		if w.Trace != "" {
			fmt.Fprintf(&b, "\nTrace: `%s`\n", w.Trace)
		}
		b.WriteString("\n</details>\n\n")
	}

	b.WriteString(r.findingSection())

	if len(r.Invariants) > 0 {
		b.WriteString(r.invariantSection())
	}

	b.WriteString(r.migrationSection())

	if v := r.Verification; v != nil {
		switch {
		case v.Unavailable != "":
			fmt.Fprintf(&b, "Masking was not checked on this branch: %s\n\n", oneLine(v.Unavailable))
		case v.Clean:
			fmt.Fprintf(&b,
				"Masking verified: %d columns read back, %d rows sampled, nothing that still parses as real.\n\n",
				v.Columns, v.RowsSampled)
		default:
			fmt.Fprintf(&b, "**Masking did not verify.** %s\n\n", strings.Join(v.Findings, " "))
		}
	}

	if i := r.Insights; i != nil {
		switch {
		case len(i.Missing) > 0 && len(i.Sequential) == 0 && i.Slowest == "":
			// Said out loud rather than rendered as a clean result. An
			// insights section that looked at nothing and a section that
			// looked and found nothing are the same four words on a pull
			// request, and only one of them is evidence.
			fmt.Fprintf(&b, "Insights could not look: %s.\n\n", strings.Join(i.Missing, ", "))
		default:
			fmt.Fprintf(&b, "Insights: ")
			parts := []string{}
			if len(i.Sequential) > 0 {
				names := make([]string, 0, len(i.Sequential))
				for _, s := range i.Sequential {
					names = append(names, fmt.Sprintf("`%s` (%d rows)", s.Table, s.Rows))
				}
				parts = append(parts, "read end to end: "+strings.Join(names, ", "))
			}
			if i.Slowest != "" {
				parts = append(parts, fmt.Sprintf("slowest query %.0fms", i.SlowestMs))
			}
			if len(i.Unused) > 0 {
				parts = append(parts, fmt.Sprintf("%d indexes nothing read", len(i.Unused)))
			}
			if len(parts) == 0 {
				parts = append(parts, "no table read end to end and no slow query")
			}
			fmt.Fprintf(&b, "%s.\n\n", strings.Join(parts, "; "))
		}
	}

	if e := r.Egress; e != nil {
		fmt.Fprintf(&b, "Outbound: %d allowed, %d refused, %d captured, %d mocked.\n",
			e.Allowed, e.Refused, e.Captured, e.Mocked)
		if e.Sandbox > 0 {
			// Stated either way. "All 4 sandbox calls had the credential
			// replaced" is worth a line precisely because its absence is the
			// thing that matters, and a reader who only ever sees the line
			// when something is wrong learns nothing from its absence.
			fmt.Fprintf(&b, "Sandbox: %d of %d calls had the credential replaced on the way out.\n",
				e.Substituted, e.Sandbox)
		}
		if e.Unsubstituted > 0 {
			fmt.Fprintf(&b,
				"%s left under a sandbox rule WITHOUT the credential being replaced, so the "+
					"application's own credential reached %s. Set the sandbox credential the "+
					"rule names.\n",
				plural(e.Unsubstituted, "1 request", fmt.Sprintf("%d requests", e.Unsubstituted)),
				strings.Join(e.UnsubstitutedHosts, ", "))
		}
		if len(e.Surprises) > 0 {
			fmt.Fprintf(&b,
				"Refused hosts nothing in the manifest mentions: %s. If this change means to reach one, add a rule.\n",
				strings.Join(e.Surprises, ", "))
		}
		b.WriteString("\n")
	}

	if l := r.Load; l != nil {
		if l.Unavailable != "" {
			fmt.Fprintf(&b, "Load was inconclusive: %s\n", oneLine(l.Unavailable))
		}
		fmt.Fprintf(&b, "Load: %d requests at %.0f a second, p95 %.0fms, %.1f%% failed.\n",
			l.Sent, l.Rate, l.P95Ms, l.ErrorRate*100)
		if l.Source != "" {
			fmt.Fprintf(&b, "Traffic source: %s.\n", oneLine(l.Source))
		}
		for _, route := range l.Routes {
			fmt.Fprintf(&b, "- %s: %d requests, %d errors.\n", oneLine(route.Route), route.Sent, route.Errors)
		}
		if len(l.Regressed) > 0 {
			fmt.Fprintf(&b, "Slower than production: %s\n", strings.Join(l.Regressed, ", "))
		}
		if len(l.Refused) > 0 {
			// The verb travels with the noun, because the singular case
			// rendered "1 route were not sent". Only the plural case had a
			// test, and 500 requests at one route is exactly the run this line
			// exists to describe, so the ungrammatical half is the half a
			// reader is most likely to meet.
			fmt.Fprintf(&b, "%s not sent, because nothing in the manifest named them safe: %s\n",
				plural(len(l.Refused), "route was", "routes were"), strings.Join(l.Refused, ", "))
		}
		b.WriteString("\n")
	}

	if c := r.Cleanup; c != nil {
		switch {
		case c.Error != "":
			fmt.Fprintf(&b, "**Teardown failed after removing %d resources.** %s\n\n",
				c.Removed, oneLine(c.Error))
		case len(c.Pending) > 0:
			fmt.Fprintf(&b, "**Teardown removed %d resources and left %s behind:**\n",
				c.Removed, plural(len(c.Pending), "one", "these"))
			for _, p := range c.Pending {
				fmt.Fprintf(&b, "- %s\n", oneLine(p))
			}
			b.WriteString("\nThe journal remembers them. Run `af down` against this environment to finish the job.\n\n")
		default:
			fmt.Fprintf(&b, "Torn down: %d resources removed, nothing left behind.\n\n", c.Removed)
		}
	}

	for _, n := range r.Notes {
		fmt.Fprintf(&b, "Not measured: %s\n\n", oneLine(n))
	}

	// Not on a run that was refused before it started. "The environment or the
	// runner could not carry a workflow through" is the wrong sentence for a
	// decision somebody made on purpose, and it reads as an apology for a bug.
	if r.Verdict() == VerdictBlocked && r.Skipped == "" {
		fmt.Fprintf(&b,
			"Blocked means the environment or the runner could not carry a workflow through. "+
				"It is not counted against this change. [What blocked means](%s/concepts/verdicts)\n\n",
			docs)
	}
	if r.Verdict() == VerdictWarn {
		fmt.Fprintf(&b,
			"A warning is a real finding about this change that does not fail the check. "+
				"Which findings fail is the manifest's `policy` block. "+
				"[What the verdicts mean](%s/concepts/verdicts)\n\n",
			docs)
	}

	fmt.Fprintf(&b, "<sub>%s", r.Branch)
	if r.Commit != "" {
		fmt.Fprintf(&b, " at `%s`", short(r.Commit))
	}
	if r.Duration != "" {
		fmt.Fprintf(&b, " in %s", r.Duration)
	}
	if r.Golden != "" {
		fmt.Fprintf(&b, ", from golden `%s`", r.Golden)
	}
	fmt.Fprintf(&b, ". <a href=\"%s\">Docs</a></sub>\n", docs)
	return b.String()
}

func rank(verdict string) int {
	switch read(verdict) {
	case VerdictFail:
		return 0
	case VerdictFlaky:
		return 1
	case VerdictWarn:
		return 2
	case VerdictBlocked:
		return 3
	case VerdictUnverified:
		return 4
	default:
		return 5
	}
}

// flatten is oneLine's sibling for prose that is not in a table.
//
// The cap is much higher because a finding's detail is a paragraph rather than
// a cell, and truncating "so the window is the statement rather than the whole
// migration" at 120 characters loses the half that says what to do. There is
// still a cap, because one of these can carry a database error and an error
// that fills the comment is an error nobody reads past.
func flatten(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const max = 500
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// oneLine keeps a table cell a table cell.
func oneLine(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	const max = 120
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

// Marker identifies this comment so an update replaces it.
//
// A check that adds a comment per push turns a pull request with twelve pushes
// into one with twelve comments, and the twelfth is the only one that is true.
const Marker = "<!-- antifailure:report -->"

// Comment returns the comment body, with the marker.
func (r Run) Comment() string { return Marker + "\n" + r.Markdown() }

// findingSection is what the run noticed that is not a workflow or an
// invariant, worst first.
//
// Nothing at all when there is nothing to say, because a heading over an empty
// list is a line the reader pays for and learns nothing from. A failing
// finding is bold and a warning is not, so the two are told apart at a glance
// rather than by reading the level.
func (r Run) findingSection() string {
	shown := make([]Finding, 0, len(r.Findings))
	for _, f := range r.Findings {
		if f.Level != LevelIgnore {
			shown = append(shown, f)
		}
	}
	if len(shown) == 0 {
		return ""
	}
	// Stable, so that two findings at the same level keep the order they were
	// added in, which is the order the checks run in and the order somebody
	// would work through them.
	sort.SliceStable(shown, func(i, j int) bool {
		return findingRank(shown[i].Level) < findingRank(shown[j].Level)
	})

	var b strings.Builder
	b.WriteString("**What this change does to the database and the network**\n\n")
	for _, f := range shown {
		title := f.Title
		if f.Level == LevelFail {
			title = "**" + title + "**"
		}
		fmt.Fprintf(&b, "%s `%s`", title, f.Rule)
		if f.Where != "" {
			fmt.Fprintf(&b, " on `%s`", f.Where)
		}
		b.WriteString("\n")
		if f.Detail != "" {
			fmt.Fprintf(&b, "%s\n", flatten(f.Detail))
		}
		if f.Fix != "" {
			fmt.Fprintf(&b, "Instead: %s\n", flatten(f.Fix))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// migrationSection is what the rehearsal did, folded away.
//
// Folded because it is the evidence rather than the answer: the answer is in
// the findings above, and somebody who wants to know what the migration cost
// on production's row counts opens it. A run with nothing pending still gets a
// line, because "no migrations in this change" is worth saying once and reads
// nothing like a rehearsal that did not happen.
func (r Run) migrationSection() string {
	m := r.Migration
	if m == nil {
		return ""
	}
	var b strings.Builder
	if m.Pending == 0 && len(m.Notes) == 0 {
		b.WriteString("Migrations: nothing pending on this branch.\n\n")
		return b.String()
	}
	if m.Pending == 0 {
		for _, n := range m.Notes {
			fmt.Fprintf(&b, "Migrations: %s\n\n", oneLine(n))
		}
		return b.String()
	}

	fmt.Fprintf(&b, "<details><summary>%s rehearsed against production's row counts, %s in total</summary>\n\n",
		plural(m.Pending, "migration", "migrations"), duration(m.TotalMS))
	if m.Tool != "" {
		fmt.Fprintf(&b, "Tool: `%s`\n\n", m.Tool)
	}
	if len(m.Slowest) > 0 {
		b.WriteString("| Statement | Took | Rewrote |\n| --- | --- | --- |\n")
		for _, st := range m.Slowest {
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n",
				oneLine(st.SQL), duration(st.MS), strings.Join(st.Rewrote, ", "))
		}
		b.WriteString("\n")
	}
	if len(m.Locks) > 0 {
		b.WriteString("| Table | Lock | Held for at least | Blocked another session |\n")
		b.WriteString("| --- | --- | --- | --- |\n")
		for _, l := range m.Locks {
			waited := "no"
			if l.Blocking {
				waited = "yes"
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", l.Table, l.Mode, duration(l.HeldMS), waited)
		}
		b.WriteString("\nSampled every 250ms, so each figure is a lower bound rather than a measurement.\n\n")
	}
	for _, n := range m.Notes {
		fmt.Fprintf(&b, "Not measured: %s\n\n", oneLine(n))
	}
	b.WriteString("</details>\n\n")
	return b.String()
}
