package workload_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/cli"
	"github.com/antifailure/antifailure/engine/internal/workload"
)

// These are the tests that make the reproducible command a promise rather than
// a hope, and they are the reason the adapter is shaped the way it is.
//
// The weak version of this test compares the emitted argv against a string
// somebody typed into the test. That passes forever after `--scale` is renamed
// to `--rate`, because both sides of the comparison are the same wrong guess.
//
// So both tests below read the REAL command tree, the same one the binary
// serves and the same one the published command reference is generated from.
// A flag renamed, removed, or retyped upstream fails them in the commit that
// does it.

// find walks the real command tree to the command an argv names.
func find(t *testing.T, argv []string) (*cobra.Command, []string) {
	t.Helper()
	root := cli.RootForDocs()
	// argv[0] is the program name, which the tree does not contain.
	cmd, rest, err := root.Find(argv[1:])
	require.NoErrorf(t, err, "the emitted argv names no command in the tree: %v", argv)
	require.NotNil(t, cmd)
	return cmd, rest
}

func TestTheEmittedArgvNamesARealCommandWithRealFlags(t *testing.T) {
	cases := []struct {
		name string
		req  workload.Request
		path string
	}{
		{"observed load", workload.Request{Kind: "observed_load"}, "af load run"},
		{"http scenario", workload.Request{Kind: "http_scenario", Select: "checkout"}, "af load scenario"},
		{"browser workflow", workload.Request{Kind: "browser_workflow"}, "af test"},
		{"exploration", workload.Request{Kind: "exploration", Select: "upgrade"}, "af explore"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := workload.Parse(tc.req)
			require.NoError(t, err)
			argv := plan.Argv()

			cmd, rest := find(t, argv)
			require.Equal(t, tc.path, cmd.CommandPath(),
				"the argv should name %s", tc.path)

			// ParseFlags rather than Execute: the point is that every flag in
			// the argv exists on the real command with the real type, not that
			// the command runs here with no environment.
			require.NoErrorf(t, cmd.ParseFlags(rest),
				"the emitted argv sets a flag %s does not have: %v", tc.path, argv)
			require.Empty(t, cmd.Flags().Args(),
				"the emitted argv carries a positional argument, and none of these commands take one")
		})
	}
}

// TestTheEmittedArgvSetsExactlyThePlansKnobs is the equivalence proof.
//
// It parses the emitted argv with the real command's own flag set and reads
// the values back out, so what is compared is what the binary would actually
// receive rather than what the string looks like.
func TestTheEmittedArgvSetsExactlyThePlansKnobs(t *testing.T) {
	t.Run("observed load carries duration, scale and seed", func(t *testing.T) {
		plan, err := workload.Parse(workload.Request{
			Kind: "observed_load", Duration: "90s", Scale: "0.25", Seed: "7",
		})
		require.NoError(t, err)
		cmd, rest := find(t, plan.Argv())
		require.NoError(t, cmd.ParseFlags(rest))

		d, err := cmd.Flags().GetDuration("duration")
		require.NoError(t, err)
		require.Equal(t, 90*time.Second, d)
		require.Equal(t, plan.Duration, d)

		s, err := cmd.Flags().GetFloat64("scale")
		require.NoError(t, err)
		require.InDelta(t, 0.25, s, 1e-9)
		require.InDelta(t, plan.Scale, s, 1e-9)

		seed, err := cmd.Flags().GetInt64("seed")
		require.NoError(t, err)
		require.Equal(t, int64(7), seed)
		require.Equal(t, plan.SeedNumber, seed)
	})

	t.Run("http scenario carries seed, concurrency and every selected name", func(t *testing.T) {
		plan, err := workload.Parse(workload.Request{
			Kind: "http_scenario", Select: "checkout, refund ,signup",
			Seed: "12", Concurrency: "40",
		})
		require.NoError(t, err)
		cmd, rest := find(t, plan.Argv())
		require.NoError(t, cmd.ParseFlags(rest))

		seed, err := cmd.Flags().GetInt64("seed")
		require.NoError(t, err)
		require.Equal(t, int64(12), seed)

		conc, err := cmd.Flags().GetInt("concurrency")
		require.NoError(t, err)
		require.Equal(t, 40, conc)
		require.Equal(t, plan.Concurrency, conc)

		only, err := cmd.Flags().GetStringSlice("only")
		require.NoError(t, err)
		require.Equal(t, []string{"checkout", "refund", "signup"}, only)
		require.Equal(t, plan.Select, only)
	})

	t.Run("browser workflow carries attempts and every selected name", func(t *testing.T) {
		plan, err := workload.Parse(workload.Request{
			Kind: "browser_workflow", Select: "sign-in,place an order",
		})
		require.NoError(t, err)
		cmd, rest := find(t, plan.Argv())
		require.NoError(t, cmd.ParseFlags(rest))

		attempts, err := cmd.Flags().GetInt("attempts")
		require.NoError(t, err)
		require.Equal(t, plan.Attempts, attempts)

		// A StringArray rather than a StringSlice here, which is exactly why
		// the argv repeats the flag instead of joining with commas: "place an
		// order" survives one and a name with a comma would not survive the
		// other.
		only, err := cmd.Flags().GetStringArray("only")
		require.NoError(t, err)
		require.Equal(t, []string{"sign-in", "place an order"}, only)
		require.Equal(t, plan.Select, only)
	})

	t.Run("exploration carries the seed as text", func(t *testing.T) {
		plan, err := workload.Parse(workload.Request{
			Kind: "exploration", Select: "upgrade", Seed: "a-word-not-a-number",
		})
		require.NoError(t, err)
		cmd, rest := find(t, plan.Argv())
		require.NoError(t, cmd.ParseFlags(rest))

		seed, err := cmd.Flags().GetString("seed")
		require.NoError(t, err)
		require.Equal(t, "a-word-not-a-number", seed)
		require.Equal(t, plan.SeedText, seed)

		only, err := cmd.Flags().GetStringArray("only")
		require.NoError(t, err)
		require.Equal(t, []string{"upgrade"}, only)
	})
}

// TestARefusalIsExactlyAMissingFlag is the rule the whole package rests on.
//
// A knob is refused when, and only when, the plain command this kind runs has
// no flag for it. Asserting the table by hand would encode today's opinion;
// asserting it against the real command tree means a flag added upstream turns
// a refusal into an acceptance in the same commit, and a flag removed upstream
// fails here rather than producing a hosted run that silently drops a knob.
func TestARefusalIsExactlyAMissingFlag(t *testing.T) {
	// The value each knob is sent with, and the flag it would have to reach.
	knobs := []struct {
		knob  string
		flag  string
		value string
		set   func(*workload.Request, string)
	}{
		{"workflows", "only", "a-name", func(r *workload.Request, v string) { r.Select = v }},
		{"duration", "duration", "30s", func(r *workload.Request, v string) { r.Duration = v }},
		{"scale", "scale", "2", func(r *workload.Request, v string) { r.Scale = v }},
		{"seed", "seed", "3", func(r *workload.Request, v string) { r.Seed = v }},
		{"concurrency", "concurrency", "5", func(r *workload.Request, v string) { r.Concurrency = v }},
	}

	for _, kind := range workload.Kinds() {
		for _, k := range knobs {
			t.Run(string(kind)+" "+k.knob, func(t *testing.T) {
				// The command this kind reproduces through, found by asking a
				// plan that sets nothing.
				base := baseRequest(kind)
				bare, err := workload.Parse(base)
				require.NoError(t, err)
				cmd, _ := find(t, bare.Argv())
				flagExists := cmd.Flags().Lookup(k.flag) != nil

				req := base
				k.set(&req, k.value)
				plan, err := workload.Parse(req)

				if flagExists {
					require.NoErrorf(t, err,
						"%s has a --%s flag, so a %s workload must accept it",
						cmd.CommandPath(), k.flag, kind)
					require.Emptyf(t, plan.Refusals,
						"%s has a --%s flag, so nothing should be refused", cmd.CommandPath(), k.flag)
					return
				}
				require.Errorf(t, err,
					"%s has no --%s flag, so a %s workload must refuse it loudly rather than drop it",
					cmd.CommandPath(), k.flag, kind)
				require.NotNil(t, plan)
				require.Lenf(t, plan.Refusals, 1, "exactly the one knob should be refused")
				require.Equal(t, k.knob, plan.Refusals[0].Knob)
				require.Equal(t, "AF-WLD-002", plan.Refusals[0].Code)
				require.Contains(t, err.Error(), k.knob)
			})
		}
	}
}

// baseRequest is the smallest request of a kind that parses, so a knob can be
// added to it one at a time.
func baseRequest(kind workload.Kind) workload.Request {
	req := workload.Request{Kind: string(kind)}
	switch kind {
	case workload.HTTPScenario, workload.Exploration:
		// Both refuse an empty selection, so the base carries one and the
		// selection case below overwrites it with the same shape.
		req.Select = "a-name"
	}
	return req
}

func TestTheCommandLineIsPasteable(t *testing.T) {
	plan, err := workload.Parse(workload.Request{
		Kind: "browser_workflow", Select: "place an order",
	})
	require.NoError(t, err)
	require.Equal(t, "af test --attempts 2 --only 'place an order'", plan.Command())

	// The quoted form has to survive a shell and come back as the same word,
	// which is the whole reason it is quoted.
	require.Equal(t, []string{"af", "test", "--attempts", "2", "--only", "place an order"}, plan.Argv())
}

func TestEveryKnobIsStatedExplicitlyEvenWhenItWasNotSent(t *testing.T) {
	// An argv that omits a flag reproduces that flag's default on the day it
	// is pasted, which is a weaker promise than reproducing this run. The
	// defaults in this repository have moved before.
	plan, err := workload.Parse(workload.Request{Kind: "observed_load"})
	require.NoError(t, err)
	line := plan.Command()
	for _, flag := range []string{"--duration", "--scale", "--seed"} {
		require.Containsf(t, line, flag,
			"a request that set nothing must still state %s, or the pasted command "+
				"reproduces whatever the default is that day", flag)
	}
	require.Equal(t, "af load run --duration 1m0s --scale 1 --seed 1", line)

	// And the stated values are the plain command's own defaults, read from
	// the tree rather than trusted.
	cmd, rest := find(t, plan.Argv())
	require.NoError(t, cmd.ParseFlags(nil))
	defDuration, err := cmd.Flags().GetDuration("duration")
	require.NoError(t, err)
	defScale, err := cmd.Flags().GetFloat64("scale")
	require.NoError(t, err)
	defSeed, err := cmd.Flags().GetInt64("seed")
	require.NoError(t, err)
	require.NoError(t, cmd.ParseFlags(rest))
	got, err := cmd.Flags().GetDuration("duration")
	require.NoError(t, err)
	require.Equal(t, defDuration, got, "a workload that set no duration runs af load run's own default")
	gotScale, err := cmd.Flags().GetFloat64("scale")
	require.NoError(t, err)
	require.InDelta(t, defScale, gotScale, 1e-9)
	gotSeed, err := cmd.Flags().GetInt64("seed")
	require.NoError(t, err)
	require.Equal(t, defSeed, gotSeed)
}

func TestAValueTheFlagCannotTakeIsRefusedRatherThanRounded(t *testing.T) {
	cases := []struct {
		name string
		req  workload.Request
		says string
	}{
		{"a duration that is not a duration",
			workload.Request{Kind: "observed_load", Duration: "1 hour"}, "duration"},
		{"a duration longer than the cap",
			workload.Request{Kind: "observed_load", Duration: "20m"}, "duration"},
		{"a scale that is not a number",
			workload.Request{Kind: "observed_load", Scale: "lots"}, "scale"},
		{"a negative scale",
			workload.Request{Kind: "observed_load", Scale: "-1"}, "scale"},
		{"a seed that is not a number",
			workload.Request{Kind: "http_scenario", Select: "a", Seed: "x"}, "seed"},
		{"a concurrency out of range",
			workload.Request{Kind: "http_scenario", Select: "a", Concurrency: "9000"}, "concurrency"},
		{"a name that would read as a flag",
			workload.Request{Kind: "http_scenario", Select: "--only"}, "workflows"},
		{"a name carrying a quote the scenario selection would split on",
			workload.Request{Kind: "http_scenario", Select: `a"b`}, "workflows"},
		{"more names than may be selected",
			workload.Request{Kind: "http_scenario", Select: manyNames(51)}, "workflows"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := workload.Parse(tc.req)
			require.Error(t, err)
			require.Contains(t, err.Error(), "AF-WLD-003")
			require.Contains(t, err.Error(), tc.says)
		})
	}
}

func manyNames(n int) string {
	parts := make([]string, 0, n)
	for i := 0; i < n; i++ {
		parts = append(parts, "name"+strconv.Itoa(i))
	}
	return strings.Join(parts, ",")
}

func TestAnEmptySelectionIsRefusedWhereTheCommandWouldRunEverything(t *testing.T) {
	for _, kind := range []workload.Kind{workload.HTTPScenario, workload.Exploration} {
		_, err := workload.Parse(workload.Request{Kind: string(kind)})
		require.Errorf(t, err, "%s with no selection must be refused", kind)
		require.Contains(t, err.Error(), "AF-WLD-004")
	}
	// af test with no selection runs every workflow, which is what af ci does
	// and what a hosted browser_workflow legitimately means.
	plan, err := workload.Parse(workload.Request{Kind: "browser_workflow"})
	require.NoError(t, err)
	require.Empty(t, plan.Select)
}

func TestALegacyDispatchVerbStillResolves(t *testing.T) {
	for verb, kind := range map[string]workload.Kind{
		"load": workload.ObservedLoad, "scenario": workload.HTTPScenario,
		"agents": workload.BrowserWorkflow, "explore": workload.Exploration,
	} {
		req := workload.Request{Kind: verb}
		if kind == workload.HTTPScenario || kind == workload.Exploration {
			req.Select = "a-name"
		}
		plan, err := workload.Parse(req)
		require.NoErrorf(t, err, "the verb %s used to mean %s and still should", verb, kind)
		require.Equal(t, kind, plan.Kind)
	}
	_, err := workload.Parse(workload.Request{Kind: "up"})
	require.Error(t, err, "af up is not a workload and must not resolve to one")
	require.Contains(t, err.Error(), "AF-WLD-001")
}
