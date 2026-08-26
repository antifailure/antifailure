package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseRate_ReadsWhatSomebodyWouldWrite(t *testing.T) {
	t.Parallel()
	cases := map[string]float64{
		"10/s": 10, "10": 10, "600/m": 10, "3600/h": 1, "0.5/s": 0.5,
		"60/minute": 1, "1/hour": 1.0 / 3600,
	}
	for spec, want := range cases {
		per, _, ok := parseRate(spec)
		require.True(t, ok, spec)
		require.InDelta(t, want, per, 0.0001, spec)
	}
}

func TestParseRate_AMalformedLimitIsNoLimit(t *testing.T) {
	t.Parallel()
	// Refusing to start over a typo in a field that is an optimisation would
	// take an environment down for the wrong reason.
	for _, spec := range []string{"", "  ", "abc", "10/fortnight", "-5/s", "0/s"} {
		_, _, ok := parseRate(spec)
		require.False(t, ok, spec)
	}
}

func TestParseRate_BurstIsAtLeastOne(t *testing.T) {
	t.Parallel()
	// A limit of 60 a minute still has to let one request through at once, or
	// every first request waits a second for a budget it has not spent.
	_, burst, ok := parseRate("60/m")
	require.True(t, ok)
	require.GreaterOrEqual(t, burst, 1.0)
}

func TestLimiter_LetsTheBurstThroughImmediately(t *testing.T) {
	t.Parallel()
	// An application that opens six connections at startup should not have
	// five of them wait a second each.
	l := newLimiter()
	started := time.Now()
	for i := 0; i < 10; i++ {
		l.wait("api.stripe.com", "10/s")
	}
	require.Less(t, time.Since(started), 200*time.Millisecond)
}

func TestLimiter_ShapesRatherThanRefuses(t *testing.T) {
	t.Parallel()
	// A refused request looks to the application exactly like the host being
	// down, and an application that retries a 429 turns a limit into a storm.
	l := newLimiter()
	for i := 0; i < 5; i++ {
		l.wait("slow", "5/s")
	}
	started := time.Now()
	waited := l.wait("slow", "5/s")
	require.Greater(t, waited, 50*time.Millisecond, "the eleventh request waits")
	require.Less(t, time.Since(started), 2*time.Second)
}

func TestLimiter_LimitsArePerRule(t *testing.T) {
	t.Parallel()
	// Spending one host's budget on another is how a busy analytics endpoint
	// starves the payment call somebody actually cares about.
	l := newLimiter()
	for i := 0; i < 5; i++ {
		l.wait("busy", "5/s")
	}
	started := time.Now()
	l.wait("quiet", "5/s")
	require.Less(t, time.Since(started), 100*time.Millisecond)
}

func TestLimiter_NoLimitNeverWaits(t *testing.T) {
	t.Parallel()
	l := newLimiter()
	started := time.Now()
	for i := 0; i < 500; i++ {
		require.Zero(t, l.wait("open", ""))
	}
	require.Less(t, time.Since(started), 200*time.Millisecond)
}

func TestDescribeRate_ReadsAsProse(t *testing.T) {
	t.Parallel()
	require.Equal(t, "10 a second, bursting to 10", describeRate("10/s"))
	require.Equal(t, "no limit", describeRate("nonsense"))
}

func TestSynthFromEnvironment_NoKeyIsNotAnError(t *testing.T) {
	t.Parallel()
	// No key is the normal case, and a synth rule then refuses with a message
	// saying which variable to set rather than the sidecar failing to start.
	require.Nil(t, synthFromEnvironment(func(string) string { return "" }))

	anthropic := synthFromEnvironment(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "k"
		}
		return ""
	})
	require.NotNil(t, anthropic)
	require.Equal(t, "anthropic", anthropic.provider)
}

func TestParseSynth_ReadsAShapeOutOfProse(t *testing.T) {
	t.Parallel()
	status, body := parseSynth(`Sure. {"status": 201, "body": {"id": "cus_test"}}`)
	require.Equal(t, 201, status)
	require.JSONEq(t, `{"id":"cus_test"}`, body)

	status, body = parseSynth(`{"body": {"ok": true}}`)
	require.Equal(t, 200, status, "a missing status means success")
	require.JSONEq(t, `{"ok":true}`, body)
}

func TestParseSynth_GivesUpReadablyRatherThanAnsweringEmptily(t *testing.T) {
	t.Parallel()
	// An empty 200 would let the application carry on with nothing, which is
	// the failure this mode is already closest to.
	status, body := parseSynth("I am not sure what that API returns.")
	require.Equal(t, 502, status)
	require.Contains(t, body, "could not read the synthesized response")
}

func TestSynthPrompt_AsksForFakeValues(t *testing.T) {
	t.Parallel()
	req, err := http.NewRequest(http.MethodPost, "https://api.example.com/v1/things?x=1",
		strings.NewReader(`{"name":"a"}`))
	require.NoError(t, err)

	prompt := synthPrompt(req, "api.example.com", []byte(`{"name":"a"}`))
	require.Contains(t, prompt, "POST https://api.example.com/v1/things?x=1")
	require.Contains(t, prompt, `{"name":"a"}`)
	require.Contains(t, prompt, "example.test")
	require.Contains(t, prompt, "never anything")
}
