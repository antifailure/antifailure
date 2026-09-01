package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/load"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// What the command hands the engine, read off the real cobra command after
// parsing real arguments.
//
// The defect was here and not in the engine: the fallback that reads the
// manifest was written and correct, and unreachable, because a cobra flag holds
// its default whether or not anybody typed it. Asserting the engine's
// precedence alone would have stayed green through the entire outage.
func TestLoadRate_UntypedFlagsDoNotOverrideTheManifest(t *testing.T) {
	t.Parallel()

	// This repository's own manifest. `af explain` reads the five percent back
	// correctly, which is what made the full rate a surprise.
	own := &schema.Load{Scale: 0.05, Duration: "60s"}

	for _, tc := range []struct {
		name     string
		smoke    bool
		argv     []string
		cfg      *schema.Load
		duration time.Duration
		scale    float64
	}{{
		name: "af load run, nothing typed, manifest decides",
		argv: nil, cfg: own,
		duration: time.Minute, scale: 0.05,
	}, {
		name: "af load run --scale 2, typed, the flag decides",
		argv: []string{"--scale", "2"}, cfg: own,
		duration: time.Minute, scale: 2,
	}, {
		name: "af load run --duration 5s, one typed flag does not carry the other",
		argv: []string{"--duration", "5s"}, cfg: own,
		duration: 5 * time.Second, scale: 0.05,
	}, {
		name: "af load run, nothing typed, no manifest, the command's own default",
		argv: nil, cfg: nil,
		duration: time.Minute, scale: 1,
	}, {
		name: "af load smoke, nothing typed, manifest lowers it", smoke: true,
		argv: nil, cfg: own,
		duration: 10 * time.Second, scale: 0.05,
	}, {
		name: "af load smoke, nothing typed, no manifest", smoke: true,
		argv: nil, cfg: nil,
		duration: 10 * time.Second, scale: 0.1,
	}, {
		// A flag typed at the value that happens to equal the default is still
		// a choice, and reading the variable alone cannot tell them apart.
		name: "af load run --scale 1 typed, beats a manifest that says 0.05",
		argv: []string{"--scale", "1"}, cfg: own,
		duration: time.Minute, scale: 1,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cmd := newLoadRunCommand(&Env{}, tc.smoke)
			require.NoError(t, cmd.ParseFlags(tc.argv))

			duration, _ := cmd.Flags().GetDuration("duration")
			scale, _ := cmd.Flags().GetFloat64("scale")
			defaultDuration, defaultScale := time.Minute, 1.0
			if tc.smoke {
				defaultDuration, defaultScale = 10*time.Second, 0.1
			}
			opts := loadRate(cmd, duration, scale, defaultDuration, defaultScale, tc.smoke)

			gotDuration, gotScale := env.ResolveLoadRate(opts, tc.cfg)
			require.Equal(t, tc.duration, gotDuration)
			require.InDelta(t, tc.scale, gotScale, 1e-9)
		})
	}
}

// The resolved number is not a number in a struct: it is requests that leave.
//
// Asserting the multiplier and stopping there is the shape of test that was
// already green while the product sent twenty times what the manifest asked
// for. This one counts arrivals at a real server.
func TestLoadRate_TheResolvedScaleIsWhatActuallyLeaves(t *testing.T) {
	t.Parallel()

	// One route at a known production rate, so the arrival count is the
	// multiplier and nothing else.
	shape := load.Shape{
		Source:            "otel",
		RequestsPerSecond: 200,
		Routes:            []load.Route{{Method: "GET", Path: "/", Weight: 1}},
	}

	var full, scaled atomic.Int64
	count := func(c *atomic.Int64) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			c.Add(1)
			w.WriteHeader(http.StatusOK)
		}))
	}
	fullSrv, scaledSrv := count(&full), count(&scaled)
	defer fullSrv.Close()
	defer scaledSrv.Close()

	// The manifest a customer aiming this at something fragile writes.
	cfg := &schema.Load{Scale: 0.05, Duration: "2s"}
	cmd := newLoadRunCommand(&Env{}, false)
	require.NoError(t, cmd.ParseFlags(nil))
	duration, _ := cmd.Flags().GetDuration("duration")
	scale, _ := cmd.Flags().GetFloat64("scale")
	_, resolved := env.ResolveLoadRate(
		loadRate(cmd, duration, scale, time.Minute, 1.0, false), cfg)

	send := func(srv *httptest.Server, scale float64) {
		_, err := load.Run(context.Background(), load.Options{
			BaseURL: srv.URL, Shape: shape, Scale: scale,
			Duration: 2 * time.Second, Seed: 1, Clock: clock.New(),
		})
		require.NoError(t, err)
	}
	// What the command used to send with nothing typed, beside what it sends
	// now, at the same seed against the same shape.
	send(fullSrv, 1.0)
	send(scaledSrv, resolved)

	require.Greater(t, full.Load(), int64(100),
		"the unscaled run is the control; if it did not send, the comparison means nothing")
	require.Less(t, scaled.Load(), full.Load()/4,
		"a manifest asking for five percent sent %d requests against the full rate's %d",
		scaled.Load(), full.Load())
}
