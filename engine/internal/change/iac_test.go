package change_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// infraManifest is the manifest the infrastructure fixture belongs to: a web
// service, a worker, one workflow, one invariant, load turned off, and a
// database declared at Postgres 15 while the fixture moves production to 16.
//
// The version mismatch is the point rather than a detail of the fixture. It is
// the state a real repository is in for as long as it takes somebody to
// remember that the manifest holds a second copy of the number, and before this
// lane nothing anywhere said so.
func infraManifest() *schema.Manifest {
	m := billingManifest()
	m.Database = &schema.Database{MaskingRules: "masking.yaml", Version: 15}
	return m
}

// The defect this lane exists to close, asserted as behaviour rather than as a
// table lookup.
//
// Before: engine/internal/change/change.go mapped the infrastructure surface to
// nil, so a pull request that changed nothing but Terraform selected NO check,
// wrote environment=false to GITHUB_OUTPUT, and skipped the run entirely
// through the gate in action.yml. The product's central claim, that a change is
// rehearsed before it merges, did not cover changes to production's own shape.
func TestAnalyze_AnInfrastructureOnlyChangeSelectsChecksRatherThanNothing(t *testing.T) {
	p := change.Analyze(change.Options{
		Manifest: infraManifest(), Files: load(t, "infrastructure.diff"),
	})

	require.False(t, p.Everything,
		"every path in this diff is recognised as infrastructure, so the fail safe must not be what "+
			"is selecting these checks; if it fired, this test proves nothing about the coverage table")
	require.Empty(t, p.Unclassified)

	// Named one by one rather than counted, so that a future edit which drops
	// one of them cannot be absorbed by another arriving.
	assert.True(t, p.Selects(change.CheckEnvironment),
		"the environment is what stands in for the runtime this change describes")
	assert.True(t, p.Selects(change.CheckWorkflows),
		"the workflows drive the application inside that environment")
	assert.True(t, p.Selects(change.CheckMigration),
		"an added line moves the database engine version and another sets a server parameter")
	assert.True(t, p.Selects(change.CheckLoad),
		"an added line changes how many copies of the service there are")
	assert.True(t, p.Selects(change.CheckEgress),
		"an added line declares a security group rule")

	// The two nothing in this diff touches, so that "selects something" has not
	// quietly become "selects everything".
	assert.False(t, p.Selects(change.CheckInvariants),
		"no schema changed, so nothing is asked of the data")
	assert.False(t, p.Selects(change.CheckMasking),
		"the masking rules file is not in this diff")

	// And the end of the chain that made the old behaviour expensive: action.yml
	// gates the whole run on the environment check being both selected and
	// available, so this is the value that decides whether anything runs at all.
	var environment change.Selection
	for _, s := range p.Plan {
		if s.Check == change.CheckEnvironment {
			environment = s
		}
	}
	require.Equal(t, change.CheckEnvironment, environment.Check)
	assert.True(t, environment.Run(),
		"the published action skips the run unless this is true, which is why selecting nothing "+
			"was not merely a quieter report")
}

// Which version the rehearsal will actually run against, in every state the two
// numbers can be in.
//
// One of these four is the interesting one and the other three are what stop it
// being a rule that only ever says one thing. A report that said "the manifest
// declares a different version" whatever the manifest declared would be a
// sentence nobody could act on.
func TestAnalyze_SaysWhichDatabaseVersionTheRehearsalWillActuallyRunAgainst(t *testing.T) {
	file := []change.File{{
		Path: "infra/rds.tf", Status: change.StatusModified,
		AddedLines: []change.AddedLine{{N: 7, Text: `  engine_version = "16.1"`}},
	}}

	find := func(t *testing.T, m *schema.Manifest) change.Fact {
		t.Helper()
		p := change.Analyze(change.Options{Manifest: m, Files: file})
		for _, f := range p.Facts {
			if f.Rule == "content.iac_database_version" {
				return f
			}
		}
		t.Fatal("the database version rule did not fire on a line that sets engine_version")
		return change.Fact{}
	}

	t.Run("they disagree, which is the state worth a sentence", func(t *testing.T) {
		f := find(t, infraManifest())
		assert.Equal(t, "16", f.Subject, "the major, read out of 16.1")
		assert.Equal(t, 7, f.Line, "the line number in the new file, so a reviewer can open it")
		assert.Contains(t, f.Evidence, "the manifest declares 15")
		assert.Contains(t, f.Evidence, "applies this change's migrations to 15 and not to 16")
		assert.Contains(t, f.Evidence, "not visible from a diff",
			"whether this is the database the manifest means is a guess, and has to read as one")
	})

	t.Run("they agree", func(t *testing.T) {
		m := infraManifest()
		m.Database.Version = 16
		f := find(t, m)
		assert.Contains(t, f.Evidence, "which is the version the manifest declares")
		assert.NotContains(t, f.Evidence, "and not to",
			"there is no disagreement to report, and reporting one would be a false alarm")
	})

	t.Run("the manifest declares no version", func(t *testing.T) {
		m := infraManifest()
		m.Database.Version = 0
		f := find(t, m)
		assert.Contains(t, f.Evidence, "the manifest declares no database version")
		// Which version an unset manifest actually gets is decided by
		// databaseVersion in internal/env. Naming a number here would be a
		// second copy of that constant, and the second copy is always the one
		// that goes stale.
		assert.NotContains(t, f.Evidence, "17",
			"the default belongs to internal/env, and a copy of it here would be a second answer")
	})

	t.Run("there is no manifest at all", func(t *testing.T) {
		f := find(t, nil)
		assert.Contains(t, f.Evidence, "the manifest declares no database version")
	})
}

// A server parameter is written with the setting on the left in one syntax and
// on the right in another, and Terraform's is the second one.
//
// A rule that read only keys would miss every AWS parameter group and every
// Cloud SQL database flag, which between them are how most people set a
// Postgres parameter, while still passing a test written against a
// postgresql.conf line.
func TestAnalyze_ReadsAServerParameterWrittenAsAValueAndAsAKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{"as a key, the postgresql.conf and Azure configuration shape", `  lock_timeout = "5000"`},
		{"as a value, the AWS parameter group and Cloud SQL flag shape", `    name  = "lock_timeout"`},
		{"as a value in YAML, the Kubernetes and Helm shape", `    - name: lock_timeout`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := change.Analyze(change.Options{
				Manifest: infraManifest(),
				Files: []change.File{{
					Path: "infra/db.tf", Status: change.StatusModified,
					AddedLines: []change.AddedLine{{N: 3, Text: tc.text}},
				}},
			})
			var got change.Fact
			for _, f := range p.Facts {
				if f.Rule == "content.iac_server_parameter" {
					got = f
				}
			}
			require.Equal(t, "content.iac_server_parameter", got.Rule,
				"nothing read %q as a server parameter", tc.text)
			assert.Equal(t, "lock_timeout", got.Subject)
			assert.True(t, p.Selects(change.CheckMigration),
				"the migration rehearsal is what runs a statement against the database this governs")
		})
	}
}

// The false alarms these rules were written to avoid, each one the reason a
// broader rule was refused.
//
// A rule that fires on everything selects everything, which is the fail safe
// wearing a rule's clothes: it costs a full run on every chart bump and it
// teaches a reader that the sentences under the plan mean nothing.
func TestAnalyze_DoesNotReadAVersionPinOrAChartAsADatabaseVersion(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{"a provider pin", `      version = "~> 5.0"`},
		{"a chart version", `version: 1.4.2`},
		{"a Kubernetes API version", `apiVersion: apps/v1`},
		{"an application image tag", `  image = "ghcr.io/org/api:1.9.0"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := change.Analyze(change.Options{
				Manifest: infraManifest(),
				Files: []change.File{{
					Path: "infra/chart.yaml", Status: change.StatusModified,
					AddedLines: []change.AddedLine{{N: 1, Text: tc.text}},
				}},
			})
			for _, f := range p.Facts {
				assert.NotEqual(t, "content.iac_database_version", f.Rule,
					"%q was read as a database engine version", tc.text)
			}
			assert.False(t, p.Selects(change.CheckMigration),
				"a version pin must not select the migration rehearsal")
		})
	}

	// The control. A rule that refused everything would pass every case above
	// while reading nothing, so the same file with a real engine version in it
	// has to still fire.
	p := change.Analyze(change.Options{
		Manifest: infraManifest(),
		Files: []change.File{{
			Path: "infra/chart.yaml", Status: change.StatusModified,
			AddedLines: []change.AddedLine{{N: 1, Text: `  engine_version = "16"`}},
		}},
	})
	assert.True(t, p.Selects(change.CheckMigration),
		"the rule refuses every shape above and has to still accept the real one")
}

// A resource named only in a trailing comment was not declared by this diff.
//
// This is written to the ONE path a comment can reach, which is not the one a
// first draft of this test guessed at. The assignment rules cannot see a
// comment at all: a line beginning with `#` or `//` has no key before its
// separator, so the pattern refuses it whether or not anything stripped the
// comment first, and a test built on those lines stays green with the stripping
// deleted. It measured nothing and said it had.
//
// The rule that CAN see into a comment is the resource token scan, because that
// one reads the whole line rather than an assignment: it is how
// `resource "aws_security_group_rule" "db" {` is recognised on a line that
// assigns nothing. So a trailing comment mentioning one is the case, and both
// comment syntaxes are their own arm.
func TestAnalyze_DoesNotReadAResourceNamedInATrailingComment(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{"a hash comment", `  name = "api" # replaced aws_security_group_rule last quarter`},
		{"a slash comment", `  name = "api" // replaced aws_security_group_rule last quarter`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := change.Analyze(change.Options{
				Manifest: infraManifest(),
				Files: []change.File{{
					Path: "infra/db.tf", Status: change.StatusModified,
					AddedLines: []change.AddedLine{{N: 1, Text: tc.text}},
				}},
			})
			for _, f := range p.Facts {
				assert.NotEqual(t, "content.iac_network_rule", f.Rule,
					"a resource named in a comment was read as a resource this diff declares: %s",
					f.Evidence)
			}
			assert.False(t, p.Selects(change.CheckEgress),
				"a comment must not select the egress check")
		})
	}

	// The control, so that a scan which had stopped reading lines entirely
	// would not pass the two arms above by finding nothing anywhere.
	p := change.Analyze(change.Options{
		Manifest: infraManifest(),
		Files: []change.File{{
			Path: "infra/db.tf", Status: change.StatusModified,
			AddedLines: []change.AddedLine{{N: 1, Text: `resource "aws_security_group_rule" "db" {`}},
		}},
	})
	assert.True(t, p.Selects(change.CheckEgress),
		"the same resource outside a comment has to still be read")
}

// A setting whose name appears only inside a comment or a description is not a
// setting this diff changed.
//
// Separate from the test above and specific about what refuses each line,
// because the three are not refused by the same thing. The two comment lines
// are refused TWICE over and either alone is enough: the comment stripping
// empties them, and the assignment pattern needs a key in front of the
// separator, which a line starting with `#` or `//` does not have. The
// description line reaches both rules intact and is refused by the value having
// to BE a parameter name rather than merely to contain one, which is the
// difference between reading a setting and reading a sentence about a setting.
func TestAnalyze_DoesNotReadASettingNamedInProse(t *testing.T) {
	p := change.Analyze(change.Options{
		Manifest: infraManifest(),
		Files: []change.File{{
			Path: "infra/db.tf", Status: change.StatusModified,
			AddedLines: []change.AddedLine{
				{N: 1, Text: `  # lock_timeout = "5000" was tried here and reverted`},
				{N: 2, Text: `  // desired_count = 9`},
				{N: 3, Text: `  description = "raise max_connections before the next sale"`},
			},
		}},
	})
	for _, f := range p.Facts {
		assert.NotContains(t, []string{"content.iac_server_parameter", "content.iac_capacity"}, f.Rule,
			"a setting named in prose was read as a setting that changed: %s", f.Evidence)
	}
	assert.False(t, p.Selects(change.CheckMigration))
	assert.False(t, p.Selects(change.CheckLoad))
}

// A checked in list of two hundred settings must not produce a profile longer
// than the diff it describes.
//
// THE FIXTURE IS THE WHOLE TEST, and the obvious one measures nothing. There
// are two guards on the cap and only one of them is the bound. The loop over
// the added lines stops once enough subjects exist, which is a short circuit:
// it saves reading the rest of a long file and it can never make the count
// exact, because it is checked BEFORE a line rather than between the subjects
// that line produces. The guard inside add is the one that decides the number.
//
// A file of one subject per line cannot tell them apart: the short circuit
// stops at exactly the cap and the real bound is never reached. So the last
// line here contributes TWO subjects at once, a resource type and an attribute
// on the same line, arriving when one slot is left. With the bound in place
// that line fills the slot and the second subject is refused. Without it the
// file produces one more than the cap, which is the defect, and a fixture of
// single subject lines reports the same number either way.
func TestAnalyze_BoundsHowManyInfrastructureSubjectsOneFileContributes(t *testing.T) {
	require.Greater(t, len(manyParameters), change.MaxIaCSubjectsPerFile,
		"the fixture has to offer more distinct subjects than the cap, or it cannot reach it")

	var lines []change.AddedLine
	// One short of the cap, each line a distinct server parameter.
	for i := 0; i < change.MaxIaCSubjectsPerFile-1; i++ {
		lines = append(lines, change.AddedLine{
			N: i + 1, Text: fmt.Sprintf(`  %s = "1"`, manyParameters[i]),
		})
	}
	// Two subjects on one line, with one slot left: the attribute from the
	// assignment rules and the resource type the value references, which the
	// token scan reads off the same line. Measured rather than assumed: the
	// first version of this line was a `resource "aws_security_group_rule"`
	// declaration, which produces ONE subject because it has no assignment on
	// it, and the cell aimed at this bound survived twice before that was
	// checked.
	lines = append(lines, change.AddedLine{
		N:    change.MaxIaCSubjectsPerFile,
		Text: `  cidr_blocks = [aws_security_group.api.id]`,
	})
	// And more afterwards, so that a cap which only stopped the last line would
	// still be caught.
	for i := change.MaxIaCSubjectsPerFile; i < len(manyParameters); i++ {
		lines = append(lines, change.AddedLine{
			N: i + 1, Text: fmt.Sprintf(`  %s = "1"`, manyParameters[i]),
		})
	}

	p := change.Analyze(change.Options{
		Manifest: infraManifest(),
		Files: []change.File{{
			Path: "infra/parameters.tf", Status: change.StatusModified, AddedLines: lines,
		}},
	})

	var subjects int
	for _, f := range p.Facts {
		if strings.HasPrefix(f.Rule, "content.iac_") {
			subjects++
		}
	}
	assert.Equal(t, change.MaxIaCSubjectsPerFile, subjects,
		"the cap is what stops a checked in list of settings producing a profile longer than the diff")

	// And the cap has not become a refusal: the file still says what it is, and
	// the checks its subjects select are still selected.
	assert.True(t, p.Selects(change.CheckMigration))
	assert.Contains(t, strings.Join(p.Blind, "\n"), "Nothing in a run applies infrastructure as code")
}

// manyParameters is more distinct server parameters than the cap allows, so the
// test above can reach it rather than run out of subjects first.
var manyParameters = []string{
	"lock_timeout", "statement_timeout", "idle_in_transaction_session_timeout",
	"deadlock_timeout", "max_connections", "max_locks_per_transaction",
	"max_pred_locks_per_transaction", "shared_buffers", "work_mem",
	"maintenance_work_mem", "effective_cache_size", "max_wal_size", "min_wal_size",
	"wal_level", "synchronous_commit", "default_transaction_isolation",
	"default_transaction_read_only", "random_page_cost", "autovacuum",
	"autovacuum_vacuum_scale_factor", "max_standby_streaming_delay",
	"max_worker_processes", "max_parallel_workers", "shared_preload_libraries",
	"log_min_duration_statement",
}

// The limit that survives the change, and has to, because the checks above it
// now say yes and a reader could take that for more than it is.
func TestAnalyze_StillSaysNothingAppliesYourInfrastructureAsCode(t *testing.T) {
	p := change.Analyze(change.Options{
		Manifest: infraManifest(), Files: load(t, "infrastructure.diff"),
	})
	blind := strings.Join(p.Blind, "\n")

	assert.Contains(t, blind, "Nothing in a run applies infrastructure as code",
		"selecting checks for an infrastructure change must not be readable as applying it")
	assert.Contains(t, blind, "stands in for what this change describes, and not the change itself")
	assert.Contains(t, blind, "4 infrastructure files changed",
		"the count is what tells a reader the sentence is about this diff rather than boilerplate")
}

// The example printed in the documentation is the real output.
//
// The page is where a customer decides whether to trust the plan, and a worked
// example there is the most quotable thing in it. A hand written one drifts the
// moment a sentence is reworded, and nothing would say so: the page would keep
// showing a plan the binary no longer produces.
func TestAnalyze_TheDocumentedInfrastructureExampleIsTheRealOutput(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)
	body, err := os.ReadFile(filepath.Join(root, "docs", "src", "content", "docs",
		"concepts", "change-analysis.md"))
	require.NoError(t, err, "the page that publishes this example could not be read, so this test "+
		"cannot say whether it agrees")

	documented := fencedBlockAfter(string(body), "## Infrastructure as code")
	require.NotEmpty(t, documented,
		"no fenced example was found under the infrastructure heading, and a test that quietly "+
			"found none would report agreement having compared nothing")

	p := change.Analyze(change.Options{
		Manifest: infraManifest(), Files: load(t, "infrastructure.diff"),
	})
	// Everything up to the "Why" section, which is the part the page shows.
	produced, _, found := strings.Cut(p.Explain(), "\nWhy\n")
	require.True(t, found, "Explain no longer has a Why section, so this comparison is reading the wrong part")

	assert.Equal(t, strings.TrimSpace(produced), strings.TrimSpace(documented),
		"the example on the concepts page is not what af change prints for "+
			"engine/internal/change/testdata/infrastructure.diff")
}

// fencedBlockAfter returns the contents of the first fenced code block that
// follows a heading.
func fencedBlockAfter(body, heading string) string {
	_, rest, found := strings.Cut(body, heading)
	if !found {
		return ""
	}
	_, rest, found = strings.Cut(rest, "```\n")
	if !found {
		return ""
	}
	block, _, found := strings.Cut(rest, "```")
	if !found {
		return ""
	}
	return block
}
