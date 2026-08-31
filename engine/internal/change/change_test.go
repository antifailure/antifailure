package change_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// billingManifest is the manifest the example diff belongs to: a web service
// and a worker built from two directories, one workflow, one invariant, and
// an egress policy that knows about Stripe and nothing else.
func billingManifest() *schema.Manifest {
	return &schema.Manifest{
		Version: 1, Name: "billing",
		Services: []schema.Service{
			{Name: "billing-api", Path: "api", Kind: schema.ServiceWeb, Port: 3000},
			{Name: "billing-worker", Path: "workers", Kind: schema.ServiceWorker},
		},
		Database: &schema.Database{MaskingRules: "masking.yaml"},
		Egress: &schema.Egress{
			Default: schema.ModeBlock,
			Rules: []schema.EgressRule{
				{Host: "api.stripe.com", Mode: schema.ModeSandbox},
			},
		},
		Workflows:  []schema.Workflow{{Name: "checkout", Description: "buy something"}},
		Invariants: []schema.Invariant{{Name: "no-orphans", SQL: "select 1 from t where false"}},
	}
}

func load(t *testing.T, name string) []change.File {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	files, truncated, err := change.ParseUnifiedDiff(strings.NewReader(string(body)))
	require.NoError(t, err)
	require.False(t, truncated, "the fixtures are small; a truncated one would make every assertion below meaningless")
	return files
}

func factsFor(p *change.Profile, path string) []change.Fact {
	var out []change.Fact
	for _, f := range p.Facts {
		if f.Path == path {
			out = append(out, f)
		}
	}
	return out
}

func surfaces(facts []change.Fact) []string {
	var out []string
	for _, f := range facts {
		s := string(f.Surface)
		if f.Subject != "" {
			s += ":" + f.Subject
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// The headline counts two different things and has to keep them apart in
// English as well as in arithmetic. Checks that will run is the promise;
// checks selected but not configured is the warning. "and N more" is only a
// sentence when there is something for them to be more than, so the case
// where nothing runs at all needs its own wording rather than the general
// one with a zero in it.
func TestHeadline_CountsWhatWillRunSeparatelyFromWhatIsNotConfigured(t *testing.T) {
	code := []change.File{{Path: "api/billing.ts", Status: change.StatusModified}}

	// Nothing configured at all: every selected check is a gap, and the
	// sentence must not claim anything is "more" than the nothing that runs.
	none := change.Analyze(change.Options{Files: code})
	assert.Contains(t, none.Headline(), "No check will run:")
	assert.NotContains(t, none.Headline(), "more",
		"nothing runs, so there is nothing for the unconfigured checks to be more than")

	// Fully configured: checks run and nothing is left unconfigured.
	full := billingManifest()
	full.Load = &schema.Load{Enabled: true}
	ok := change.Analyze(change.Options{Manifest: full, Files: code})
	assert.Contains(t, ok.Headline(), "checks will run.")
	assert.NotContains(t, ok.Headline(), "not configured")

	// The mixed case, which is the only one "more" belongs in: this manifest
	// runs the environment and the workflows, and has load turned off.
	mixed := change.Analyze(change.Options{Manifest: billingManifest(), Files: code})
	assert.Contains(t, mixed.Headline(), "checks will run, and 1 more is selected and not configured.")

	// And the headline never grades any of it.
	for _, h := range []string{none.Headline(), ok.Headline(), mixed.Headline()} {
		for _, word := range []string{"risk", "risky", "safe", "unsafe", "dangerous"} {
			assert.NotContains(t, strings.ToLower(h), word)
		}
	}
}

// rules returns the rule that produced each fact, sorted and deduplicated.
// Asserting on these rather than only on surfaces is what makes a
// classification auditable: two rules can assign the same surface, and a test
// that reads only the surface cannot tell which one fired.
func rules(facts []change.Fact) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range facts {
		if f.Rule == "" || seen[f.Rule] {
			continue
		}
		seen[f.Rule] = true
		out = append(out, f.Rule)
	}
	sort.Strings(out)
	return out
}

// A migration is not always a .sql file. Ruby, Python and Go migration tools
// all write code into a migrations directory, and the path rule is the only
// thing that recognises those; asserting the surface of a .sql migration
// alone would let path.sql quietly cover for path.migration being broken.
func TestAnalyze_RecognisesAMigrationThatIsNotASQLFile(t *testing.T) {
	for _, p := range []string{
		"db/migrate/20260824120000_add_billing_status.rb",
		"alembic/versions/9f2a_add_billing_status.py",
		"prisma/migrations/20260824_add_status/migration.sql",
	} {
		profile := change.Analyze(change.Options{
			Manifest: billingManifest(),
			Files:    []change.File{{Path: p, Status: change.StatusAdded}},
		})
		facts := factsFor(profile, p)
		require.NotEmpty(t, facts, "%s must be classified", p)
		assert.Equal(t, []string{"path.migration"}, rules(facts), "%s", p)
		assert.Contains(t, surfaces(facts), "schema", "%s", p)
		assert.True(t, profile.Selects(change.CheckMigration), "%s", p)
		assert.False(t, profile.Everything, "%s is recognised, so the fail safe must not fire", p)
	}
}

// The diff the marketing page has always shown: a migration, a service, a
// worker and an event type. This is the whole feature in one test.
func TestAnalyze_ClassifiesAMigrationAndAServiceDiff(t *testing.T) {
	p := change.Analyze(change.Options{
		Manifest: billingManifest(), Base: "main",
		Files: load(t, "migration-and-billing.diff"),
	})

	require.False(t, p.Everything, "every path in this diff is recognised, so the fail safe must not fire")
	require.Empty(t, p.Unclassified)

	assert.Equal(t, []string{"schema"},
		surfaces(factsFor(p, "migrations/20260824_add_billing_status.sql")))
	// Which rule claimed it, not merely what it was claimed as. This path is
	// both inside a migrations directory and a .sql file, so asserting only
	// the surface lets either rule cover for the other being broken.
	assert.Equal(t, []string{"path.migration"},
		rules(factsFor(p, "migrations/20260824_add_billing_status.sql")))
	assert.Equal(t, []string{"code", "egress:api.stripe.com", "egress:hooks.slack.com", "service:billing-api"},
		surfaces(factsFor(p, "api/billing.ts")))
	assert.Equal(t, []string{"code", "service:billing-worker"},
		surfaces(factsFor(p, "workers/billing.ts")))

	// events/ is not a declared service path, so the file is application
	// source attributed to nothing. Saying otherwise would be a guess.
	assert.Equal(t, []string{"code"}, surfaces(factsFor(p, "events/subscription.ts")))

	assert.True(t, p.Selects(change.CheckEnvironment))
	assert.True(t, p.Selects(change.CheckMigration))
	assert.True(t, p.Selects(change.CheckInvariants))
	assert.True(t, p.Selects(change.CheckWorkflows))
	assert.True(t, p.Selects(change.CheckEgress))
	assert.False(t, p.Selects(change.CheckMasking),
		"the masking rules file is not in this diff")
	// Selected and available are separate answers on purpose. A schema change
	// is something load exercises, so the diff selects it; this manifest has
	// load off, so it is not available, and the report says both.
	assert.True(t, p.Selects(change.CheckLoad))
	for _, s := range p.Plan {
		if s.Check == change.CheckLoad {
			assert.False(t, s.Available)
			assert.False(t, s.Run(), "a workflow step must not be told to run a check the manifest turned off")
		}
	}
}

// The egress fact is the one conclusion that comes from reading a line rather
// than a path, and it is decided by the same policy engine the sidecar uses.
func TestAnalyze_NamesOutboundHostsAndWhatTheFirewallWouldDo(t *testing.T) {
	p := change.Analyze(change.Options{
		Manifest: billingManifest(), Files: load(t, "migration-and-billing.diff"),
	})

	byHost := map[string]change.Fact{}
	for _, f := range p.Facts {
		if f.Surface == change.SurfaceEgress {
			byHost[f.Subject] = f
		}
	}
	require.Len(t, byHost, 2)

	stripe := byHost["api.stripe.com"]
	assert.Equal(t, "api/billing.ts", stripe.Path)
	assert.Equal(t, "content.outbound_host", stripe.Rule)
	assert.Contains(t, stripe.Evidence, "routes to mode sandbox")
	assert.Equal(t, 6, stripe.Line, "the line number is the one in the new file, so a reviewer can open it")

	slack := byHost["hooks.slack.com"]
	assert.Contains(t, slack.Evidence, "no egress rule matches")
	assert.Contains(t, slack.Evidence, "block")
}

// The fail safe. A path no rule recognises selects everything, and this test
// exists so that the next person to add a rule cannot quietly make it select
// nothing instead.
func TestAnalyze_AnUnrecognisedPathSelectsEveryCheck(t *testing.T) {
	p := change.Analyze(change.Options{
		Manifest: billingManifest(), Files: load(t, "unrecognised.diff"),
	})

	require.Equal(t, []string{"pipeline/model.qqq"}, p.Unclassified)
	require.True(t, p.Everything)
	for _, c := range change.Checks() {
		assert.Truef(t, p.Selects(c), "%s must be selected when a path is unrecognised", c)
	}
	assert.Contains(t, p.Selected(), change.CheckMasking)
}

// The same rule, reached the other two ways.
func TestAnalyze_AnEmptyOrTruncatedDiffSelectsEveryCheck(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts change.Options
	}{
		{"empty", change.Options{Manifest: billingManifest()}},
		{"truncated", change.Options{
			Manifest:  billingManifest(),
			Truncated: true,
			Files:     []change.File{{Path: "README.md", Status: change.StatusModified}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := change.Analyze(tc.opts)
			require.True(t, p.Everything)
			for _, c := range change.Checks() {
				assert.Truef(t, p.Selects(c), "%s must be selected", c)
			}
		})
	}
}

// A change to prose selects nothing, which is the saving the whole feature
// exists to make. It is also the assertion most likely to be quietly broken by
// a new rule, so it names the checks rather than counting them.
func TestAnalyze_ProseSelectsNoCheck(t *testing.T) {
	p := change.Analyze(change.Options{
		Manifest: billingManifest(), Files: load(t, "docs-only.diff"),
	})

	require.Empty(t, p.Unclassified)
	require.False(t, p.Everything)
	assert.Empty(t, p.Selected())
	assert.Contains(t, p.Headline(), "documentation")
}

// Deletions, renames and binary files are all things a diff shows and a
// classifier cannot follow, so each one has to appear in the blind spots.
func TestAnalyze_SaysWhatItCannotSee(t *testing.T) {
	p := change.Analyze(change.Options{
		Manifest: billingManifest(), Files: load(t, "rename-delete-binary.diff"),
	})
	blind := strings.Join(p.Blind, "\n")

	assert.Contains(t, blind, "deleted")
	assert.Contains(t, blind, "api/billing.ts became api/payments.ts",
		"where a rename came from is the one thing the new path cannot tell a reviewer")
	assert.Contains(t, blind, "binary")
	assert.Contains(t, blind, "infrastructure as code")
	assert.Contains(t, blind, "Nothing here says this change is safe")

	// The two unconditional sentences are the ones that must survive every
	// future edit to this list, so they are asserted by their substance
	// rather than left to the conditional sentences above to imply. The
	// first is the honest limit of reading a diff at all: size is not
	// danger, in either direction.
	assert.Contains(t, blind, "does not run the program",
		"the report has to say that this reads a diff rather than executing anything")
	assert.Contains(t, blind, "one line change to a configuration default",
		"a small diff being able to matter more than a large one is the single most important thing this cannot see")
	assert.Contains(t, blind, "thousand line refactor",
		"and the converse, so nobody reads a big diff as a big risk")

	// The rename is attributed to the service by its NEW path, and the test
	// says so out loud because a reader of the report has to know which.
	facts := factsFor(p, "api/payments.ts")
	require.NotEmpty(t, facts)
	assert.Equal(t, []string{"code", "service:billing-api"}, surfaces(facts))

	// The status reaches the report, so a row for a file that no longer
	// exists does not read as a row for a file that was edited.
	assert.Contains(t, p.Markdown(), "`docs/README.md` (deleted)")
	assert.Contains(t, p.Explain(), "api/payments.ts (renamed)")
}

// The added lines of a very large file are read only up to a limit, and the
// report says so rather than reporting no outbound host in it.
func TestAnalyze_SaysWhenAFileWasTooLongToRead(t *testing.T) {
	f := change.File{Path: "api/generated.go", Status: change.StatusModified, LinesTruncated: true}
	p := change.Analyze(change.Options{Manifest: billingManifest(), Files: []change.File{f}})
	assert.Contains(t, strings.Join(p.Blind, "\n"), "only the first 4000")
}

// A worker cannot be reached by a browser agent. The site's own example diff
// touches one, so the report has to say it rather than counting the worker as
// covered by the workflows.
func TestAnalyze_NamesAWorkerTheWorkflowsCannotReach(t *testing.T) {
	p := change.Analyze(change.Options{
		Manifest: billingManifest(), Files: load(t, "migration-and-billing.diff"),
	})
	assert.Contains(t, strings.Join(p.Blind, "\n"),
		"billing-worker is a worker")
}

// Selected and unavailable is the most useful line in the report: something
// changed and nothing is going to look at it.
func TestAnalyze_ReportsAGapWhenTheManifestCannotRunASelectedCheck(t *testing.T) {
	m := billingManifest()
	m.Invariants = nil
	m.Workflows = nil

	p := change.Analyze(change.Options{
		Manifest: m, Files: load(t, "migration-and-billing.diff"),
	})

	gaps := map[change.Check]string{}
	for _, g := range p.Gaps() {
		gaps[g.Check] = g.Unavailable
	}
	require.Contains(t, gaps, change.CheckInvariants)
	require.Contains(t, gaps, change.CheckWorkflows)
	assert.Contains(t, gaps[change.CheckInvariants], "declares no invariants")
	assert.Contains(t, p.Markdown(), "Selected and unavailable")
}

// With no manifest at all, nothing configurable is available. A profile that
// claimed otherwise would be inventing coverage.
func TestAnalyze_WithoutAManifestNothingIsAvailable(t *testing.T) {
	p := change.Analyze(change.Options{Files: load(t, "migration-and-billing.diff")})
	for _, s := range p.Plan {
		assert.Falsef(t, s.Available, "%s cannot be available with no manifest", s.Check)
		assert.NotEmpty(t, s.Unavailable)
	}
}

// Two runs over the same diff must produce byte identical output, whatever
// order the files arrived in. A report that shuffles cannot be diffed against
// the last one, and a reviewer who cannot diff it stops reading it.
func TestAnalyze_IsDeterministicWhateverOrderTheDiffArrivesIn(t *testing.T) {
	files := load(t, "migration-and-billing.diff")
	forward := change.Analyze(change.Options{Manifest: billingManifest(), Files: files})

	reversed := make([]change.File, len(files))
	for i := range files {
		reversed[i] = files[len(files)-1-i]
	}
	backward := change.Analyze(change.Options{Manifest: billingManifest(), Files: reversed})

	a, err := json.Marshal(forward)
	require.NoError(t, err)
	b, err := json.Marshal(backward)
	require.NoError(t, err)
	assert.Equal(t, string(a), string(b))
	assert.Equal(t, forward.Markdown(), backward.Markdown())

	// Equal is not enough on its own: two unsorted runs would also be equal.
	// The order itself is the guarantee the report and any diff of two
	// reports rest on, so it is asserted rather than inferred.
	assert.True(t, sort.SliceIsSorted(forward.Facts, func(i, j int) bool {
		a, b := forward.Facts[i], forward.Facts[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Subject < b.Subject
	}), "the facts are not in path, rule, line, subject order: %+v", forward.Facts)
}

// Every conclusion names the file and the rule behind it. A profile whose
// reasoning cannot be audited is astrology, and this is the check that keeps
// it from becoming one.
func TestAnalyze_EveryFactNamesAFileAndARuleAndASentence(t *testing.T) {
	for _, name := range []string{
		"migration-and-billing.diff", "docs-only.diff",
		"unrecognised.diff", "rename-delete-binary.diff",
	} {
		p := change.Analyze(change.Options{Manifest: billingManifest(), Files: load(t, name)})
		for _, f := range p.Facts {
			assert.NotEmptyf(t, f.Path, "%s: a fact with no file", name)
			assert.NotEmptyf(t, f.Rule, "%s: %s has no rule", name, f.Path)
			assert.NotEmptyf(t, f.Evidence, "%s: %s has no evidence", name, f.Path)
			assert.NotEmptyf(t, f.Surface, "%s: %s has no surface", name, f.Path)
		}
	}
}

// The design constraint, enforced. This package describes what a change
// touches; the moment it starts describing what a change IS, it is making a
// promise the product's own terms refuse to make.
func TestAnalyze_NeverGradesTheChange(t *testing.T) {
	// The two disclaimer sentences use the word safe on purpose, so the ban is
	// on the shapes that grade rather than on the word.
	banned := []string{
		"risky", "risk profile", "risk score", "safe to merge", "looks safe",
		"low risk", "high risk", "medium risk", "no risk", "dangerous", "severity",
	}
	for _, name := range []string{
		"migration-and-billing.diff", "docs-only.diff",
		"unrecognised.diff", "rename-delete-binary.diff",
	} {
		p := change.Analyze(change.Options{Manifest: billingManifest(), Files: load(t, name)})
		text := strings.ToLower(p.Headline() + "\n" + p.Markdown() + "\n" + p.Explain())
		for _, word := range banned {
			assert.NotContainsf(t, text, word,
				"%s: the output grades the change with %q", name, word)
		}
		assert.Containsf(t, text, "nothing here says this change is safe",
			"%s: the disclaimer that makes the rest of the output readable is missing", name)
	}
}

// The manifest's own rules teach the classifier a layout the built in rules
// cannot predict. Longest match wins, so order does not decide.
func TestAnalyze_ManifestRulesClassifyWhatTheBuiltInRulesCannot(t *testing.T) {
	m := billingManifest()
	m.Change = &schema.Change{Rules: []schema.ChangeRule{
		{Path: "pipeline/**", Surface: "code"},
		{Path: "pipeline/model.qqq", Surface: "config", Note: "the scoring model the api reads at boot"},
	}}

	p := change.Analyze(change.Options{Manifest: m, Files: load(t, "unrecognised.diff")})
	require.Empty(t, p.Unclassified, "a manifest rule claims it, so the fail safe must not fire")
	require.False(t, p.Everything)

	facts := factsFor(p, "pipeline/model.qqq")
	require.Len(t, facts, 1)
	assert.Equal(t, change.SurfaceConfig, facts[0].Surface,
		"the longer pattern wins regardless of the order the two are written in")
	assert.Equal(t, "manifest.change_rule", facts[0].Rule)
	assert.Equal(t, "the scoring model the api reads at boot", facts[0].Evidence)
}

// In a monorepo two services can both claim a file, and which one a report
// names decides whether a reader believes it. The longest declared path wins,
// and a genuine tie names both rather than picking one.
func TestAnalyze_AttributesAFileToTheServiceWithTheLongestDeclaredPath(t *testing.T) {
	t.Run("longest wins", func(t *testing.T) {
		m := &schema.Manifest{
			Version: 1, Name: "mono",
			Services: []schema.Service{
				{Name: "gateway", Path: ".", Kind: schema.ServiceWeb},
				{Name: "billing", Path: "services/billing", Kind: schema.ServiceWeb},
			},
		}
		p := change.Analyze(change.Options{Manifest: m, Files: []change.File{
			{Path: "services/billing/charge.go", Status: change.StatusModified},
			{Path: "cmd/main.go", Status: change.StatusModified},
		}})
		assert.Equal(t, []string{"code", "service:billing"},
			surfaces(factsFor(p, "services/billing/charge.go")))
		assert.Equal(t, []string{"code", "service:gateway"},
			surfaces(factsFor(p, "cmd/main.go")))
	})

	t.Run("a tie names both and says so", func(t *testing.T) {
		m := &schema.Manifest{
			Version: 1, Name: "mono",
			Services: []schema.Service{
				{Name: "api", Path: "app", Kind: schema.ServiceWeb},
				{Name: "admin", Path: "app", Kind: schema.ServiceWeb},
			},
		}
		p := change.Analyze(change.Options{Manifest: m, Files: []change.File{
			{Path: "app/server.go", Status: change.StatusModified},
		}})
		assert.Equal(t, []string{"code", "service:admin", "service:api"},
			surfaces(factsFor(p, "app/server.go")))
		assert.Contains(t, strings.Join(p.Blind, "\n"), "attributed to more than one service")
	})
}

// A manifest that reaches the analyser has been validated, so this should be
// unreachable. It is asserted anyway: the failure mode of dropping the engine
// silently is an analysis reporting no outbound hosts, which reads exactly
// like a change that calls nothing.
func TestAnalyze_SaysSoWhenTheEgressPolicyWillNotCompile(t *testing.T) {
	m := billingManifest()
	m.Egress = &schema.Egress{Rules: []schema.EgressRule{
		{Host: "api.*.stripe.com", Mode: schema.ModeAllow},
	}}

	p := change.Analyze(change.Options{
		Manifest: m, Files: load(t, "migration-and-billing.diff"),
	})
	assert.Contains(t, strings.Join(p.Blind, "\n"), "would not compile")
	for _, f := range p.Facts {
		assert.NotEqual(t, change.SurfaceEgress, f.Surface,
			"no host can have been decided, because there was nothing to decide with")
	}
}
