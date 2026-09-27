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
	"time"

	"github.com/antifailure/antifailure/engine/internal/explore"
	"github.com/antifailure/antifailure/engine/internal/pgcrash"
)

// Run is everything one pull request check produced.
type Run struct {
	Environment string
	URL         string
	Branch      string
	Commit      string
	Golden      string
	Workflows   []Workflow
	Exploration *Exploration
	// AccessProbe holds the observations the security access-probe pass made:
	// each persona's reach of each declared object, and whether the object's
	// canary came back. It is a separate channel from Exploration because it is
	// not a declared goal and must not be judged by the exploration's
	// completeness rules; the security collector reads its observations for the
	// authz differential and nothing renders it as an exploration section. Nil
	// when the manifest declares no access fixtures, which is every run today.
	AccessProbe *Exploration
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
	// Chaos is what the fault injection run broke and what the recovery
	// afterwards was shown to have done.
	Chaos *Chaos
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
	// Drafted reports that the run used a manifest drafted from the
	// repository, because there was none to read. The comment says so before
	// anything else, since every line under it describes a configuration
	// nobody wrote.
	Drafted bool `json:"drafted"`
	// EmptySource reports that the golden this run branched was built from
	// nothing: database.source_url_env named nothing and no seed ran, so the
	// migrations built the schema and there were no rows. Said before the
	// workflow table, because a run against an empty database looks exactly
	// like one against production to everything except the data.
	EmptySource bool `json:"empty_source"`
}

// Exploration preserves the actual browser observations separately from
// declared workflow assertions. Missing goals are incomplete, never clean.
type Exploration struct {
	Declared    []string
	Results     []explore.Exploration
	Unavailable string
}

// Incomplete names a configured exploration whose execution was not proved.
func (e *Exploration) Incomplete() bool {
	if e == nil {
		return false
	}
	if e.Unavailable != "" || len(e.Declared) == 0 {
		return true
	}
	seen := map[string]bool{}
	for _, x := range e.Results {
		if x.Outcome.Verdict != VerdictPass || len(x.Visited) == 0 || x.Evidence.Trace == "" {
			return true
		}
		if seen[x.Name] {
			return true
		}
		seen[x.Name] = true
	}
	for _, name := range e.Declared {
		if !seen[name] {
			return true
		}
	}
	return len(e.Results) != len(e.Declared)
}

func (e *Exploration) markdown() string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("<details><summary>Exploration observations</summary>\n\n")
	b.WriteString("These are observations, not workflow assertions.\n\n")
	if e.Incomplete() {
		b.WriteString("Configured exploration is incomplete. This is not a clean exploration.\n\n")
	}
	if e.Unavailable != "" {
		fmt.Fprintf(&b, "%s\n\n", flatten(e.Unavailable))
	}
	for _, name := range e.Declared {
		found := false
		for _, x := range e.Results {
			if x.Name == name {
				found = true
			}
		}
		if !found {
			fmt.Fprintf(&b, "Goal `%s` produced no browser result.\n\n", oneLine(name))
		}
	}
	for _, x := range e.Results {
		fmt.Fprintf(&b, "Goal `%s`: %s visited, %s, %s. %s\n\n", oneLine(x.Name), plural(len(x.Visited), "page", "pages"), plural(len(x.Journey), "move", "moves"), plural(len(x.Findings), "observation", "observations"), flatten(x.Outcome.Detail))
		for _, f := range x.Findings {
			fmt.Fprintf(&b, "- %s at %s, step %d: %s\n", f.Kind.Title(), oneLine(f.URL), f.Step, flatten(f.Detail))
		}
		for _, missing := range x.Missing {
			fmt.Fprintf(&b, "Not explored: %s\n\n", flatten(missing))
		}
		if x.Evidence.Trace != "" {
			fmt.Fprintf(&b, "Trace: `%s`\n\n", oneLine(x.Evidence.Trace))
		}
	}
	b.WriteString("</details>\n\n")
	return b.String()
}

// Workflow is one agent result.
type Workflow struct {
	Name    string
	Verdict string
	// Cause is the runner's reason for the verdict. Carried because blocked is
	// several different facts, and the one a manifest can fix, a workflow its
	// budget stopped, has to be told apart from a runner that never started.
	Cause  string
	Detail string
	Steps  []string
	Trace  string
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

// The six verdicts. Verdict also refuses to hide an incomplete configured
// experiment behind an advisory result.
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
// A configured exploration that never completed is an exception: it reports
// blocked before advisories, matching the hosted check's incomplete result.
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
	case r.Load != nil && r.Load.Unavailable != "", r.Exploration.Incomplete():
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
	verdict := r.Verdict()
	// A drafted manifest's workflows are guesses about what the application
	// does, and a run of them that reached no verdict has verified nothing
	// about it. The blocked and unverified headlines below say "could not be
	// carried through", which is about the runner, and this is about the
	// configuration, so it is said in its own words.
	if r.Drafted && r.NothingVerified() && len(r.Workflows) > 0 &&
		(verdict == VerdictBlocked || verdict == VerdictUnverified) {
		return "Nothing was verified. The workflows were drafted, not written for this application."
	}
	switch verdict {
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
		if r.Exploration.Incomplete() {
			return "Configured exploration could not be completed. Nothing here counts against the change."
		}
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

	// Right after the headline, because every line under it describes a
	// configuration nobody wrote, and the one command that changes that is
	// worth more to the reader than the run.
	if r.Drafted {
		b.WriteString(DraftedSentence + "\n\n")
	}

	if r.URL != "" {
		fmt.Fprintf(&b, "Environment `%s` is at %s\n\n", r.Environment, r.URL)
	}

	// Before the workflow table. A workflow that passed against no rows has
	// not passed against production's shape, and a reader who takes the
	// table first takes away the wrong thing.
	if r.EmptySource {
		b.WriteString(EmptySourceSentence + "\n\n")
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
	b.WriteString(r.Exploration.markdown())

	if len(r.Invariants) > 0 {
		b.WriteString(r.invariantSection())
	}

	b.WriteString(r.migrationSection())
	// After the migration and before the masking verification. The chaos run
	// happens last in a check and this section sits in the middle of the
	// report, because a reader is looking for what the change did to the
	// database and a crash recovery is the most database shaped thing here.
	b.WriteString(r.chaosSection())

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

// DraftedSentence is the block that opens a report run against a drafted
// manifest.
const DraftedSentence = "**No antifailure.yaml in this repository.** This run used a manifest " +
	"Antifailure drafted from the repository. Run `af init` and commit the file to make it yours."

// EmptySourceSentence is the block that precedes the workflow table when the
// golden held no production data. The same words the engine prints during
// `af up`, so a reader who saw it in a terminal recognises it here.
const EmptySourceSentence = "**This ran on an empty database.** `database.source_url_env` names " +
	"nothing, so the migrations built the schema and no production data was masked or " +
	"branched. Set `database.source_url_env: PRODUCTION_DATABASE_URL` and add that secret " +
	"to the repository."

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

// Chaos is what the fault injection run did, and what it proved about the
// recovery that followed.
//
// A section of its own rather than findings alone, because a reader needs the
// numbers whether or not anything was wrong: the count of acknowledged commits
// is what makes "nothing was lost" mean something, and a run that lost nothing
// out of four commits has said almost nothing. The findings say what to do and
// this says what was measured.
type Chaos struct {
	// Faults is one entry per fault the manifest declared, in the order they
	// were run.
	Faults []ChaosFault
	// Skipped is why the chaos block did not run, when it did not. It is here
	// rather than in Notes because a chaos block that was declared and did not
	// run has to be visible next to the section it would have filled.
	Skipped string
}

// ChaosFault is one fault and its outcome.
type ChaosFault struct {
	Name   string
	Kind   string
	Target string
	// Evidence is what the injector said it did at the moment it did it: the
	// process it killed, the exit code the container carried, the network it
	// detached. It is what backs the claim that the fault landed.
	Evidence string
	// Injected is whether the fault was applied at all, and Undone is whether
	// its undo ran. A fault that was applied and not undone leaves an
	// environment in a state the next thing to run will meet.
	Injected bool
	Undone   bool
	// Error is why the fault could not be injected, when it could not.
	Error string
	// Refused is whether the fault was turned down by the guard that keeps a
	// fault inside this environment, before it touched anything. That is a
	// different fact from an Error with Refused false, which is a fault that
	// tried to go in and failed partway: a refusal leaves the environment as
	// it found it, so every other fault in the run was measured against an
	// unbroken system and its result stands. Reporting the two alike told a
	// reader that a durability proof run AFTER a refused disk fill meant
	// nothing, on the same screen that showed it.
	Refused bool
	// Recovery is the durability proof, when one was run around this fault.
	Recovery *ChaosRecovery
	// Invariants is what the manifest's own rules about the user's own data
	// said before the fault and after the recovery. Empty when the manifest
	// declares none, and empty when no durability proof ran around this
	// fault.
	//
	// Beside Recovery rather than inside it, because the two do not fail
	// together. A database that does not come back leaves Recovery nil and
	// leaves this arm with the most important thing it will ever say, which is
	// that the project's own rules were never asked of the recovered database
	// because there is not one.
	Invariants []ChaosInvariant
	// DurationMs is how long the fault's whole step took: the declared wait
	// before it, the fault, the undo and any verification. It is not how long
	// the fault was in place, which is InPlaceMs.
	DurationMs int64
	// InPlaceMs is how long the fault was measured to be in place: from the
	// moment the injection returned to the moment its undo began. Zero when
	// it never went in. HoldDeclaredMs is the hold the manifest asked for,
	// carried beside it so a reader compares the two rather than trusting
	// either one alone.
	InPlaceMs      int64
	HoldDeclaredMs int64
}

// chaosKindWithNoUndo is the one fault kind the injector cannot reverse,
// spelled as the manifest spells it. The fault package owns the constant and a
// test in the env package, which imports both, holds the two to one string.
const chaosKindWithNoUndo = "process_kill"

// InPlaceSays is the sentence every surface uses for how long a fault was in
// place, measured, beside what the manifest declared.
//
// One sentence in one place, because the number it replaces was read by an
// agent as the length of a network partition when it measured nothing at all:
// a duration of 0 ms beside a declared five second hold made the reader doubt
// the cut had lasted, and nothing on the screen said which of the two to
// believe. A fault that went in and has no measured hold says that instead of
// printing a zero.
func (f ChaosFault) InPlaceSays() string {
	if !f.Injected {
		return ""
	}
	declared := ""
	if f.HoldDeclaredMs > 0 {
		declared = fmt.Sprintf(" (declared %s)", millis(f.HoldDeclaredMs))
	}
	if f.InPlaceMs <= 0 {
		return "in place for no measurable time" + declared
	}
	if f.Kind == chaosKindWithNoUndo {
		// A killed process has nothing to put back, so there is no span in
		// which it is "in place". The hold is the wait before the result is
		// read, which the manifest's own description says.
		return fmt.Sprintf("followed by a wait of %s%s before the result was read, since a killed process has no undo",
			millis(f.InPlaceMs), declared)
	}
	undone := ", then undone"
	if !f.Undone {
		undone = ", and its undo did not complete"
	}
	return fmt.Sprintf("in place for %s%s%s", millis(f.InPlaceMs), declared, undone)
}

// ChaosInvariant is one of the manifest's own invariants, asked before the
// fault and again after the recovery.
//
// Both sides are carried because one side alone cannot say what a violation
// means. A rule that is broken after a crash and was broken before it is not
// something the crash did, and reporting it as one blames a fault for a defect
// the run inherited.
type ChaosInvariant struct {
	Name        string
	Description string
	// BeforeHeld and AfterHeld are true when the statement returned no rows on
	// that side. BeforeError and AfterError say why a side has no verdict at
	// all, and a side that carries one is not a violation: read the error
	// first, the way every other invariant result in this package is read.
	BeforeHeld  bool
	BeforeError string
	AfterHeld   bool
	AfterError  string
	// Columns, Rows and More are the violating rows from AFTER the recovery,
	// bounded, so a reader sees which rows rather than only how many. More is
	// true when there were others that were not kept.
	Columns []string
	Rows    [][]string
	More    bool
}

// BeforeSays and AfterSays are what each side came out as, in the words every
// surface uses.
//
// One sentence in one place, because the terminal and the pull request comment
// have already drifted once in this feature: the terminal learned to say what
// a run had established about torn pages while the comment said nothing, and
// the comment called a failed amcheck a pass while the terminal quoted it.
func (i ChaosInvariant) BeforeSays() string {
	return chaosInvariantSide(i.BeforeHeld, i.BeforeError, 0, false)
}

// AfterSays is the same for the side read after the recovery, and it is the
// side that carries the evidence rows.
func (i ChaosInvariant) AfterSays() string {
	return chaosInvariantSide(i.AfterHeld, i.AfterError, len(i.Rows), i.More)
}

// Attributable reports whether this invariant is one the fault can be blamed
// for: it held before, it was asked after, and it does not hold now.
func (i ChaosInvariant) Attributable() bool {
	return i.BeforeError == "" && i.AfterError == "" && i.BeforeHeld && !i.AfterHeld
}

// chaosInvariantSide renders one side.
//
// The error is read before the verdict, so a side that could not be asked can
// never print as a violation. "Not asked" and "violated" are the two answers
// this whole feature exists to keep apart.
func chaosInvariantSide(held bool, errText string, rows int, more bool) string {
	switch {
	case errText != "":
		return "not asked: " + oneLine(errText)
	case held:
		return "held"
	case more:
		return fmt.Sprintf("violated, more than %d rows", rows)
	case rows == 1:
		return "violated, 1 row"
	case rows == 0:
		// A statement that returned rows and kept none is a bound of zero,
		// which nothing configures today. It is said as what is known rather
		// than as a count, because "violated, 0 rows" reads as a pass.
		return "violated"
	}
	return fmt.Sprintf("violated, %d rows", rows)
}

// ChaosRecovery is what the crash proof established.
type ChaosRecovery struct {
	// Crashed and Signal are read from the database's own log.
	Crashed bool
	Signal  int
	// Replayed says the write ahead log was replayed, and RedoStart and
	// RedoEnd are how far.
	Replayed           bool
	RedoStart, RedoEnd string
	// StateBefore and StateAfter are the cluster state from its control file
	// either side of the fault.
	StateBefore, StateAfter string
	// Acknowledged is how many commits the client was told were committed,
	// Present is how many rows survived, Lost is how many acknowledged commits
	// are gone, Phantom is how many rows no client wrote, and InFlightLanded
	// is how many commits that were in flight at the crash did land.
	Acknowledged, Present, Lost, Phantom, InFlightLanded int
	// HeapRows and IndexRows are the two independent counts, and Amcheck is
	// what the index verifier said.
	HeapRows, IndexRows int64
	Amcheck             string
	// ChecksumsOn reports whether a torn page would have been detected.
	ChecksumsOn bool
	// DowntimeMs is how long the database did not answer a query, measured
	// by a probe that ran beside the fault from the moment it was injected.
	// Unreachable is whether it ever stopped answering, Recovered whether it
	// was seen answering again before the probe stopped, and ProbeIntervalMs
	// is how often the probe asked, which is the measurement's resolution.
	DowntimeMs      int64
	Unreachable     bool
	Recovered       bool
	ProbeIntervalMs int64
	// Verified reports whether the run established what it set out to. A run
	// that is not verified has not passed: it has not looked.
	Verified bool
}

// readBackUnfinished is why nothing after the counts was asked: the proof
// consults amcheck only once both scans of the writers' table have returned,
// so an empty answer means the read back stopped first.
const readBackUnfinished = "reading the writers' table back after the fault did not finish"

// AmcheckPassed reports whether amcheck verified the index. Only the one
// sentence pgcrash records for a pass counts; every other answer is the reason
// it could not say so, including bt_index_check reporting a problem.
func (rec *ChaosRecovery) AmcheckPassed() bool { return rec.Amcheck == pgcrash.AmcheckPassed }

// AmcheckSays is what the index verifier said, in its own words, or that it
// was never asked. Empty would read as nothing wrong.
func (rec *ChaosRecovery) AmcheckSays() string {
	if rec.Amcheck == "" {
		return "did not run, because " + readBackUnfinished
	}
	return rec.Amcheck
}

// PagesSay says what this run established about torn pages, and only that.
//
// One function for the terminal and the pull request comment, because the two
// used to be written separately and drifted: the terminal learned to say this
// while the comment said nothing, and the comment called a failed amcheck a
// pass while the terminal quoted it.
//
// A page torn by the crash is only DETECTED when data checksums are on: with
// them off Postgres reads it back as data. So the claim is made only when all
// three hold. The control file was read after the fault, because a checksum
// version that was never read is not a zero. Checksums are on. And the read
// back finished, which is what a non empty amcheck answer means, because the
// proof asks amcheck only after it has counted the heap by sequential scan and
// a checksum failure there would have stopped it first. The claim covers the
// writers' table and nothing else, since that is all the proof reads.
func (rec *ChaosRecovery) PagesSay() string {
	switch {
	case rec.StateAfter == "":
		return "not checked, because the control file could not be read after the fault, so whether data checksums are on is unknown"
	case !rec.ChecksumsOn:
		return "not checked, because data checksums are off on this cluster and a torn page would read back as data"
	case rec.Amcheck == "":
		return "not checked, because " + readBackUnfinished
	}
	return "the writers' table read back in full with data checksums on, and no page of it failed its checksum. No other table was read."
}

// chaosSection renders what the faults did.
func (r Run) chaosSection() string {
	c := r.Chaos
	if c == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("**What happened when this environment was broken on purpose**\n\n")
	if c.Skipped != "" {
		fmt.Fprintf(&b, "No fault was injected: %s\n\n", oneLine(c.Skipped))
		return b.String()
	}
	for _, f := range c.Faults {
		target := f.Target
		if target == "" {
			target = "the environment"
		}
		switch {
		case f.Error != "" && f.Injected:
			// A fault that WENT IN and then failed is not a fault that could
			// not be injected, and "nothing after it was measured" is false of
			// it: the invariant arm was measured, and it is printed under this
			// line. A database that does not come back after a crash arrives
			// here, and the sentence below used to send the reader to look at
			// a fault that had landed.
			fmt.Fprintf(&b, "Fault `%s` went into %s and the run around it did not finish: %s\n\n",
				f.Name, oneLine(target), oneLine(f.Error))
			b.WriteString(chaosInvariantTable(f))
			continue
		case f.Error != "":
			fmt.Fprintf(&b, "Fault `%s` could not be injected into %s: %s Nothing after it was measured.\n\n",
				f.Name, oneLine(target), oneLine(f.Error))
			continue
		case !f.Injected:
			fmt.Fprintf(&b, "Fault `%s` did not run.\n\n", f.Name)
			continue
		}
		fmt.Fprintf(&b, "Fault `%s` (%s) on %s: %s. It was %s.\n\n",
			f.Name, oneLine(f.Kind), oneLine(target), strings.TrimSuffix(oneLine(f.Evidence), "."), f.InPlaceSays())
		rec := f.Recovery
		if rec == nil {
			b.WriteString(chaosInvariantTable(f))
			continue
		}
		b.WriteString("| What was measured | Result |\n| --- | --- |\n")
		fmt.Fprintf(&b, "| The database crashed | %s |\n", crashCell(rec))
		fmt.Fprintf(&b, "| The write ahead log replayed | %s |\n", replayCell(rec))
		fmt.Fprintf(&b, "| Commits the client was told were committed | %d |\n", rec.Acknowledged)
		fmt.Fprintf(&b, "| Of those, missing after recovery | %d |\n", rec.Lost)
		fmt.Fprintf(&b, "| Rows present that no client wrote | %d |\n", rec.Phantom)
		fmt.Fprintf(&b, "| Commits in flight at the crash that landed | %d |\n", rec.InFlightLanded)
		fmt.Fprintf(&b, "| Heap and index agree | %s |\n", agreeCell(rec))
		fmt.Fprintf(&b, "| Torn pages in the writers' table | %s |\n", rec.PagesSay())
		fmt.Fprintf(&b, "| Cluster state, before and after | %s, then %s |\n",
			orUnknown(rec.StateBefore), orUnknown(rec.StateAfter))
		fmt.Fprintf(&b, "| The database was unreachable for | %s |\n", rec.UnreachableSays())
		b.WriteString("\n")
		b.WriteString(chaosInvariantTable(f))
		if !rec.Verified {
			b.WriteString("This fault's recovery was not established. The findings above say what could not be looked at.\n\n")
		}
	}
	return b.String()
}

// chaosInvariantTable is what this project's own rules about its own data said
// either side of the fault.
//
// Empty for a manifest that declares no invariants, which is most of them, so
// nothing is added to a report that has nothing to add. Both sides are in the
// table rather than the after side alone, because a reader who sees only the
// after column cannot tell a rule the fault broke from one that was already
// broken, and those are the two facts the whole arm exists to separate.
func chaosInvariantTable(f ChaosFault) string {
	if len(f.Invariants) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("| The project's own rules about its own data | Before the fault | After the recovery |\n| --- | --- | --- |\n")
	for _, i := range f.Invariants {
		name := fmt.Sprintf("`%s`", oneLine(i.Name))
		if i.Attributable() {
			name = fmt.Sprintf("**`%s`**", oneLine(i.Name))
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", name, i.BeforeSays(), i.AfterSays())
	}
	b.WriteString("\n")
	return b.String()
}

// UnreachableSays is how long the database did not answer, in the words
// every surface uses.
//
// "never" is said as a word. A database a pause froze, or one that came back
// between two probes, is not the same fact as one that was down for no time,
// and a zero beside a fault reads as the fault having done nothing.
func (rec *ChaosRecovery) UnreachableSays() string {
	if rec.ProbeIntervalMs <= 0 {
		// A report from an engine that did not probe. Its number is the old
		// one, timed through the settle, so it is labelled as that.
		return millis(rec.DowntimeMs) + ", timed from the fault to the first query after the settle, not probed"
	}
	every := millis(rec.ProbeIntervalMs)
	switch {
	case !rec.Unreachable:
		return "never: every query a probe sent every " + every + " from the fault onwards was answered"
	case !rec.Recovered:
		return fmt.Sprintf("at least %s, and it had not answered again when the probe stopped (probed every %s)",
			millis(rec.DowntimeMs), every)
	}
	return fmt.Sprintf("%s, probed every %s", millis(rec.DowntimeMs), every)
}

// crashCell says whether the database crashed, in the words the log used.
func crashCell(rec *ChaosRecovery) string {
	if !rec.Crashed {
		return "no, and the log carries no process killed by a signal"
	}
	return fmt.Sprintf("yes, a server process was killed by signal %d", rec.Signal)
}

// replayCell says how far replay reached.
func replayCell(rec *ChaosRecovery) string {
	if !rec.Replayed {
		return "no replay is recorded in the log"
	}
	return fmt.Sprintf("yes, from %s to %s", oneLine(rec.RedoStart), oneLine(rec.RedoEnd))
}

// agreeCell says whether the two independent counts matched, and whether
// amcheck vouched for the index.
//
// "yes" only when amcheck PASSED. It used to be "yes" whenever amcheck had
// answered anything at all, so an index amcheck could not verify, and one it
// reported a problem in, read as a clean one on the page a reviewer reads
// before merging. A count mismatch stays bold whatever amcheck said, because
// two scans disagreeing is corruption on its own.
func agreeCell(rec *ChaosRecovery) string {
	switch {
	case rec.Amcheck == "":
		return "not checked, because " + readBackUnfinished
	case rec.HeapRows != rec.IndexRows:
		return fmt.Sprintf("**no: the heap counted %d and the index counted %d**", rec.HeapRows, rec.IndexRows)
	case !rec.AmcheckPassed():
		return fmt.Sprintf("**not verified: both scans counted %d rows, and amcheck said: %s**",
			rec.HeapRows, oneLine(rec.Amcheck))
	}
	return fmt.Sprintf("yes, %d rows both ways, and amcheck found every row in the index", rec.HeapRows)
}

// orUnknown is a cluster state, or a word for not having read one.
func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unread"
	}
	return oneLine(s)
}

// millis renders a duration a reader can compare, from the milliseconds the
// report carries across its JSON boundary.
func millis(ms int64) string {
	if ms <= 0 {
		return "no measurable time"
	}
	return (time.Duration(ms) * time.Millisecond).Round(time.Millisecond).String()
}
