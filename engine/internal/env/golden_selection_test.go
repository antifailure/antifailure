package env_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// One machine holds every project's goldens, so selection is a security
// boundary rather than a convenience.
//
// The Docker provider keeps goldens as images and a daemon is machine wide; a
// configured golden store is shared by a fleet on purpose. Whether an
// environment may branch a given golden is therefore a question that gets
// asked on every `af up`, and it has been answered wrongly twice.
//
// The first answer was "the newest verified one", and bringing the control
// plane up branched a golden an example had refreshed thirty seconds earlier.
// The second answer was "the newest verified one made under the same masking
// rules", which looks like a project key and is not one: a project with no
// masking.yaml declares no rules, and every project on a machine with no
// masking.yaml therefore declared the SAME rules and shared one pool.
//
// Reproduced with the released binary before any of this was written, in two
// ordinary Express repositories with a Dockerfile each and neither declaring a
// masking rule. acme-billing declared a production database and refreshed a
// golden from it. nova-shop declared none, said in its own generated manifest
// that "branches will start empty", and `af up` printed "branching the
// database from gv_20260901033741_74234e98" and brought up a database holding
// acme-billing's customers and regions tables with acme-billing's rows in them.
//
// These tests are about the identity, not about the loop. The loop was never
// the part that was wrong.

// project builds an orchestrator the way a repository on disk would, with an
// optional masking.yaml, so that the identity under test is computed from the
// same inputs the real command computes it from.
func project(t *testing.T, name string, mutate func(*schema.Manifest, string)) *env.Orchestrator {
	t.Helper()
	root := t.TempDir()
	m := &schema.Manifest{
		Name: name,
		Database: &schema.Database{
			Provider:     schema.DBDocker,
			Version:      17,
			URLEnv:       "DATABASE_URL",
			MaskingRules: "masking.yaml",
		},
	}
	if mutate != nil {
		mutate(m, root)
	}
	o, err := env.New(env.Options{
		Root: root, Manifest: m, Branch: "main", Clock: clock.New(),
	})
	require.NoError(t, err)
	return o
}

func identity(t *testing.T, o *env.Orchestrator) string {
	t.Helper()
	got, err := o.GoldenProvenanceForTest()
	require.NoError(t, err)
	require.NotEmpty(t, got, "an identity is never empty; the empty string is what a golden "+
		"that recorded nothing carries, and the two must never be equal")
	return got
}

func writeRules(t *testing.T, root, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, "masking.yaml"), []byte(body), 0o600))
}

// The reproduced defect, as a property.
//
// Two unrelated projects, neither with a masking.yaml, which is the ordinary
// state of a repository somebody ran `af init` in five minutes ago. Under the
// rules digest both hashed to 74234e98afe7498f and shared one pool. This is
// the assertion the old code could not have passed.
func TestGoldenIdentity_TwoProjectsWithNoMaskingRulesDoNotShareAGolden(t *testing.T) {
	acmeProject := project(t, "acme-billing", func(m *schema.Manifest, _ string) {
		m.Database.SourceURLEnv = "PROD_DATABASE_URL"
	})
	novaProject := project(t, "nova-shop", nil)
	acme, nova := identity(t, acmeProject), identity(t, novaProject)

	// The defect stated as a property, rather than described. The two projects
	// agree on the masking rules digest, because neither declares a rule and
	// the digest of no rules is one fixed value, and that is precisely why
	// that digest could never have been the key. They must not agree on the
	// identity that decides what may be branched.
	acmeRules, err := acmeProject.RulesDigestForTest()
	require.NoError(t, err)
	novaRules, err := novaProject.RulesDigestForTest()
	require.NoError(t, err)
	require.Equal(t, acmeRules, novaRules,
		"two projects with no masking.yaml were masked identically; if this ever "+
			"stops being true, the reason this fix exists has changed")
	require.NotEqual(t, acme, nova,
		"a project that declares no production database must not be able to branch "+
			"a golden another project copied out of one")

	// Selection agrees, which is the half that reaches a user. A golden made
	// for acme is refused for nova and counted as a refusal, which is what
	// lets the error say "there are goldens here and none of them are yours"
	// rather than "there are no goldens".
	got, refused := env.PickGoldenForTest([]provider.GoldenVersion{
		{ID: "gv_20260901033741_74234e98", Verified: true, Provenance: acme, RulesHash: acmeRules},
	}, nova)
	require.Empty(t, got)
	require.Equal(t, 1, refused)
}

// Two projects that differ in nothing except their name.
//
// Separated from the reproduced pair deliberately. In that pair one project
// declares a production database and the other does not, so the source alone
// separates them and the test would still pass with the project name taken out
// of the identity entirely. This one has no other difference to fall back on.
func TestGoldenIdentity_TwoProjectsAlikeInEverythingButTheirNameDoNotShare(t *testing.T) {
	one := identity(t, project(t, "checkout", nil))
	two := identity(t, project(t, "storefront", nil))
	require.NotEqual(t, one, two,
		"two fresh repositories with no production database and no masking rules "+
			"differ in nothing a golden records except whose they are")

	got, refused := env.PickGoldenForTest([]provider.GoldenVersion{
		{ID: "gv_1", Verified: true, Provenance: one},
	}, two)
	require.Empty(t, got)
	require.Equal(t, 1, refused)
}

// Identical masking rules are not identity either, and this is the case the
// previous fix would have looked correct against.
//
// Two projects that both mask email the same way are still two projects. The
// rules digest is equal by construction here, so nothing about masking can
// separate them and only the project can.
func TestGoldenIdentity_TwoProjectsWithIdenticalMaskingRulesDoNotShareAGolden(t *testing.T) {
	rules := "rules:\n  - table: public.users\n    column: email\n    transform: email\n"
	one := project(t, "acme-billing", func(_ *schema.Manifest, root string) {
		writeRules(t, root, rules)
	})
	two := project(t, "nova-shop", func(_ *schema.Manifest, root string) {
		writeRules(t, root, rules)
	})
	require.NotEqual(t, identity(t, one), identity(t, two),
		"two projects masking the same way are still two projects")
}

// The reuse that has to keep working, or the whole store is pointless.
//
// One project, two branches, and the identity is the same, so the second
// branch costs a branch rather than a full refresh of production. A fix that
// made every environment refresh would be routed around within a day.
func TestGoldenIdentity_OneProjectAcrossBranchesSharesOneGolden(t *testing.T) {
	root := t.TempDir()
	writeRules(t, root, "rules:\n  - table: public.users\n    column: email\n    transform: email\n")
	m := &schema.Manifest{
		Name: "acme-billing",
		Database: &schema.Database{
			Provider: schema.DBDocker, Version: 17,
			URLEnv: "DATABASE_URL", MaskingRules: "masking.yaml",
			SourceURLEnv: "PROD_DATABASE_URL",
		},
	}
	main, err := env.New(env.Options{Root: root, Manifest: m, Branch: "main", Clock: clock.New()})
	require.NoError(t, err)
	feature, err := env.New(env.Options{
		Root: root, Manifest: m, Branch: "feature/add-billing", Clock: clock.New(),
	})
	require.NoError(t, err)

	shared := identity(t, main)
	require.Equal(t, shared, identity(t, feature),
		"a second branch of one project must branch the golden the first one made")

	got, refused := env.PickGoldenForTest([]provider.GoldenVersion{
		{ID: "gv_1", Verified: true, Provenance: shared},
	}, identity(t, feature))
	require.Equal(t, "gv_1", got)
	require.Zero(t, refused)
}

// The identity does not depend on where the repository is checked out.
//
// This is the reason the repository root is not part of it, and it is the
// difference between a pool that matches and a pool that never does. CI checks
// out somewhere new on every run, a git worktree per branch is a different
// directory, and the machine that pulls a published golden has never seen the
// publisher's disk at all. Keying on the path would refuse the golden in every
// one of those cases and cost a full copy of production each time.
func TestGoldenIdentity_DoesNotDependOnTheCheckoutDirectory(t *testing.T) {
	rules := "rules:\n  - table: public.users\n    column: email\n    transform: email\n"
	laptop := project(t, "acme-billing", func(m *schema.Manifest, root string) {
		m.Database.SourceURLEnv = "PROD_DATABASE_URL"
		writeRules(t, root, rules)
	})
	runner := project(t, "acme-billing", func(m *schema.Manifest, root string) {
		m.Database.SourceURLEnv = "PROD_DATABASE_URL"
		writeRules(t, root, rules)
	})
	require.NotEqual(t, laptop, runner, "the two orchestrators are in different directories")
	require.Equal(t, identity(t, laptop), identity(t, runner),
		"a CI runner with a fresh checkout must be able to branch what the laptop published")
}

// A golden a test suite published is never selectable by a real project.
//
// This is not a hypothetical: the golden the released binary branched into an
// unrelated project came from this repository's own goldenstore fixture, whose
// manifest is named app-publisher and whose schema is two tables called
// customers and regions. The fixture and the project differ in the name and in
// whether a production database is declared at all, and either one alone is
// enough to refuse it.
func TestGoldenIdentity_ATestSuitesGoldenIsNotSelectableByARealProject(t *testing.T) {
	fixture := identity(t, project(t, "app-publisher", func(m *schema.Manifest, _ string) {
		m.Database.SourceURLEnv = "PROD_URL"
		follow := 1
		m.Database.Subset = &schema.Subset{
			Enabled: true, SeedTable: "regions", SeedWhere: "code = 'eu'",
			MaxRows: 1000, FollowDependents: &follow,
		}
	}))
	real := identity(t, project(t, "nova-shop", nil))

	got, refused := env.PickGoldenForTest([]provider.GoldenVersion{
		{ID: "gv_20260901033708_74234e98", Verified: true, Provenance: fixture},
	}, real)
	require.Empty(t, got, "a fixture's golden reached a real project's environment once already")
	require.Equal(t, 1, refused)
}

// Every part of the identity is load bearing, one mutation at a time.
//
// Written as a table because the failure this guards against is a component
// silently dropping out of the digest: a refactor that stops feeding the seed
// command in changes nothing visible, and quietly widens the pool by one axis.
// Each row is a manifest that differs from the base in exactly one way and
// must not share the base's golden.
func TestGoldenIdentity_EveryComponentSeparatesTwoProjects(t *testing.T) {
	base := func(m *schema.Manifest, _ string) {
		m.Database.SourceURLEnv = "PROD_DATABASE_URL"
		m.Database.Seed = "psql -f seed.sql"
		follow := 2
		m.Database.Subset = &schema.Subset{
			Enabled: true, SeedTable: "accounts", MaxRows: 500, FollowDependents: &follow,
		}
	}
	baseline := identity(t, project(t, "acme-billing", base))

	cases := map[string]func(*schema.Manifest, string){
		"a different project name": func(m *schema.Manifest, root string) {
			base(m, root)
			m.Name = "acme-invoicing"
		},
		"a different production variable": func(m *schema.Manifest, root string) {
			base(m, root)
			m.Database.SourceURLEnv = "OTHER_DATABASE_URL"
		},
		"no production variable at all": func(m *schema.Manifest, root string) {
			base(m, root)
			m.Database.SourceURLEnv = ""
		},
		"a different seed command": func(m *schema.Manifest, root string) {
			base(m, root)
			m.Database.Seed = "psql -f other.sql"
		},
		"masking rules where there were none": func(m *schema.Manifest, root string) {
			base(m, root)
			writeRules(t, root, "rules:\n  - table: public.users\n    column: email\n    transform: email\n")
		},
		"a different subset": func(m *schema.Manifest, root string) {
			base(m, root)
			m.Database.Subset.SeedTable = "organizations"
		},
		"no subset": func(m *schema.Manifest, root string) {
			base(m, root)
			m.Database.Subset.Enabled = false
		},
		"a different Postgres major": func(m *schema.Manifest, root string) {
			base(m, root)
			m.Database.Version = 16
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			require.NotEqual(t, baseline, identity(t, project(t, "acme-billing", mutate)),
				"this difference produces a different golden and must produce a different identity")
		})
	}
}

// Two manifests that describe the same work agree, so nothing forces a refresh
// for a change that is not one. A subset listing the same two virtual
// relationships in the other order asks for the same slice.
func TestGoldenIdentity_IsNotDisturbedByOrderingOrByADisabledBlock(t *testing.T) {
	follow := 1
	withVirtual := func(order []schema.VirtualRelationship) *env.Orchestrator {
		return project(t, "acme-billing", func(m *schema.Manifest, _ string) {
			m.Database.Subset = &schema.Subset{
				Enabled: true, SeedTable: "accounts", MaxRows: 10,
				FollowDependents: &follow, VirtualRelationships: order,
			}
		})
	}
	a := schema.VirtualRelationship{From: "public.a.id", To: "public.b.a_id"}
	b := schema.VirtualRelationship{From: "public.c.id", To: "public.d.c_id"}
	require.Equal(t,
		identity(t, withVirtual([]schema.VirtualRelationship{a, b})),
		identity(t, withVirtual([]schema.VirtualRelationship{b, a})),
		"reordering two lines of a manifest is not a change to what a golden holds")

	require.Equal(t,
		identity(t, project(t, "acme-billing", nil)),
		identity(t, project(t, "acme-billing", func(m *schema.Manifest, _ string) {
			m.Database.Subset = &schema.Subset{Enabled: false, SeedTable: "accounts"}
		})),
		"a subset block that is switched off produces the same golden as no block at all")
}

// An unverified version is never branched, whatever else it records. That rule
// predates this one and is the product's central promise.
func TestPickGolden_NeverBranchesAnUnverifiedVersion(t *testing.T) {
	goldens := []provider.GoldenVersion{
		{ID: "gv_new", Verified: false, Provenance: "gp1-mine"},
		{ID: "gv_old", Verified: true, Provenance: "gp1-mine"},
	}
	got, _ := env.PickGoldenForTest(goldens, "gp1-mine")
	require.Equal(t, "gv_old", got)
}

// Nothing usable, and the count says why.
//
// Six goldens in the store and none of them usable here is a different
// situation from an empty store, and the caller says something different about
// each. An unverified version is not a refusal about ownership and must not be
// counted as one, or the message tells somebody to go looking for a project
// that does not exist.
func TestPickGolden_SaysHowManyItRefused(t *testing.T) {
	goldens := []provider.GoldenVersion{
		{ID: "a", Verified: true, Provenance: "gp1-other1"},
		{ID: "b", Verified: true, Provenance: "gp1-other2"},
		{ID: "c", Verified: false, Provenance: "gp1-mine"},
	}
	got, refused := env.PickGoldenForTest(goldens, "gp1-mine")
	require.Empty(t, got)
	require.Equal(t, 2, refused, "an unverified version is not a refusal about ownership")
}

// A version that records nothing is refused.
//
// The first version of this accepted one, reasoning that a missing record
// should not break a machine that already had goldens on it. That reasoning
// was wrong and it cost a run: with the lenient rule in place, bringing the
// control plane up branched an empty golden the masking test suite had
// published minutes earlier. Every golden this engine publishes records its
// project, so one that records nothing came from somewhere else.
func TestPickGolden_RefusesAVersionThatRecordsNothing(t *testing.T) {
	got, refused := env.PickGoldenForTest(
		[]provider.GoldenVersion{{ID: "gv_unknown", Verified: true}}, "gp1-mine")
	require.Empty(t, got)
	require.Equal(t, 1, refused)

	// And it is refused even when the caller could not compute an identity of
	// its own, which is the shape a lenient comparison degrades into.
	got, refused = env.PickGoldenForTest(
		[]provider.GoldenVersion{{ID: "gv_unknown", Verified: true}}, "")
	require.Empty(t, got)
	require.Equal(t, 1, refused)
}

// The observed case, exactly: a newer golden from another project, and this
// project's own older one behind it.
func TestPickGolden_PrefersTheMatchOverANewerStranger(t *testing.T) {
	got, refused := env.PickGoldenForTest([]provider.GoldenVersion{
		{ID: "gv_20260901033741_74234e98", Verified: true, Provenance: "gp1-acme"},
		{ID: "gv_20260829163911_710c39a7", Verified: true, Provenance: "gp1-nova"},
	}, "gp1-nova")
	require.Equal(t, "gv_20260829163911_710c39a7", got)
	require.Equal(t, 1, refused)
}

// A published golden is checked before a byte of it is restored.
//
// A store is shared on purpose and `af golden pull` with no version named
// takes the newest object in it, so the attestation is where the claim about
// whose data this is has to live. An attestation carrying none reads as the
// empty string, which is never equal to a real identity.
func TestAttestedProvenance_ReadsTheClaimAndRefusesAnAbsentOne(t *testing.T) {
	require.Equal(t, "gp1-acme",
		env.AttestedProvenanceForTest(`{"rules_hash":"abc","provenance":"gp1-acme"}`))
	require.Empty(t, env.AttestedProvenanceForTest(`{"rules_hash":"abc"}`))
	require.Empty(t, env.AttestedProvenanceForTest(`not json at all`))

	mine := identity(t, project(t, "nova-shop", nil))
	require.NotEqual(t, mine, env.AttestedProvenanceForTest(`{"rules_hash":"abc"}`),
		"a golden published before this field existed is refused rather than assumed to be ours")
}
