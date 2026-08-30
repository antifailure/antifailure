package insights

import (
	"fmt"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The rolling deploy check: does the PREVIOUS release still work against the
// schema this pull request produces.
//
// The rehearsal next door proves what a migration costs, which locks it takes
// and for how long, which tables it rewrites, which plans it changes. It does
// not prove the thing a deploy actually depends on. For the minutes between
// the migration applying and the last old instance shutting down, the previous
// release is talking to the migrated database, and nothing in this package
// tested that. RuleRenameColumnInUse says the sentence about rolling deploys
// and then leaves the reader to believe it.
//
// So this file describes the experiment and grades it, and internal/env runs
// it: build the previous commit's image, point it at a branch the pending
// migrations have already been applied to, and drive the workflows through it.
//
// Two decisions here decide whether the whole check is worth having.
//
// The first is that a failure caused by us is never reported as a failure of
// the application. An old image that will not build, a runner that will not
// start, a repository with no resolvable previous commit: every one of those
// is `blocked`, and blocked does not count. The first time this check reports
// "your migration breaks the previous release" because a Dockerfile changed
// its base image and the build failed, nobody believes it again.
//
// The second is the control run. A workflow that fails against the migrated
// branch has not proved anything on its own, because it may be a workflow that
// fails on the previous release for reasons that have nothing to do with the
// schema. So a failure is re-run against a branch of the SAME golden carrying
// the schema that release was deployed against. Passing there and failing here
// is the migration; the two runs differ in nothing else. Failing in both is
// `unverified` and is said so in those words. The control costs a second
// environment and it is only paid when something already failed, which is the
// case where being right matters and being cheap does not.

// RollingWhen decides when the check runs.
//
// One key rather than an `enabled` bool beside a `when`, because two ways to
// turn a check off is two things to read before you know whether it ran.
type RollingWhen string

const (
	// RollingRisky runs the check only when the pending migrations contain a
	// change the previous release can notice. It is the default.
	RollingRisky RollingWhen = "risky"
	// RollingAlways runs it for every migration, including a purely additive
	// one.
	RollingAlways RollingWhen = "always"
	// RollingNever turns it off.
	RollingNever RollingWhen = "never"
)

// DefaultRollingAgainst is how the previous release is chosen when the
// manifest says nothing.
//
// The invariant this check is about is a property of what is RUNNING in
// production during the deploy window, and no repository can tell us that. The
// merge base is the closest thing that is computable offline: it is the commit
// this branch was cut from, which under the continuous deployment most teams
// running a preview environment product are doing is a commit that was built,
// merged and deployed. `HEAD~1` is wrong on a branch with more than one commit
// and wrong again after a squash merge, and the last release tag is right for
// the minority who tag and wrong silently for everybody who does not.
//
// A team that deploys from tags overrides it, which is why the key exists.
const DefaultRollingAgainst = "merge-base"

// RollingConfig is the manifest's rolling_compatibility block, resolved.
type RollingConfig struct {
	// When decides whether the check runs at all.
	When RollingWhen
	// Against names the previous release. "merge-base", "previous-commit", or
	// any revision git can resolve, including a tag or a branch name.
	Against string
}

// ConfigureRolling resolves the block. A nil block is the default.
//
// Called from Configure rather than from a caller, so that "is this check on"
// is answered in the same one place every other check is answered in.
func ConfigureRolling(in *schema.RollingCompatibility) RollingConfig {
	c := RollingConfig{When: RollingRisky, Against: DefaultRollingAgainst}
	if in == nil {
		return c
	}
	if in.When != "" {
		c.When = RollingWhen(in.When)
	}
	if in.Against != "" {
		c.Against = in.Against
	}
	return c
}

// On reports whether the check runs at all, given what the migration does.
//
// The default is risky rather than always, and the reason is cost. This check
// builds a second image and brings up a second environment, which roughly
// doubles a run. A migration that only adds a nullable column, a table or an
// index cannot be noticed by code that never heard of it, so paying that on
// every additive migration would buy nothing and would get the check turned
// off by the first person who timed their pipeline. `always` is there for
// somebody who wants the guarantee unconditionally, including for a release
// with no migrations at all.
func (c RollingConfig) On(narrowing bool) bool {
	switch c.When {
	case RollingNever:
		return false
	case RollingAlways:
		return true
	default:
		return narrowing
	}
}

// ChangeKind names one thing a migration does that code written against the
// old schema can notice.
//
// Every kind here breaks something specific. A kind that does not is not on
// the list: CREATE TABLE, ADD COLUMN with a default or nullable, CREATE INDEX
// and CREATE VIEW are invisible to code that never heard of them, and a check
// that ran for those would double the cost of every additive migration in
// exchange for a result nobody could act on.
type ChangeKind string

const (
	// ChangeDropColumn is a column the previous release may still select.
	ChangeDropColumn ChangeKind = "drop_column"
	// ChangeRenameColumn removes the old name, which is the same break as a
	// drop with a friendlier looking statement.
	ChangeRenameColumn ChangeKind = "rename_column"
	// ChangeDropTable is a table the previous release may still read.
	ChangeDropTable ChangeKind = "drop_table"
	// ChangeRenameTable removes the old name.
	ChangeRenameTable ChangeKind = "rename_table"
	// ChangeDropView is a view the previous release may still read. Views are
	// usually there precisely because something reads them.
	ChangeDropView ChangeKind = "drop_view"
	// ChangeColumnType can refuse a value the previous release still writes,
	// and can hand back a value its driver cannot decode.
	ChangeColumnType ChangeKind = "change_column_type"
	// ChangeSetNotNull refuses an insert that omits the column, which every
	// insert in the previous release does if the column was nullable.
	ChangeSetNotNull ChangeKind = "set_not_null"
	// ChangeAddRequired is a new NOT NULL column with no default. The previous
	// release cannot know to supply it.
	ChangeAddRequired ChangeKind = "add_required_column"
	// ChangeDropDefault leaves an insert that relied on the default with
	// nothing to write.
	ChangeDropDefault ChangeKind = "drop_default"
	// ChangeAddConstraint can refuse a row the previous release still writes.
	ChangeAddConstraint ChangeKind = "add_constraint"
)

// SchemaChange is one narrowing change, located in the migration that made it.
type SchemaChange struct {
	Kind ChangeKind `json:"kind"`
	// Table is the relation, unqualified, so it compares against what the
	// server reports in an error message.
	Table string `json:"table"`
	// Column is empty for a change at table level.
	Column string `json:"column,omitempty"`
	// NewName is the name a rename moved to, so the report can suggest it.
	NewName string `json:"new_name,omitempty"`
	// Constraint is the constraint a statement added, when it named one.
	Constraint string `json:"constraint,omitempty"`
	// Migration and Statement locate it in the repository.
	Migration string `json:"migration,omitempty"`
	Statement string `json:"statement"`
}

// Object is what the change is about, as the server would name it.
func (c SchemaChange) Object() string {
	if c.Column == "" {
		return c.Table
	}
	return c.Table + "." + c.Column
}

// Breaks says, in one clause, what the previous release cannot do any more.
//
// The clause is the whole value of this check over a lint. A lint can say a
// rename is not backward compatible; only a run can say which release and
// which workflow, and the sentence has to be specific enough that somebody
// believes it without opening the migration.
func (c SchemaChange) Breaks() string {
	switch c.Kind {
	case ChangeDropColumn:
		return "this migration dropped " + c.Object()
	case ChangeRenameColumn:
		return "this migration renamed " + c.Object() + " to " + c.NewName
	case ChangeDropTable:
		return "this migration dropped the table " + c.Table
	case ChangeRenameTable:
		return "this migration renamed the table " + c.Table + " to " + c.NewName
	case ChangeDropView:
		return "this migration dropped the view " + c.Table
	case ChangeColumnType:
		return "this migration changed the type of " + c.Object()
	case ChangeSetNotNull:
		return "this migration made " + c.Object() + " NOT NULL, so an insert that omits it is refused"
	case ChangeAddRequired:
		return "this migration added " + c.Object() +
			" as NOT NULL with no default, so an insert that does not set it is refused"
	case ChangeDropDefault:
		return "this migration dropped the default on " + c.Object()
	case ChangeAddConstraint:
		if c.Constraint != "" {
			return "this migration added the constraint " + c.Constraint + " to " + c.Table
		}
		return "this migration added a constraint to " + c.Table
	default:
		return "this migration changed " + c.Object()
	}
}

// NarrowingChanges reads the statements a migration will run and reports every
// change the previous release can notice.
//
// It reads statements rather than diffing two schemas because a diff cannot
// tell a rename from a drop and an add, and the difference decides what the
// report is allowed to say. A rename has a new name to point at; a drop does
// not.
func NarrowingChanges(stmts []Statement) []SchemaChange {
	var out []SchemaChange
	for _, st := range stmts {
		out = append(out, narrowingIn(st)...)
	}
	return out
}

// Narrowing reports whether anything in these statements can break code
// written against the schema as it was.
func Narrowing(stmts []Statement) bool { return len(NarrowingChanges(stmts)) > 0 }

func narrowingIn(st Statement) []SchemaChange {
	upper := fold(st.SQL)
	at := func(kind ChangeKind, table, column, newName, constraint string) SchemaChange {
		return SchemaChange{
			Kind: kind, Table: table, Column: column, NewName: newName,
			Constraint: constraint, Migration: st.Migration, Statement: st.SQL,
		}
	}

	switch {
	case strings.HasPrefix(upper, "DROP TABLE"):
		return []SchemaChange{at(ChangeDropTable, identAfter(upper, "DROP TABLE"), "", "", "")}
	case strings.HasPrefix(upper, "DROP VIEW"), strings.HasPrefix(upper, "DROP MATERIALIZED VIEW"):
		name := identAfter(upper, "DROP VIEW")
		if name == "" {
			name = identAfter(upper, "DROP MATERIALIZED VIEW")
		}
		return []SchemaChange{at(ChangeDropView, name, "", "", "")}
	}

	if !strings.HasPrefix(upper, "ALTER TABLE") {
		return nil
	}
	table := identAfter(upper, "ALTER TABLE")

	// One ALTER TABLE can carry several actions, and a migration that drops
	// two columns in one statement has to report two changes or the second
	// one is invisible. The actions are separated by commas at depth zero,
	// which is the only place a comma can separate them: a type like
	// numeric(10,2) and a constraint like CHECK (a IN (1,2)) both put commas
	// inside parentheses.
	var out []SchemaChange
	for _, action := range splitActions(upper) {
		switch {
		case strings.HasPrefix(action, "DROP COLUMN"):
			out = append(out, at(ChangeDropColumn, table, identAfter(action, "DROP COLUMN"), "", ""))

		case strings.HasPrefix(action, "RENAME COLUMN"):
			old := identAfter(action, "RENAME COLUMN")
			out = append(out, at(ChangeRenameColumn, table, old, identAfter(action, " TO "), ""))

		case strings.HasPrefix(action, "RENAME TO"):
			out = append(out, at(ChangeRenameTable, table, "", identAfter(action, "RENAME TO"), ""))

		case strings.HasPrefix(action, "ADD COLUMN"), isBareAddColumn(action):
			column := identAfter(action, "ADD COLUMN")
			if column == "" {
				column = identAfter(action, "ADD")
			}
			if strings.Contains(action, "NOT NULL") && !strings.Contains(action, "DEFAULT") {
				out = append(out, at(ChangeAddRequired, table, column, "", ""))
			}

		case strings.HasPrefix(action, "ALTER COLUMN"), strings.HasPrefix(action, "ALTER "):
			column := identAfter(action, "ALTER COLUMN")
			if column == "" {
				column = identAfter(action, "ALTER")
			}
			switch {
			case strings.Contains(action, " TYPE "):
				out = append(out, at(ChangeColumnType, table, column, "", ""))
			case strings.Contains(action, "SET NOT NULL"):
				out = append(out, at(ChangeSetNotNull, table, column, "", ""))
			case strings.Contains(action, "DROP DEFAULT"):
				out = append(out, at(ChangeDropDefault, table, column, "", ""))
			}

		case strings.HasPrefix(action, "ADD CONSTRAINT"), strings.HasPrefix(action, "ADD CHECK"),
			strings.HasPrefix(action, "ADD UNIQUE"), strings.HasPrefix(action, "ADD PRIMARY KEY"),
			strings.HasPrefix(action, "ADD FOREIGN KEY"):
			// NOT VALID checks nothing that already exists, but it still
			// refuses a NEW row, and the previous release is what writes the
			// new rows during the window. So it counts.
			name := ""
			if strings.HasPrefix(action, "ADD CONSTRAINT") {
				name = identAfter(action, "ADD CONSTRAINT")
			}
			out = append(out, at(ChangeAddConstraint, table, "", "", name))
		}
	}
	return out
}

// isBareAddColumn recognises `ADD <name> <type>`, which Postgres accepts with
// the COLUMN keyword left out and which every tool that generates SQL by hand
// eventually emits.
func isBareAddColumn(action string) bool {
	if !strings.HasPrefix(action, "ADD ") {
		return false
	}
	rest := strings.Fields(action)[1:]
	if len(rest) == 0 {
		return false
	}
	switch rest[0] {
	case "COLUMN", "CONSTRAINT", "CHECK", "UNIQUE", "PRIMARY", "FOREIGN", "EXCLUDE":
		return false
	}
	return true
}

// splitActions cuts the action list of an ALTER TABLE on top level commas.
func splitActions(upperSQL string) []string {
	i := strings.Index(upperSQL, "ALTER TABLE")
	if i < 0 {
		return nil
	}
	rest := upperSQL[i+len("ALTER TABLE"):]
	// Past the table name and the noise words that can precede it.
	fields := strings.Fields(rest)
	skipped := 0
	for _, f := range fields {
		skipped++
		switch f {
		case "IF", "EXISTS", "ONLY":
			continue
		}
		break
	}
	rest = strings.Join(fields[skipped:], " ")

	var out []string
	depth, start := 0, 0
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(rest[start:i]))
				start = i + 1
			}
		}
	}
	if tail := strings.TrimSpace(rest[start:]); tail != "" {
		out = append(out, tail)
	}
	return out
}

// identAfter pulls the identifier following a keyword out of folded SQL,
// skipping the noise words that can sit between the two.
//
// It works on folded text on purpose. lint.go has its own helpers for this and
// they are deliberately not reused: that file is under active change by
// somebody else and a shared private helper is the kind of coupling that turns
// two independent edits into a conflict.
func identAfter(upperSQL, keyword string) string {
	i := strings.Index(upperSQL, keyword)
	if i < 0 {
		return ""
	}
	for _, word := range strings.Fields(upperSQL[i+len(keyword):]) {
		switch word {
		case "IF", "NOT", "EXISTS", "ONLY", "CONCURRENTLY", "COLUMN", "TABLE", "VIEW":
			continue
		}
		// Unqualified and unquoted, in that order, because the schema
		// qualifier sits outside the quotes: public."orders" is bare
		// "orders" and only then orders.
		//
		// Lower cased at the end, including the inside of a quoted
		// identifier, which is not what Postgres does. Folding has already
		// destroyed the original case by the time this reads it, so
		// preserving it is not on offer; what matters is that the server's
		// own error messages are lower cased the same way before they are
		// compared, so "Orders" matches "Orders" on both sides.
		return strings.ToLower(unquote(bareTable(strings.TrimSuffix(word, ";"))))
	}
	return ""
}

// RollingVerdict is what the check concluded.
//
// The vocabulary is the run report's, and the two words that matter are the
// ones that do NOT count against the change. `blocked` is our failure: the
// image would not build, the previous commit could not be resolved, the runner
// did not start. `unverified` is a workflow that failed on the previous
// release with and without this branch's migrations, so it says nothing about
// them.
// Reporting either as `fail` would spend the whole credibility of the check on
// its first false positive.
type RollingVerdict string

const (
	RollingPass       RollingVerdict = "pass"
	RollingFail       RollingVerdict = "fail"
	RollingBlocked    RollingVerdict = "blocked"
	RollingUnverified RollingVerdict = "unverified"
	// RollingOff is the check not running, either because the manifest said so
	// or because the migrations contain nothing the previous release could
	// notice. It is named rather than omitted, for the reason every other
	// check in this package is: a report that silently leaves a check out
	// reads exactly like a check that found nothing.
	RollingOff RollingVerdict = "off"
)

// RollingWorkflow is one workflow's answer.
type RollingWorkflow struct {
	Name string `json:"name"`
	// Verdict is pass, fail, blocked or unverified for this workflow alone.
	Verdict RollingVerdict `json:"verdict"`
	// Migrated is what the runner said against the migrated branch, in the
	// runner's own vocabulary, so a reader can see the raw result as well as
	// our reading of it.
	Migrated string `json:"migrated"`
	// Control is what the runner said against the same golden with the
	// schema that release was deployed against, and is empty when no control
	// was needed.
	Control string `json:"control,omitempty"`
	// Detail is the runner's explanation of the failure.
	Detail string `json:"detail,omitempty"`
	// Cause names the schema change that explains it, when one was found in
	// the previous release's own output. Empty means no claim is being made.
	Cause *Incompatibility `json:"cause,omitempty"`
}

// Incompatibility is a Postgres error the previous release printed, matched to
// the migration statement that caused it.
//
// Both halves are required. An error naming an object no statement in this
// migration touched is not attributed to the migration, and a statement with
// no error against it is not reported as the cause of anything. The product of
// those two rules is that every sentence this check prints is one it can show
// the evidence for.
type Incompatibility struct {
	// Object is what the server named, as "table.column" or "table".
	Object string `json:"object"`
	// Change is the migration statement that removed or narrowed it.
	Change SchemaChange `json:"change"`
	// Evidence is the line the previous release printed, verbatim.
	Evidence string `json:"evidence"`
}

// Sentence is the finding in one line.
//
// One line, with the release, the object and what happened to it, because that
// sentence is the whole value of this check over a lint. A lint can say a
// rename is not backward compatible; only a run can say which release, which
// object and which workflow, and it has to be specific enough that somebody
// believes it without opening the migration.
func (i Incompatibility) Sentence(release string) string {
	if release == "" {
		release = "the previous release"
	}
	c := i.Change
	switch c.Kind {
	case ChangeDropColumn, ChangeDropTable, ChangeDropView:
		return release + " still reads " + i.Object + ", which this migration dropped."
	case ChangeRenameColumn, ChangeRenameTable:
		return release + " still reads " + i.Object +
			", which this migration renamed to " + c.NewName + "."
	case ChangeColumnType:
		return release + " still uses " + i.Object +
			", whose type this migration changed."
	case ChangeSetNotNull:
		return release + " still writes " + i.Object +
			", which this migration made NOT NULL."
	case ChangeAddRequired:
		return release + " still writes rows without " + i.Object +
			", which this migration added as NOT NULL with no default."
	case ChangeDropDefault:
		return release + " still relies on the default on " + i.Object +
			", which this migration dropped."
	case ChangeAddConstraint:
		if c.Constraint != "" {
			return release + " still writes rows " + c.Constraint + " refuses, and this " +
				"migration added that constraint to " + c.Table + "."
		}
		return release + " still writes rows a constraint this migration added to " +
			c.Table + " refuses."
	default:
		return release + " still uses " + i.Object + ", and " + c.Breaks() + "."
	}
}

// Rolling is the whole check.
type Rolling struct {
	Verdict RollingVerdict `json:"verdict"`
	// Reason explains a verdict of off or blocked, and is empty otherwise.
	Reason string `json:"reason,omitempty"`
	// Against is the commit the previous release was taken from, and How says
	// which question that answers, because merge base and last tag are
	// different questions with different answers.
	Against string `json:"against,omitempty"`
	How     string `json:"how,omitempty"`
	// Changes are the narrowing changes this migration makes.
	Changes []SchemaChange `json:"changes,omitempty"`
	// Workflows is what each one did.
	Workflows []RollingWorkflow `json:"workflows,omitempty"`
	// Findings are the Postgres errors the previous release printed that name
	// an object this migration changed. Held for the whole run rather than per
	// workflow, because the previous release's output is a single stream and
	// there is nothing in it that says which workflow was driving when a line
	// was written.
	Findings []Incompatibility `json:"findings,omitempty"`
	// Unattributed are Postgres errors the previous release printed that name
	// something this migration did not touch. Reported without a claim
	// attached, because they are usually the real cause of an unverified
	// workflow and hiding them would waste the run.
	Unattributed []string `json:"unattributed,omitempty"`
	// Missing says what could not be measured, and why.
	Missing []string `json:"missing,omitempty"`
}

// Failed reports whether the previous release provably breaks.
func (r *Rolling) Failed() bool { return r != nil && r.Verdict == RollingFail }

// RunnerOutcome is one workflow as the runner reported it.
type RunnerOutcome struct {
	Name string
	// Verdict is the runner's word: pass, fail, flaky, blocked or unverified.
	Verdict string
	Detail  string
}

// NeedsControl reports the workflows whose failure has to be re-run against the
// schema that release was deployed against before it can be believed.
//
// Only failures. A workflow that passed needs no alibi, and paying for a
// second environment to confirm a pass is how a check that usually passes
// becomes a check people turn off.
func NeedsControl(migrated []RunnerOutcome) []string {
	var out []string
	for _, m := range migrated {
		if m.Verdict == "fail" {
			out = append(out, m.Name)
		}
	}
	return out
}

// GradeRolling turns the two runs and the previous release's own output into a
// verdict.
//
// control is keyed by workflow name and holds only the workflows that were
// re-run. A failure with no control entry is unverified rather than failed:
// without the control there is no evidence that the migration is what changed.
func GradeRolling(
	migrated []RunnerOutcome, control map[string]RunnerOutcome,
	changes []SchemaChange, output []string, release string,
) Rolling {
	r := Rolling{Changes: changes}
	found, unattributed := Attribute(output, changes)
	r.Findings = found
	r.Unattributed = unattributed

	counts := map[RollingVerdict]int{}
	for _, m := range migrated {
		w := RollingWorkflow{Name: m.Name, Migrated: m.Verdict, Detail: m.Detail}
		switch m.Verdict {
		case "pass", "flaky":
			// A flaky workflow failed once and passed on retry. It passed
			// against the migrated schema, which is the question asked here,
			// and calling it a failure would import a different check's
			// problem into this one.
			w.Verdict = RollingPass
		case "fail":
			c, ran := control[m.Name]
			switch {
			case !ran:
				w.Verdict = RollingUnverified
				w.Detail = "this workflow failed and was not re-run against the schema " +
					"that release was deployed against, so nothing here shows the " +
					"migration is what changed"
			case c.Verdict == "pass" || c.Verdict == "flaky":
				w.Verdict = RollingFail
				w.Control = c.Verdict
				// Attached only when there is exactly one thing it could be.
				// The previous release's output is collected for the whole
				// run rather than per workflow, so with two findings and two
				// failures there is no evidence for which explains which, and
				// pairing them off would be a confident sentence about the
				// wrong one. Both findings are on the report either way.
				if len(found) == 1 {
					cause := found[0]
					w.Cause = &cause
				}
			default:
				w.Verdict = RollingUnverified
				w.Control = c.Verdict
				w.Detail = "this workflow does not pass on " + describeRelease(release) +
					" against the schema it was deployed against either, so it says " +
					"nothing about these migrations"
			}
		default:
			// blocked, unverified, or a word the runner grew that this build
			// does not know. Never counted against the change.
			w.Verdict = RollingBlocked
		}
		counts[w.Verdict]++
		r.Workflows = append(r.Workflows, w)
	}

	switch {
	case len(migrated) == 0:
		r.Verdict = RollingBlocked
		r.Reason = "no workflow ran against the previous release, so nothing was proved"
	case counts[RollingFail] > 0:
		r.Verdict = RollingFail
	case counts[RollingPass] > 0:
		// At least one workflow was exercised end to end against the migrated
		// schema and came back clean. Pass rather than amber: turning amber
		// because a second workflow could not be exercised would make this
		// check amber on most runs, and a check that is amber on most runs is
		// one nobody reads. The ones that proved nothing are named in the
		// report instead.
		r.Verdict = RollingPass
	case counts[RollingUnverified] > 0:
		r.Verdict = RollingUnverified
	default:
		r.Verdict = RollingBlocked
		r.Reason = "every workflow was blocked before it could exercise the previous release"
	}
	return r
}

func describeRelease(release string) string {
	if release == "" {
		return "the previous release"
	}
	return release
}

// Attribute matches Postgres errors in the previous release's output to the
// migration statements that caused them.
//
// The output is the previous release's own stdout and stderr, which is where a
// driver puts what the server said. That is deliberately a weaker source than
// the database's log and it is the one that exists: the server log of a
// branched container is not readable from here, and asking pg_stat_statements
// does not help because a statement that errors never gets an entry.
//
// What makes it trustworthy is not the source, it is the join. An error is
// only ever reported as a finding when the object it names is one of the
// objects this migration changed. Everything else comes back as unattributed,
// which is information without a claim on it.
func Attribute(output []string, changes []SchemaChange) ([]Incompatibility, []string) {
	byColumn := map[string][]SchemaChange{}
	byTable := map[string][]SchemaChange{}
	byConstraint := map[string]SchemaChange{}
	for _, c := range changes {
		if c.Column != "" {
			byColumn[c.Table+"."+c.Column] = append(byColumn[c.Table+"."+c.Column], c)
			byColumn[c.Column] = append(byColumn[c.Column], c)
		} else {
			byTable[c.Table] = append(byTable[c.Table], c)
		}
		if c.Constraint != "" {
			byConstraint[c.Constraint] = c
		}
	}

	seen := map[string]bool{}
	var found []Incompatibility
	var other []string
	for _, err := range scanPostgresErrors(output) {
		if seen[err.line] {
			continue
		}
		seen[err.line] = true

		match, ok := matchChange(err, byColumn, byTable, byConstraint)
		if !ok {
			other = append(other, err.line)
			continue
		}
		object := match.Object()
		if err.table != "" && err.column != "" {
			object = err.table + "." + err.column
		}
		found = append(found, Incompatibility{
			Object: object, Change: match, Evidence: err.line,
		})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Object < found[j].Object })
	return found, other
}

func matchChange(
	err pgError,
	byColumn map[string][]SchemaChange, byTable map[string][]SchemaChange,
	byConstraint map[string]SchemaChange,
) (SchemaChange, bool) {
	if err.constraint != "" {
		if c, ok := byConstraint[err.constraint]; ok {
			return c, true
		}
	}
	if err.column != "" {
		if err.table != "" {
			if list := byColumn[err.table+"."+err.column]; len(list) > 0 {
				return list[0], true
			}
			// The server named a table this migration did not touch. Matching
			// on the column alone here would attribute an error about
			// invoices.email to a migration that dropped users.email, which
			// is a confident sentence about the wrong thing.
			return SchemaChange{}, false
		}
		// No table in the message, which is what Postgres says for an
		// unqualified reference. One candidate is an answer; several is a
		// guess, and a guess is what this check exists not to print.
		if list := byColumn[err.column]; len(list) == 1 {
			return list[0], true
		}
		return SchemaChange{}, false
	}
	if err.table != "" {
		if list := byTable[err.table]; len(list) > 0 {
			return list[0], true
		}
	}
	return SchemaChange{}, false
}

// pgError is one Postgres error message, taken apart.
type pgError struct {
	line       string
	table      string
	column     string
	constraint string
}

// scanPostgresErrors finds the server's own messages in a service's output.
//
// The messages are matched by their English text rather than by SQLSTATE,
// because SQLSTATE is what a driver has and almost never what it prints. Every
// pattern here is a message Postgres composes itself, so the wording is fixed
// by the server rather than by the application, and an application that logs
// the error at all logs these words. An application that swallows the error
// entirely produces no evidence, which is reported as a failure with no cause
// rather than as a cause nobody can see.
func scanPostgresErrors(output []string) []pgError {
	var out []pgError
	for _, raw := range output {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)

		// column "x" of relation "y" does not exist
		// column "x" of relation "y" contains null values
		if col, rel, ok := twoQuoted(lower, `column "`, `" of relation "`, `"`); ok {
			out = append(out, pgError{line: line, table: bareTable(rel), column: col})
			continue
		}
		// null value in column "x" of relation "y" violates not-null constraint
		if col, rel, ok := twoQuoted(lower, `null value in column "`, `" of relation "`, `"`); ok {
			out = append(out, pgError{line: line, table: bareTable(rel), column: col})
			continue
		}
		// column "x" does not exist
		if col, ok := oneQuoted(lower, `column "`, `" does not exist`); ok {
			out = append(out, pgError{line: line, column: col})
			continue
		}
		// column x.y does not exist, which is what the server says when the
		// reference in the statement was qualified.
		if ref, ok := unquotedBetween(lower, "column ", " does not exist"); ok {
			table, column, qualified := strings.Cut(ref, ".")
			if qualified {
				out = append(out, pgError{line: line, table: bareTable(table), column: column})
			} else {
				out = append(out, pgError{line: line, column: ref})
			}
			continue
		}
		// relation "x" does not exist
		if rel, ok := oneQuoted(lower, `relation "`, `" does not exist`); ok {
			out = append(out, pgError{line: line, table: bareTable(rel)})
			continue
		}
		// new row for relation "x" violates check constraint "y"
		if rel, name, ok := twoQuoted(lower, `new row for relation "`, `" violates check constraint "`, `"`); ok {
			out = append(out, pgError{line: line, table: bareTable(rel), constraint: name})
			continue
		}
		// duplicate key value violates unique constraint "y"
		if name, ok := oneQuoted(lower, `violates unique constraint "`, `"`); ok {
			out = append(out, pgError{line: line, constraint: name})
			continue
		}
		// insert or update on table "x" violates foreign key constraint "y"
		if rel, name, ok := twoQuoted(lower, `on table "`, `" violates foreign key constraint "`, `"`); ok {
			out = append(out, pgError{line: line, table: bareTable(rel), constraint: name})
			continue
		}
	}
	return out
}

func oneQuoted(line, prefix, suffix string) (string, bool) {
	i := strings.Index(line, prefix)
	if i < 0 {
		return "", false
	}
	rest := line[i+len(prefix):]
	j := strings.Index(rest, suffix)
	if j <= 0 {
		return "", false
	}
	return rest[:j], true
}

func twoQuoted(line, prefix, middle, suffix string) (string, string, bool) {
	first, ok := oneQuoted(line, prefix, middle)
	if !ok {
		return "", "", false
	}
	i := strings.Index(line, prefix+first+middle)
	rest := line[i+len(prefix+first+middle):]
	j := strings.Index(rest, suffix)
	if j <= 0 {
		return "", "", false
	}
	return first, rest[:j], true
}

// unquotedBetween reads an unquoted identifier between two fixed phrases,
// refusing anything with a space in it so that a sentence fragment is not
// mistaken for a name.
func unquotedBetween(line, prefix, suffix string) (string, bool) {
	name, ok := oneQuoted(line, prefix, suffix)
	if !ok || name == "" {
		return "", false
	}
	if strings.ContainsAny(name, ` "'`) {
		return "", false
	}
	return name, true
}

// Explain renders the check for somebody with thirty seconds.
func (r *Rolling) Explain() string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("Rolling deploy compatibility:\n")

	switch r.Verdict {
	case RollingOff:
		fmt.Fprintf(&b, "  not run: %s\n\n", r.Reason)
		return b.String()
	case RollingBlocked:
		fmt.Fprintf(&b, "  could not be checked: %s\n", r.Reason)
		b.WriteString("  This is a fact about the check rather than about the change, so it\n" +
			"  does not count against it.\n\n")
		return b.String()
	}

	release := shortRef(r.Against)
	fmt.Fprintf(&b, "  the previous release is %s, %s\n", release, r.How)

	// Set by a failure that named no cause of its own, which is what decides
	// whether the two sections below are worth printing.
	unexplained := false

	for _, w := range r.Workflows {
		switch w.Verdict {
		case RollingPass:
			fmt.Fprintf(&b, "  pass        %s\n", w.Name)
		case RollingFail:
			fmt.Fprintf(&b, "  FAIL        %s\n", w.Name)
			b.WriteString("              It passes against a branch of the same golden carrying\n" +
				"              the schema that release was deployed against, so this\n" +
				"              branch's migrations are the difference.\n")
			switch {
			case w.Cause != nil:
				fmt.Fprintf(&b, "              %s\n", w.Cause.Sentence(release))
				fmt.Fprintf(&b, "              %s\n", w.Cause.Evidence)
				fmt.Fprintf(&b, "              %s\n", w.Cause.Change.Statement)
			case len(r.Findings) > 0:
				b.WriteString("              More than one thing this migration changed was reported\n" +
					"              by the previous release, so which of them explains this\n" +
					"              workflow is not established. They are listed below.\n")
			default:
				b.WriteString("              The previous release printed no Postgres error naming\n" +
					"              an object this migration changed, so which change it is has\n" +
					"              not been identified here.\n")
				if w.Detail != "" {
					fmt.Fprintf(&b, "              %s\n", w.Detail)
				}
			}
			unexplained = true
		case RollingUnverified:
			fmt.Fprintf(&b, "  unverified  %s\n", w.Name)
			if w.Detail != "" {
				fmt.Fprintf(&b, "              %s\n", w.Detail)
			}
		default:
			fmt.Fprintf(&b, "  blocked     %s\n", w.Name)
			if w.Detail != "" {
				fmt.Fprintf(&b, "              %s\n", w.Detail)
			}
		}
	}

	if unexplained && len(r.Findings) == 0 && len(r.Changes) > 0 {
		// No error to match, so the honest thing to offer is the list of what
		// this migration takes away. It is not a claim about the failure; it
		// is the shortlist somebody reading the failure would build by hand.
		b.WriteString("\n  What this migration takes away, none of which the previous release\n" +
			"  named in its own output:\n")
		for _, c := range r.Changes {
			fmt.Fprintf(&b, "    %s\n      %s\n", c.Breaks(), c.Statement)
		}
	}
	if len(r.Findings) > 0 && !attached(r.Workflows) {
		b.WriteString("\n  What the previous release said, matched to what this migration did:\n")
		for _, f := range r.Findings {
			fmt.Fprintf(&b, "    %s\n", f.Sentence(release))
			fmt.Fprintf(&b, "      %s\n", f.Evidence)
			fmt.Fprintf(&b, "      %s\n", f.Change.Statement)
		}
	}
	if r.Verdict == RollingFail {
		b.WriteString("\n  A rolling deploy runs both releases at once. Until the last old\n" +
			"  instance stops, the release above is talking to this schema.\n")
	}
	if len(r.Unattributed) > 0 {
		b.WriteString("\n  The previous release also reported these, which name nothing this\n" +
			"  migration changed, so they are not attributed to it:\n")
		for _, line := range r.Unattributed {
			fmt.Fprintf(&b, "    %s\n", line)
		}
	}
	for _, m := range r.Missing {
		fmt.Fprintf(&b, "  Not measured: %s\n", m)
	}
	b.WriteString("\n")
	return b.String()
}

// attached reports whether a finding was already printed against a workflow,
// so the run level list does not repeat it.
func attached(ws []RollingWorkflow) bool {
	for _, w := range ws {
		if w.Cause != nil {
			return true
		}
	}
	return false
}

// shortRef abbreviates a commit for display, leaving anything that is not a
// hexadecimal object name alone so a tag stays readable.
func shortRef(ref string) string {
	if len(ref) < 12 {
		return ref
	}
	for i := 0; i < len(ref); i++ {
		c := ref[i]
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return ref
		}
	}
	return ref[:12]
}
