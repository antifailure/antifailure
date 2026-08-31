package fidelity_test

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/fidelity"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// full is an observation of an environment where everything worked, which the
// tests below then break one fact at a time. Written out rather than built by
// a helper chain so that what each test changes is visible in the test.
func full() fidelity.Observation {
	return fidelity.Observation{
		EnvID: "shop-main-abc123",
		Manifest: &schema.Manifest{
			Name: "shop",
			Services: []schema.Service{
				{Name: "web", Kind: schema.ServiceWeb},
				{Name: "worker", Kind: schema.ServiceWorker},
			},
			Database: &schema.Database{Provider: schema.DBDocker},
			Egress: &schema.Egress{
				Default: schema.ModeBlock,
				Rules: []schema.EgressRule{
					{Host: "api.stripe.com", Mode: schema.ModeMock},
					{Host: "api.resend.com", Mode: schema.ModeCapture},
				},
			},
			Personas: []schema.Persona{{Name: "buyer", Email: "buyer@example.test"}},
			Load:     &schema.Load{},
		},
		Running: []provider.RunningService{
			{Name: "web", Kind: "web", Ready: true, URL: "http://127.0.0.1:8080"},
			{Name: "worker", Kind: "worker", Ready: true},
		},
		Runtime:     "the local runtime, which publishes an address this machine can reach",
		Golden:      "gv_20260830120000_abcd1234",
		Attested:    true,
		Attestation: "41 columns read back over 82000 rows sampled",
		Tables:      12,
		Rows:        184000,
		Hosts: []fidelity.Host{
			{Name: "api.stripe.com", Mode: schema.ModeMock, Pack: "stripe", Stateful: true},
			{Name: "api.resend.com", Mode: schema.ModeCapture},
		},
		Personas: []fidelity.Persona{
			{Name: "buyer", Login: schema.LoginPassword, Present: true, Table: "public.users"},
		},
	}
}

func componentState(t *testing.T, inv fidelity.Inventory, dim schema.FidelityDimension, name string) fidelity.Component {
	t.Helper()
	d, ok := inv.Dimension(dim)
	require.True(t, ok, "dimension %s is missing from the inventory", dim)
	for _, c := range d.Components {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("component %q is missing from dimension %s", name, dim)
	return fidelity.Component{}
}

// The number is only worth having if it is the same number twice, and the
// order the orchestrator happened to see hosts in must not change it.
func TestTheSameObservationProducesTheSameInventoryAndScore(t *testing.T) {
	first, err := json.Marshal(fidelity.Build(full()))
	require.NoError(t, err)
	second, err := json.Marshal(fidelity.Build(full()))
	require.NoError(t, err)
	require.Equal(t, string(first), string(second))

	shuffled := full()
	shuffled.Hosts[0], shuffled.Hosts[1] = shuffled.Hosts[1], shuffled.Hosts[0]
	third, err := json.Marshal(fidelity.Build(shuffled))
	require.NoError(t, err)
	require.Equal(t, string(first), string(third),
		"the order the hosts were observed in changed the inventory")
}

func TestEveryDimensionIsPresentInEveryInventory(t *testing.T) {
	inv := fidelity.Build(fidelity.Observation{})
	require.Len(t, inv.Dimensions, len(schema.AllFidelityDimensions()))
	for i, name := range schema.AllFidelityDimensions() {
		require.Equal(t, name, inv.Dimensions[i].Name,
			"the dimensions are not in AllFidelityDimensions order")
	}
}

// The rule the whole package exists for: something nobody could measure is not
// a pass and is not a failure, and it is named.
func TestAnUnmeasuredComponentIsInNeitherHalfOfTheScore(t *testing.T) {
	obs := full()
	obs.Hosts[0] = fidelity.Host{
		Name: "api.stripe.com", Mode: schema.ModeSynth,
	}
	inv := fidelity.Build(obs)

	require.Equal(t, fidelity.Unmeasured,
		componentState(t, inv, schema.FidelityThirdParty, "api.stripe.com").State)

	score := inv.Score()
	withSynth, ok := score.Percent()
	require.True(t, ok)

	// The same environment with that host removed entirely scores the same,
	// which is what "excluded" has to mean.
	trimmed := full()
	trimmed.Hosts = trimmed.Hosts[1:]
	trimmed.Manifest.Egress.Rules = trimmed.Manifest.Egress.Rules[1:]
	without, ok := fidelity.Build(trimmed).Score().Percent()
	require.True(t, ok)
	require.Equal(t, without, withSynth)

	var named bool
	for _, e := range score.Excluded {
		if e.Component == "api.stripe.com" {
			named = true
			require.Contains(t, e.Because, "unverified")
		}
	}
	require.True(t, named, "the excluded component was not named with a reason")
}

func TestNothingMeasurableMeansNoScoreRatherThanZero(t *testing.T) {
	obs := full()
	obs.ServicesReason = "the runtime could not be reached"
	obs.BranchReason = "the branch could not be read"
	obs.GoldenReason = "the provider does not record which golden this branch came from"
	obs.PersonasReason = "the accounts could not be looked for"
	obs.Hosts = nil
	obs.Manifest.Egress.Rules = nil

	inv := fidelity.Build(obs)
	score := inv.Score()
	require.Zero(t, score.Counted)
	_, ok := score.Percent()
	require.False(t, ok, "a percentage was produced from nothing that was measured")
	require.Contains(t, inv.Headline(), "no score")
	require.NotContains(t, inv.Headline(), "0 percent")
}

// A dimension the manifest never asked for must not read as fully reproduced.
// An environment that sends no traffic has not reproduced traffic perfectly.
func TestADimensionNobodyAskedForIsExcludedRatherThanCountedAsAPass(t *testing.T) {
	obs := full()
	obs.Manifest.Personas = nil
	obs.Personas = nil
	inv := fidelity.Build(obs)

	d, ok := inv.Dimension(schema.FidelityAuth)
	require.True(t, ok)
	require.Empty(t, d.Components)
	require.Contains(t, d.NotApplicable, "no personas")
	require.Equal(t, fidelity.Unmeasured, d.Verdict())

	var excluded bool
	for _, e := range inv.Score().Excluded {
		if e.Dimension == schema.FidelityAuth {
			excluded = true
			require.Empty(t, e.Component, "a whole dimension was excluded as if it were one component")
		}
	}
	require.True(t, excluded)
}

// The runtime is reported and never scored, because nothing here knows what
// production runs on and inventing the other side of that comparison is the
// thing this package refuses to do.
func TestTheRuntimeIsReportedAndNeverScored(t *testing.T) {
	inv := fidelity.Build(full())
	d, ok := inv.Dimension(schema.FidelityRuntime)
	require.True(t, ok)
	require.NotEmpty(t, d.NotApplicable)
	require.Contains(t, d.NotApplicable, "the local runtime")
	require.Contains(t, d.NotApplicable, "nothing to compare it against")
}

func TestServices(t *testing.T) {
	t.Run("running and ready is reproduced", func(t *testing.T) {
		c := componentState(t, fidelity.Build(full()), schema.FidelityServices, "web")
		require.Equal(t, fidelity.Reproduced, c.State)
		require.Contains(t, c.Detail, "http://127.0.0.1:8080")
	})

	t.Run("declared and not running is absent", func(t *testing.T) {
		obs := full()
		obs.Running = obs.Running[:1]
		c := componentState(t, fidelity.Build(obs), schema.FidelityServices, "worker")
		require.Equal(t, fidelity.Absent, c.State)
		require.Equal(t, "declared and not running", c.Detail)
	})

	t.Run("running and not answering is absent in the runtime's own words", func(t *testing.T) {
		obs := full()
		obs.Running[0].Ready = false
		obs.Running[0].State = "exited"
		obs.Running[0].Detail = "Exited (1) 2 seconds ago"
		c := componentState(t, fidelity.Build(obs), schema.FidelityServices, "web")
		require.Equal(t, fidelity.Absent, c.State)
		require.Equal(t, "exited Exited (1) 2 seconds ago", c.Detail)
	})

	// A stopped environment has not been shown to reproduce nothing. Counting
	// every service absent here would put a real zero in a real denominator
	// for a run that never happened.
	t.Run("a stopped environment is unmeasured rather than absent", func(t *testing.T) {
		obs := full()
		obs.Running = nil
		obs.ServicesReason = "nothing is running for this environment"
		inv := fidelity.Build(obs)
		for _, name := range []string{"web", "worker"} {
			require.Equal(t, fidelity.Unmeasured,
				componentState(t, inv, schema.FidelityServices, name).State)
		}
	})
}

func TestDatabase(t *testing.T) {
	t.Run("a verified attested branch is reproduced", func(t *testing.T) {
		inv := fidelity.Build(full())
		data := componentState(t, inv, schema.FidelityDatabase, "data")
		require.Equal(t, fidelity.Reproduced, data.State)
		require.Contains(t, data.Detail, "12 tables over 184000 rows")
		require.Equal(t, fidelity.Reproduced,
			componentState(t, inv, schema.FidelityDatabase, "provenance").State)
	})

	// A count that stopped at its ceiling is a floor, and a floor printed as a
	// total is a number somebody would quote at a customer. The word has to
	// survive all the way to the line a person reads.
	t.Run("a count that stopped at its ceiling says at least", func(t *testing.T) {
		obs := full()
		obs.RowsAreAFloor = true
		c := componentState(t, fidelity.Build(obs), schema.FidelityDatabase, "data")
		require.Contains(t, c.Detail, "at least 184000 rows")

		exact := componentState(t, fidelity.Build(full()), schema.FidelityDatabase, "data")
		require.NotContains(t, exact.Detail, "at least",
			"a complete count must not hedge, or the word stops meaning anything")
	})

	t.Run("a subset is a substitution and says the row counts are not production's", func(t *testing.T) {
		obs := full()
		obs.Subset = true
		c := componentState(t, fidelity.Build(obs), schema.FidelityDatabase, "data")
		require.Equal(t, fidelity.Substituted, c.State)
		require.Contains(t, c.Detail, "not production's")
	})

	t.Run("a golden built with no source is a substitution", func(t *testing.T) {
		obs := full()
		obs.Empty = true
		c := componentState(t, fidelity.Build(obs), schema.FidelityDatabase, "data")
		require.Equal(t, fidelity.Substituted, c.State)
		require.Contains(t, c.Detail, "none of its rows")
	})

	t.Run("an attestation that does not check out is absent, not unmeasured", func(t *testing.T) {
		obs := full()
		obs.Attested = false
		obs.Attestation = "the attestation does not match its own signature"
		c := componentState(t, fidelity.Build(obs), schema.FidelityDatabase, "provenance")
		require.Equal(t, fidelity.Absent, c.State)
		require.Contains(t, c.Detail, "signature")
	})

	t.Run("an unverified golden is absent, not unknown", func(t *testing.T) {
		obs := full()
		obs.Attested = false
		obs.Attestation = "golden gv_20260830120000_abcd1234 is no longer marked verified"
		c := componentState(t, fidelity.Build(obs), schema.FidelityDatabase, "provenance")
		require.Equal(t, fidelity.Absent, c.State,
			"a golden that lost its verification is the one case somebody has to act on")
		require.Contains(t, c.Detail, "no longer marked verified")
	})

	t.Run("a provider that cannot say leaves provenance unmeasured", func(t *testing.T) {
		obs := full()
		obs.Golden, obs.Attested = "", false
		obs.GoldenReason = "this provider does not record which golden a branch came from"
		c := componentState(t, fidelity.Build(obs), schema.FidelityDatabase, "provenance")
		require.Equal(t, fidelity.Unmeasured, c.State)
		require.Equal(t, obs.GoldenReason, c.Detail)
	})
}

func TestThirdPartyModes(t *testing.T) {
	cases := []struct {
		name string
		host fidelity.Host
		want fidelity.State
		says string
	}{
		{"allow reaches the real host", fidelity.Host{Name: "h", Mode: schema.ModeAllow},
			fidelity.Reproduced, "for real"},
		{"sandbox is the provider's own sandbox", fidelity.Host{Name: "h", Mode: schema.ModeSandbox},
			fidelity.Substituted, "sandbox"},
		{"capture answers with the success shape", fidelity.Host{Name: "h", Mode: schema.ModeCapture},
			fidelity.Substituted, "inbox"},
		{"a stateful pack keeps what was created",
			fidelity.Host{Name: "h", Mode: schema.ModeMock, Pack: "stripe", Stateful: true},
			fidelity.Substituted, "keeps what was created"},
		{"a stateless pack says so",
			fidelity.Host{Name: "h", Mode: schema.ModeMock, Pack: "canned"},
			fidelity.Substituted, "keeps no state"},
		{"mock with no pack refuses every request",
			fidelity.Host{Name: "h", Mode: schema.ModeMock},
			fidelity.Absent, "404"},
		{"a wildcard rule cannot be attributed to a pack",
			fidelity.Host{Name: "*.h", Mode: schema.ModeMock, PackReason: "the rule matches a pattern"},
			fidelity.Unmeasured, "pattern"},
		{"synth is unmeasured because the product calls it unverified",
			fidelity.Host{Name: "h", Mode: schema.ModeSynth},
			fidelity.Unmeasured, "unverified"},
		{"block is a refusal rather than an absence",
			fidelity.Host{Name: "h", Mode: schema.ModeBlock},
			fidelity.Refused, "blocked by the policy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs := full()
			obs.Hosts = []fidelity.Host{tc.host}
			c := componentState(t, fidelity.Build(obs), schema.FidelityThirdParty, tc.host.Name)
			require.Equal(t, tc.want, c.State)
			require.Contains(t, c.Detail, tc.says)
		})
	}
}

// The claim the design rests on: a host answered offline by a pack that keeps
// state is a better reproduction than a host the policy refuses, and the
// ordering has to say so.
func TestAStatefulMockOutranksABlockedHost(t *testing.T) {
	mocked := full()
	mocked.Hosts = []fidelity.Host{{Name: "h", Mode: schema.ModeMock, Pack: "stripe", Stateful: true}}
	blocked := full()
	blocked.Hosts = []fidelity.Host{{Name: "h", Mode: schema.ModeBlock}}

	mockedDim, _ := fidelity.Build(mocked).Dimension(schema.FidelityThirdParty)
	blockedDim, _ := fidelity.Build(blocked).Dimension(schema.FidelityThirdParty)
	require.Equal(t, fidelity.Substituted, mockedDim.Verdict())
	require.Equal(t, fidelity.Refused, blockedDim.Verdict())

	// Both in one dimension, which is the case that actually pins the order.
	// Each host on its own reports its own state whatever the ranking says, so
	// a dimension holding both is the only place a reversed rank shows up: the
	// verdict has to be the refusal, because a policy that blocks a host has
	// not reproduced it and a pack that answers for another one does not make
	// up for that.
	both := full()
	both.Hosts = []fidelity.Host{
		{Name: "api.stripe.com", Mode: schema.ModeMock, Pack: "stripe", Stateful: true},
		{Name: "api.twilio.com", Mode: schema.ModeBlock},
	}
	bothDim, _ := fidelity.Build(both).Dimension(schema.FidelityThirdParty)
	require.Equal(t, fidelity.Refused, bothDim.Verdict(),
		"a dimension holding a substitution and a refusal reports the refusal")
}

func TestThirdPartyWithNoNamedHostsIsExcluded(t *testing.T) {
	obs := full()
	obs.Hosts = nil
	obs.Manifest.Egress = &schema.Egress{Default: schema.ModeBlock}
	d, ok := fidelity.Build(obs).Dimension(schema.FidelityThirdParty)
	require.True(t, ok)
	require.Contains(t, d.NotApplicable, "names no third party hosts")
	require.Contains(t, d.NotApplicable, "block")
}

func TestPersonas(t *testing.T) {
	t.Run("an account that exists and signs in with a password is reproduced", func(t *testing.T) {
		c := componentState(t, fidelity.Build(full()), schema.FidelityAuth, "buyer")
		require.Equal(t, fidelity.Reproduced, c.State)
		require.Contains(t, c.Detail, "public.users")
	})

	t.Run("an account with no row is absent", func(t *testing.T) {
		obs := full()
		obs.Personas[0].Present = false
		c := componentState(t, fidelity.Build(obs), schema.FidelityAuth, "buyer")
		require.Equal(t, fidelity.Absent, c.State)
		require.Contains(t, c.Detail, "no account with this address")
	})

	// The account exists and cannot be signed in as, which is a different
	// failure from a missing account and reads differently to whoever fixes it.
	t.Run("a code that cannot be delivered is absent and says why", func(t *testing.T) {
		obs := full()
		obs.Personas[0].Login = schema.LoginEmailCode
		obs.Personas[0].Deliverable = false
		c := componentState(t, fidelity.Build(obs), schema.FidelityAuth, "buyer")
		require.Equal(t, fidelity.Absent, c.State)
		require.Contains(t, c.Detail, "the account exists")
		require.Contains(t, c.Detail, "no rule captures messages")
	})

	t.Run("a delivered code with a capture rule is reproduced", func(t *testing.T) {
		obs := full()
		obs.Personas[0].Login = schema.LoginMagicLink
		obs.Personas[0].Deliverable = true
		require.Equal(t, fidelity.Reproduced,
			componentState(t, fidelity.Build(obs), schema.FidelityAuth, "buyer").State)
	})

	t.Run("a second factor with nowhere to enrol it is absent", func(t *testing.T) {
		obs := full()
		obs.Personas[0].MFA = true
		c := componentState(t, fidelity.Build(obs), schema.FidelityAuth, "buyer")
		require.Equal(t, fidelity.Absent, c.State)
		require.Contains(t, c.Detail, "enrol a second factor")
	})

	t.Run("an adapter this cannot read leaves every persona unmeasured", func(t *testing.T) {
		obs := full()
		obs.Personas = nil
		obs.PersonasReason = "the clerk adapter creates accounts through the provider's own API"
		c := componentState(t, fidelity.Build(obs), schema.FidelityAuth, "buyer")
		require.Equal(t, fidelity.Unmeasured, c.State)
		require.Equal(t, obs.PersonasReason, c.Detail)
	})
}

func TestTraffic(t *testing.T) {
	t.Run("no traffic asked for is excluded", func(t *testing.T) {
		d, _ := fidelity.Build(full()).Dimension(schema.FidelityTraffic)
		require.Contains(t, d.NotApplicable, "does not ask for traffic")
	})

	t.Run("an access log is production's shape", func(t *testing.T) {
		obs := full()
		obs.Manifest.Load = &schema.Load{Enabled: true, Source: schema.LoadAccessLog}
		obs.Traffic = "41 routes read from ops/access.log, at 120 requests a second"
		c := componentState(t, fidelity.Build(obs), schema.FidelityTraffic, "endpoint mix")
		require.Equal(t, fidelity.Reproduced, c.State)
		require.Equal(t, obs.Traffic, c.Detail)
	})

	t.Run("the default shape is not production's", func(t *testing.T) {
		obs := full()
		obs.Manifest.Load = &schema.Load{Enabled: true, Source: schema.LoadNone}
		c := componentState(t, fidelity.Build(obs), schema.FidelityTraffic, "endpoint mix")
		require.Equal(t, fidelity.Absent, c.State)
		require.Contains(t, c.Detail, "engine's own default shape")
	})

	t.Run("a source nobody connected is absent with the engine's own reason", func(t *testing.T) {
		obs := full()
		obs.Manifest.Load = &schema.Load{Enabled: true, Source: schema.LoadDatadog}
		obs.TrafficReason = "datadog is not connected in this build; use access_log or none"
		c := componentState(t, fidelity.Build(obs), schema.FidelityTraffic, "endpoint mix")
		require.Equal(t, fidelity.Absent, c.State)
		require.Equal(t, obs.TrafficReason, c.Detail)
	})
}

func TestADimensionsVerdictIsItsWeakestMeasuredComponent(t *testing.T) {
	obs := full()
	obs.Running = obs.Running[:1]
	d, _ := fidelity.Build(obs).Dimension(schema.FidelityServices)
	require.Equal(t, fidelity.Absent, d.Verdict())

	obs = full()
	obs.Hosts = []fidelity.Host{
		{Name: "a", Mode: schema.ModeAllow},
		{Name: "b", Mode: schema.ModeCapture},
		{Name: "c", Mode: schema.ModeSynth},
	}
	d, _ = fidelity.Build(obs).Dimension(schema.FidelityThirdParty)
	require.Equal(t, fidelity.Substituted, d.Verdict(),
		"the unmeasured host changed the verdict")
}

func TestPercentIsRoundedHalfUpAndUndefinedOnNothing(t *testing.T) {
	cases := []struct {
		reproduced, counted, want int
	}{
		{0, 1, 0}, {1, 1, 100}, {1, 2, 50}, {1, 3, 33}, {2, 3, 67},
		{1, 8, 13}, {3, 8, 38}, {5, 8, 63}, {17, 21, 81},
	}
	for _, tc := range cases {
		got, ok := fidelity.Score{Reproduced: tc.reproduced, Counted: tc.counted}.Percent()
		require.True(t, ok)
		require.Equal(t, tc.want, got, "%d of %d", tc.reproduced, tc.counted)
	}
	_, ok := fidelity.Score{}.Percent()
	require.False(t, ok)
}

func TestCheckTellsUnmetApartFromUnmeasurable(t *testing.T) {
	obs := full()
	obs.Running = obs.Running[:1]
	inv := fidelity.Build(obs)

	got := inv.Check([]schema.FidelityDimension{schema.FidelityServices, schema.FidelityDatabase})
	require.Len(t, got, 2)

	require.False(t, got[0].Met)
	require.True(t, got[0].Measurable, "a measured shortfall was reported as unmeasurable")
	require.Contains(t, got[0].Because, "worker is absent")

	require.True(t, got[1].Met)
	require.True(t, got[1].Measurable)

	// A dimension with something unmeasured in it is neither met nor broken.
	obs = full()
	obs.GoldenReason = "this provider does not record which golden a branch came from"
	obs.Golden, obs.Attested = "", false
	got = fidelity.Build(obs).Check([]schema.FidelityDimension{schema.FidelityDatabase})
	require.False(t, got[0].Met)
	require.False(t, got[0].Measurable)
	// The reason names the component and does not repeat the verdict, because
	// both renderers already say the dimension could not be measured and the
	// message stuttered when this said it too.
	require.Equal(t, "no state could be read for provenance", got[0].Because)

	// So is a dimension that had nothing to measure at all.
	got = fidelity.Build(full()).Check([]schema.FidelityDimension{schema.FidelityTraffic})
	require.False(t, got[0].Met)
	require.False(t, got[0].Measurable)
}

func TestExplainCarriesTheDefinitionAndTheExclusions(t *testing.T) {
	obs := full()
	obs.Hosts[0] = fidelity.Host{Name: "api.stripe.com", Mode: schema.ModeSynth}
	text := fidelity.Build(obs).Explain()

	// The count and the definition travel with the number, every time. A bare
	// percentage is the shape of an invented statistic even when it is not
	// one, and "5 of 6" is the part a reader can check against the table above
	// it.
	require.Contains(t, text,
		"5 of 6 measured components are production's own, which is 83 percent.",
		"the headline dropped the count the percentage is derived from")
	require.Contains(t, text, "3 components and dimensions are excluded and named below.")

	require.Contains(t, text, "A component is production's own when the environment reaches the real thing.")
	require.Contains(t, text, "Nothing unmeasured is counted either way.")
	require.Contains(t, text, "Not measured, and so not counted:")
	require.Contains(t, text, "api.stripe.com")
	require.Contains(t, text, "runtime")

	// Every dimension appears, including the ones with nothing in them, so a
	// reader cannot mistake a dimension that measured nothing for one that was
	// not part of the inventory.
	for _, name := range schema.AllFidelityDimensions() {
		require.Contains(t, text, string(name))
	}
	require.Equal(t, text, fidelity.Build(obs).Explain(), "Explain is not stable across calls")
}

func TestExplainRequirementsSaysWhichKindOfFailureItWas(t *testing.T) {
	text := fidelity.ExplainRequirements([]fidelity.Requirement{
		{Dimension: schema.FidelityDatabase, Met: true, Measurable: true},
		{Dimension: schema.FidelityServices, Measurable: true, Because: "worker is absent"},
		{Dimension: schema.FidelityTraffic, Because: "the manifest does not ask for traffic"},
	})
	require.Contains(t, text, "database       reproduced")
	require.Contains(t, text, "not reproduced: worker is absent")
	require.Contains(t, text, "not measured, so neither met nor broken")
	require.Equal(t, 1, strings.Count(text, "Required by the manifest:"))
	require.Empty(t, fidelity.ExplainRequirements(nil))
}

// A name too long for its column is cut to fit, and a name is whatever named
// it. A host or a service can carry a non ASCII character, and cutting one on
// a byte rather than on a rune produces invalid UTF-8: a mangled name in the
// terminal and a replacement character in the JSON somebody parses.
func TestALongNameIsCutOnARuneAndNotOnAByte(t *testing.T) {
	obs := full()
	obs.Hosts = []fidelity.Host{
		{Name: strings.Repeat("é", 30), Mode: schema.ModeBlock},
	}
	text := fidelity.Build(obs).Explain()
	require.True(t, utf8.ValidString(text), "the rendered inventory is not valid UTF-8")
	require.NotContains(t, text, "�", "a rune was cut in half")
}
