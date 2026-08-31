package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Whether tightening an egress policy silently switches the model off.
//
// This product intercepts and controls outbound HTTP, and a model call is
// outbound HTTP, so the question is not academic: "the AI planner stopped
// working when I tightened my egress rules" is a genuinely confusing failure
// and an expensive support ticket, because the two things look unrelated and
// nothing in either place mentions the other.
//
// The answer is that the model call is not subject to the policy, and it is
// worth being precise about why rather than treating it as luck. The policy
// applies to traffic THROUGH the sidecar: services sit on a network with no
// route out and every name they resolve points at the sidecar, so their packets
// have nowhere else to go. A synth rule's own model call originates IN the
// sidecar, which is the one container with a route out, and it is made with its
// own client rather than through the engine that decides about everybody else's
// traffic.
//
// So a model provider does not have to be named in the manifest, and 'default:
// block' does not turn the planner off. That is the behaviour somebody would
// want and it is also entirely invisible in the code, which is why it is
// asserted here: a refactor that routed the sidecar's own calls through its own
// policy would be a reasonable looking change that broke synth mode for
// everybody with a default of block.

// modelServer stands in for api.anthropic.com and records that it was reached.
func modelServer(t *testing.T, reply string) (*httptest.Server, *bool) {
	t.Helper()
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv, &reached
}

func TestSynth_TheModelCallIsNotSubjectToTheEgressPolicy(t *testing.T) {
	t.Parallel()

	model, reached := modelServer(t,
		`{"content":[{"type":"text","text":"{\"status\":200,\"body\":{\"id\":\"cus_test\"}}"}]}`)

	// The strictest policy this product has: everything blocked except the one
	// host the rule names, and the model provider is deliberately not named.
	// Nothing about this manifest gives the sidecar permission to reach a model
	// and it must reach one anyway.
	s := newSidecar(t, &schema.Egress{
		Default: schema.ModeBlock,
		Rules:   []schema.EgressRule{{Host: "api.stripe.com", Mode: schema.ModeSynth}},
	})
	s.proxy.synth = &synthConfig{
		provider: "anthropic", apiKey: "sk-test", model: "claude-sonnet-5",
		baseURL: model.URL,
	}

	req, err := http.NewRequest(http.MethodPost,
		"https://api.stripe.com/v1/customers", strings.NewReader(`{"email":"a@example.test"}`))
	require.NoError(t, err)

	var out bytes.Buffer
	var rec record
	require.True(t, s.proxy.serveSynth(&out, req, "api.stripe.com", &rec))

	require.True(t, *reached,
		"the sidecar's own model call was stopped by the policy it enforces on "+
			"everybody else, so a manifest with 'default: block' silently turns "+
			"synth mode off")
	require.True(t, rec.Synthesized)
	require.Equal(t, 200, rec.Status)
	require.Contains(t, out.String(), "X-Antifailure-Synthesized: true")
	require.Contains(t, out.String(), "cus_test")
}

// The refusal when there is no key names the variable to set, and does not
// blame the policy. Somebody reading "this host is set to synth" against a
// blocking manifest would otherwise reasonably conclude the block was the
// cause, and go and edit the wrong file.
func TestSynth_NoKeyRefusesWithoutBlamingThePolicy(t *testing.T) {
	t.Parallel()

	s := newSidecar(t, &schema.Egress{
		Default: schema.ModeBlock,
		Rules:   []schema.EgressRule{{Host: "api.stripe.com", Mode: schema.ModeSynth}},
	})
	require.Nil(t, s.proxy.synth, "no key was configured, so there is nothing to call with")

	req, err := http.NewRequest(http.MethodPost,
		"https://api.stripe.com/v1/customers", strings.NewReader(`{}`))
	require.NoError(t, err)

	var out bytes.Buffer
	var rec record
	require.True(t, s.proxy.serveSynth(&out, req, "api.stripe.com", &rec))

	require.Equal(t, http.StatusForbidden, rec.Status)
	require.False(t, rec.Allowed)
	require.Contains(t, out.String(), "ANTHROPIC_API_KEY or OPENAI_API_KEY")
	// The word that would send somebody to the wrong file.
	require.NotContains(t, strings.ToLower(out.String()), "egress")
}

// A model that cannot be reached is reported as a model that cannot be reached.
// The same sentence for an unreachable model and a blocked host would be the
// one that makes this confusing, because only one of them is fixed in the
// manifest.
func TestSynth_AnUnreachableModelSaysSo(t *testing.T) {
	t.Parallel()

	s := newSidecar(t, &schema.Egress{
		Default: schema.ModeBlock,
		Rules:   []schema.EgressRule{{Host: "api.stripe.com", Mode: schema.ModeSynth}},
	})
	s.proxy.synth = &synthConfig{
		provider: "anthropic", apiKey: "sk-test", model: "claude-sonnet-5",
		// Nothing listens here, and port 1 is never a real service.
		baseURL: "http://127.0.0.1:1",
	}

	req, err := http.NewRequest(http.MethodPost,
		"https://api.stripe.com/v1/customers", strings.NewReader(`{}`))
	require.NoError(t, err)

	var out bytes.Buffer
	var rec record
	require.True(t, s.proxy.serveSynth(&out, req, "api.stripe.com", &rec))

	require.Equal(t, http.StatusBadGateway, rec.Status)
	require.Contains(t, out.String(), "could not reach the model")
	require.NotContains(t, out.String(), "sk-test")
}
