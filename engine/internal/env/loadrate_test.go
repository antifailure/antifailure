package env_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The precedence that was inverted.
//
// `af load run` gave both flags a cobra default, so opts.Duration and
// opts.Scale were never zero, so the branch that reads the manifest was
// unreachable and a manifest saying `scale: 0.05` sent production's full rate.
// The table is the whole contract: a typed flag wins, the manifest wins over a
// command default, and a command default is what is left when nobody said
// anything.
func TestResolveLoadRate(t *testing.T) {
	t.Parallel()

	// This repository's own manifest, which is what the defect was
	// demonstrated against.
	own := &schema.Load{Scale: 0.05, Duration: "60s"}

	for _, tc := range []struct {
		name     string
		opts     env.LoadOptions
		cfg      *schema.Load
		duration time.Duration
		scale    float64
	}{{
		name:     "a typed flag beats the manifest",
		opts:     env.LoadOptions{Duration: 5 * time.Second, Scale: 2, DefaultDuration: time.Minute, DefaultScale: 1},
		cfg:      own,
		duration: 5 * time.Second, scale: 2,
	}, {
		name:     "an untyped flag does not, so the manifest decides",
		opts:     env.LoadOptions{DefaultDuration: time.Minute, DefaultScale: 1},
		cfg:      own,
		duration: time.Minute, scale: 0.05,
	}, {
		name:     "no flag and no manifest leaves the command's own defaults",
		opts:     env.LoadOptions{DefaultDuration: time.Minute, DefaultScale: 1},
		cfg:      nil,
		duration: time.Minute, scale: 1,
	}, {
		name:     "a load block that configures neither is the same as none",
		opts:     env.LoadOptions{DefaultDuration: time.Minute, DefaultScale: 1},
		cfg:      &schema.Load{Enabled: true},
		duration: time.Minute, scale: 1,
	}, {
		// af ci --load. Its 30s used to sit in the overriding slot.
		name:     "af ci --load takes the manifest's duration over its own 30s",
		opts:     env.LoadOptions{DefaultDuration: 30 * time.Second, DefaultScale: 1},
		cfg:      &schema.Load{Scale: 0.05, Duration: "5m"},
		duration: 5 * time.Minute, scale: 0.05,
	}, {
		name:     "af ci --load keeps its 30s when the manifest is silent",
		opts:     env.LoadOptions{DefaultDuration: 30 * time.Second, DefaultScale: 1},
		cfg:      &schema.Load{Enabled: true},
		duration: 30 * time.Second, scale: 1,
	}, {
		// A duration the manifest schema would reject, reaching here anyway.
		// Falling through to the default is the only safe reading; taking
		// zero would hand load.Run a zero duration.
		name:     "an unparseable manifest duration falls through to the default",
		opts:     env.LoadOptions{DefaultDuration: time.Minute, DefaultScale: 1},
		cfg:      &schema.Load{Duration: "one minute"},
		duration: time.Minute, scale: 1,
	}, {
		// af load smoke. Its defaults are a promise about what a smoke is.
		name:     "smoke lets the manifest lower its rate",
		opts:     env.LoadOptions{DefaultDuration: 10 * time.Second, DefaultScale: 0.1, Ceiling: true},
		cfg:      own,
		duration: 10 * time.Second, scale: 0.05,
	}, {
		name:     "smoke does not let the manifest raise it",
		opts:     env.LoadOptions{DefaultDuration: 10 * time.Second, DefaultScale: 0.1, Ceiling: true},
		cfg:      &schema.Load{Scale: 4, Duration: "5m"},
		duration: 10 * time.Second, scale: 0.1,
	}, {
		name:     "smoke still obeys a typed flag above its own ceiling",
		opts:     env.LoadOptions{Duration: 5 * time.Minute, Scale: 4, DefaultDuration: 10 * time.Second, DefaultScale: 0.1, Ceiling: true},
		cfg:      &schema.Load{Scale: 0.05},
		duration: 5 * time.Minute, scale: 4,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, s := env.ResolveLoadRate(tc.opts, tc.cfg)
			require.Equal(t, tc.duration, d)
			require.InDelta(t, tc.scale, s, 1e-9)
		})
	}
}

// The property that matters more than any single row: nothing this resolves is
// traffic nobody asked for. The rate is either no more than the command used to
// send, or exactly the number the manifest names. The defect was traffic aimed
// at something fragile, so a fix that could raise the rate on its own judgment
// would be a second one, and `af load smoke` never rises at all because its
// defaults are a promise about what a smoke is.
func TestResolveLoadRate_NeverSendsMoreThanTheOldCodeDid(t *testing.T) {
	t.Parallel()
	for _, scale := range []float64{0, 0.01, 0.05, 0.5, 1, 4} {
		for _, dur := range []string{"", "1s", "60s", "5m"} {
			cfg := &schema.Load{Scale: scale, Duration: dur}
			for _, cmd := range []struct {
				name    string
				opts    env.LoadOptions
				oldRate float64
			}{
				{"run", env.LoadOptions{DefaultDuration: time.Minute, DefaultScale: 1}, 1},
				{"smoke", env.LoadOptions{DefaultDuration: 10 * time.Second, DefaultScale: 0.1, Ceiling: true}, 0.1},
			} {
				_, got := env.ResolveLoadRate(cmd.opts, cfg)
				if got > cmd.oldRate {
					require.Equalf(t, scale, got,
						"%s with scale %v duration %q resolved to %v, above the %v it used to "+
							"send and not a number the manifest asked for",
						cmd.name, scale, dur, got, cmd.oldRate)
					require.Falsef(t, cmd.opts.Ceiling,
						"%s promises a ceiling of %v and resolved to %v", cmd.name, cmd.oldRate, got)
				}
			}
		}
	}
}
