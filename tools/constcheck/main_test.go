package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var rules = map[string][]string{
	"DDL lint rules":      seventeen(),
	"detection analyzers": thirteen(),
	"fidelity states":     {"unmeasured", "absent", "refused", "substituted", "reproduced"},
}

func seventeen() []string {
	out := make([]string, 0, 17)
	for i := 0; i < 17; i++ {
		out = append(out, "rule_"+string(rune('a'+i)))
	}
	return out
}

func thirteen() []string {
	out := make([]string, 0, 13)
	for i := 0; i < 13; i++ {
		out = append(out, "analyzer_"+string(rune('a'+i)))
	}
	return out
}

func found(t *testing.T, body string) []finding {
	t.Helper()
	f, _ := checkCounts("f.md", body, rules)
	return f
}

// The defect this tool exists for, in the words it was actually written in.
func TestTheHistoricalDefect(t *testing.T) {
	for _, text := range []string{
		"| `migration_lint` | Any of the six migration lint rules. |",
		"Every migration finding, including the six lint rules, has a rule name.",
		"MigrationLint governs all six lint rules together.",
		"It diffs pg_stat_statements and applies six lint rules.",
	} {
		f := found(t, text)
		if len(f) != 1 {
			t.Fatalf("%q: got %d findings, want 1", text, len(f))
		}
		if !strings.Contains(f[0].why, "There are 17") {
			t.Fatalf("%q: %s", text, f[0].why)
		}
	}
}

// The regression for the bug that made this tool report success while missing
// half of what it was pointed at.
//
// A greedy quantifier for the words between a number and its noun consumed
// "analyzers read the" out of this sentence, leaving the noun outside the
// match. Two of the four wrong analyzer counts went unreported and the tool
// exited having said nothing about them, which is exactly what a working tool
// on a clean tree looks like. If this goes green while TestFillerWords stays
// green, the gap measurement has been replaced by something that only handles
// one of the two shapes.
func TestNounImmediatelyAfterTheCount(t *testing.T) {
	f := found(t, "Twelve analyzers read the repository and say what they assumed.")
	if len(f) != 1 {
		t.Fatalf("got %d findings, want 1: the noun directly after the count was missed", len(f))
	}
	if !strings.Contains(f[0].why, "There are 13") {
		t.Fatal(f[0].why)
	}
}

func TestFillerWordsBetweenCountAndNoun(t *testing.T) {
	if f := found(t, "Any of the six migration lint rules."); len(f) != 1 {
		t.Fatalf("got %d findings, want 1", len(f))
	}
	// Four words is past the limit, so this is a different sentence rather
	// than a longer noun phrase.
	if f := found(t, "Six of the ways a very short lint rules list can be read."); len(f) != 0 {
		t.Fatalf("got %d findings, want 0: %v", len(f), f[0].why)
	}
}

// The three correct comments that a rule keyed only on the count called wrong.
func TestCountInsideAHypotheticalIsNotAClaim(t *testing.T) {
	for _, text := range []string{
		"When two analyzers disagree, the stronger evidence wins.",
		"Questions are the things af init has to ask, because a finding was below the confidence threshold or two analyzers disagreed.",
		"It caches file contents so that ten analyzers reading package.json read the disk once.",
	} {
		if f := found(t, text); len(f) != 0 {
			t.Fatalf("%q: %s", text, f[0].why)
		}
	}
}

// The restraint that is the whole reason a gate like this stays believed.
func TestUnclaimedSubsetIsNotFlagged(t *testing.T) {
	for _, text := range []string{
		"The lint rules cover VACUUM FULL and CLUSTER, both of which rewrite the table offline.",
		"A lint rule that says unsafe and stops is a lint people disable.",
		"Two of those rules are worth reading before you write any code.",
	} {
		if f := found(t, text); len(f) != 0 {
			t.Fatalf("%q: %s", text, f[0].why)
		}
	}
}

// A number joined to the token before it by a hyphen is part of that token.
//
// Found by declaring a set whose noun appears in markup, which none of the
// earlier sets did: a class attribute puts several Tailwind classes within
// three words of any noun that follows, and this exact string was read as a
// claim that there are three verdicts.
func TestHyphenatedNumberIsNotACount(t *testing.T) {
	body := `<span className="gap-6 text-black/35"> lint rules</span>`
	if f := found(t, body); len(f) != 0 {
		t.Fatalf("a Tailwind class was read as a count: %s", f[0].why)
	}
	// The same number standing on its own is still a count.
	if f := found(t, "There are 3 lint rules."); len(f) != 1 {
		t.Fatalf("got %d findings, want 1: the hyphen guard swallowed a real count", len(f))
	}
}

func TestCorrectCountIsSilent(t *testing.T) {
	for _, text := range []string{
		"Any of the seventeen migration lint rules.",
		"Thirteen analyzers read the repository.",
		"17 lint rules run against every migration.",
	} {
		if f := found(t, text); len(f) != 0 {
			t.Fatalf("%q: %s", text, f[0].why)
		}
	}
}

// The number has to count the noun, not merely appear beside it.
func TestNounMustBeWhatIsCounted(t *testing.T) {
	for _, text := range []string{
		"Six tables were rewritten, and the lint rules ran against all of them.",
		"Six statements, then the lint rules report what they found.",
	} {
		if f := found(t, text); len(f) != 0 {
			t.Fatalf("%q: %s", text, f[0].why)
		}
	}
}

// A wrapped paragraph puts the count on one line and the noun on the next.
func TestClaimWrappedAcrossLines(t *testing.T) {
	f := found(t, "the lock a migration held, what Postgres rewrote, the six lint\nrules, the plans that changed and the query counts")
	if len(f) != 1 {
		t.Fatalf("got %d findings, want 1: a wrapped claim was not seen as one claim", len(f))
	}
	if f[0].line != 1 {
		t.Fatalf("line %d, want the line the count is on", f[0].line)
	}
}

// Rule 2 on an identifier column says which member is missing.
func TestTableNamesTheMissingMember(t *testing.T) {
	rows := [][]string{{"`absent`", "x"}, {"`refused`", "x"}, {"`substituted`", "x"}, {"`reproduced`", "x"}}
	f, ok := checkTable("inventory.md", "fidelity states", rows, rules["fidelity states"])
	if !ok {
		t.Fatal("a table missing a member was accepted")
	}
	if !strings.Contains(f.why, "unmeasured") {
		t.Fatalf("did not name the missing member: %s", f.why)
	}
}

func TestCompleteIdentifierTablePasses(t *testing.T) {
	var rows [][]string
	for _, m := range rules["fidelity states"] {
		rows = append(rows, []string{"`" + m + "`", "x"})
	}
	if _, ok := checkTable("inventory.md", "fidelity states", rows, rules["fidelity states"]); ok {
		t.Fatal("a complete table was rejected")
	}
}

// The lint rules table names each rule in English rather than by identifier,
// so only the row count can be compared. That half has to work, because it is
// the only thing standing between an eighteenth rule and a page that
// documents seventeen.
func TestEnglishTableComparesRowCount(t *testing.T) {
	var rows [][]string
	for i := 0; i < 16; i++ {
		rows = append(rows, []string{"**Some rule in prose**", "why", "fix"})
	}
	f, ok := checkTable("insights.md", "DDL lint rules", rows, rules["DDL lint rules"])
	if !ok {
		t.Fatal("a table one row short of the set was accepted")
	}
	if !strings.Contains(f.why, "16 rows for 17 members") {
		t.Fatalf("%s", f.why)
	}
	rows = append(rows, []string{"**One more**", "why", "fix"})
	if _, ok := checkTable("insights.md", "DDL lint rules", rows, rules["DDL lint rules"]); ok {
		t.Fatal("a complete table was rejected")
	}
}

func TestTableUnderHeading(t *testing.T) {
	body := strings.Join([]string{
		"## Before", "| A | B |", "| --- | --- |", "| ignore | me |", "",
		"## The states", "Some prose introducing it.", "",
		"| State | Means |", "| --- | --- |", "| `a` | x |", "| `b` | y |", "",
		"## After", "| A | B |", "| --- | --- |", "| ignore | me |",
	}, "\n")
	rows, err := tableUnder(body, "The states")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: %v", len(rows), rows)
	}
	if rows[0][0] != "`a`" {
		t.Fatalf("first row %q, want the first member and not the header", rows[0][0])
	}
}

// A renamed heading has to stop the run. Returning no rows would make rule 2 a
// check that reads nothing and passes, which is the failure this whole tool is
// about one level up.
func TestMissingHeadingIsAnError(t *testing.T) {
	if _, err := tableUnder("## Something else\n\n| A |\n| --- |\n| x |\n", "The states"); err == nil {
		t.Fatal("a missing heading was not an error")
	}
	if _, err := tableUnder("## The states\n\nProse and no table at all.\n", "The states"); err == nil {
		t.Fatal("a missing table was not an error")
	}
}

func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "x.go")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// Go carries the type only on the first spec of a const run. An untyped
// constant in the middle would be dropped by a reader that looked at each spec
// alone, and the count would come out short, which is the error this tool
// reports rather than makes.
func TestConstRunCarriesTheTypeForward(t *testing.T) {
	p := write(t, `package x
type Rule string
const (
	A Rule = "a"
	B      = "b"
	C      = "c"
)
type Other string
const D Other = "d"
`)
	got, err := goSet(p, constType, "Rule")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("got %v, want a b c", got)
	}
}

func TestSliceFuncAndRegistries(t *testing.T) {
	p := write(t, `package x
func DefaultAnalyzers() []Analyzer {
	return []Analyzer{&One{}, &Two{}, &Three{}}
}
var things = []Thing{{Name: "alpha"}, {Name: "beta"}, {Name: "gamma"}}
var reg = map[string]P{"stripe": {}, "github": {}, "resend": {}}
`)
	got, err := goSet(p, sliceFunc, "DefaultAnalyzers")
	if err != nil || len(got) != 3 || got[0] != "One" {
		t.Fatalf("slice func: %v %v", got, err)
	}
	got, err = goSet(p, sliceVar, "things")
	if err != nil || strings.Join(got, ",") != "alpha,beta,gamma" {
		t.Fatalf("slice var: %v %v", got, err)
	}
	got, err = goSet(p, mapVar, "reg")
	if err != nil || len(got) != 3 {
		t.Fatalf("map var: %v %v", got, err)
	}
}

func TestARenamedSymbolIsAnError(t *testing.T) {
	p := write(t, "package x\ntype Rule string\nconst A Rule = \"a\"\n")
	if _, err := goSet(p, constType, "Renamed"); err == nil {
		t.Fatal("a symbol that is not there was not an error")
	}
}

// The guard against this tool going quiet. Every declaration has to resolve
// against the real repository and come back with the size the code has, so a
// package move or a rename fails the build instead of silently reducing this
// gate to nothing.
func TestEveryDeclaredSetResolves(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, s := range sets {
		got, err := goSet(filepath.Join(root, s.file), s.kind, s.symbol)
		if err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if len(got) < 3 {
			t.Fatalf("%s: %d members", s.name, len(got))
		}
		if s.noun == nil {
			t.Fatalf("%s: no noun", s.name)
		}
	}
	if len(sets) < 10 {
		t.Fatalf("%d sets declared; sets were removed rather than fixed", len(sets))
	}
}

// The lint rules are the reason this exists, so their real size is asserted
// here rather than only read. A test that reads the same file the tool reads
// proves they agree and nothing else.
func TestLintRulesAreSeventeen(t *testing.T) {
	got, err := goSet(filepath.Join("..", "..", "engine", "internal", "insights", "lint.go"), constType, "Rule")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 17 {
		t.Fatalf("%d lint rules, want 17. If a rule was added, the documentation "+
			"and this number both need it", len(got))
	}
}

// run has to say what it examined. Zero findings and a green exit are what a
// check that has stopped reaching its subject looks like, and the only thing
// that separates the two is the tool saying what it read.
func TestRunReportsWhatItExamined(t *testing.T) {
	var out bytes.Buffer
	_ = run(filepath.Join("..", ".."), &out)
	got := out.String()
	for _, want := range []string{
		"closed sets read from Go source",
		"sentences stating a count considered",
		"reference tables checked row for row",
		// Silence is not coverage. Nineteen oracle kinds that nothing
		// enumerates is a fine reason not to check them, and it has to be
		// visible as a decision rather than absent as an oversight.
		"deliberately not checked",
		"already gated elsewhere",
		// The anti-vacuum property, stated where a reader of the output sees
		// it rather than only in a comment they would have to go and find.
		"fails this command rather than being skipped",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output does not say %q:\n%s", want, got)
		}
	}
	if m := regexp.MustCompile(`(\d+) sentences stating a count considered`).FindStringSubmatch(got); m == nil || m[1] == "0" {
		t.Fatalf("no counted sentences were considered at all:\n%s", got)
	}
	if m := regexp.MustCompile(`(\d+) reference tables checked`).FindStringSubmatch(got); m == nil || m[1] == "0" {
		t.Fatalf("no reference tables were checked at all:\n%s", got)
	}
	if len(unchecked) == 0 {
		t.Fatal("the deliberately unchecked list is empty, so the output claims a decision it no longer records")
	}
}
