package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/policy"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The twin that posts nowhere, and the one that posts somewhere real.
//
// A prospect's application delivers to third party CRMs through a Zapier catch
// hook. That is the case where the two failure modes of an egress rule are
// both expensive and both silent. A rule that answers a plausible success for
// a host nobody named produces a rehearsal in which every delivery reports
// success and no record moves, which is a twin that lies. A rule that lets the
// request out produces a rehearsal that fires somebody's real automation
// against real contacts, which is worse.
//
// The tests below drive the real policy engine and the real capture handler,
// with no fake in between, so they say what the sidecar does rather than what
// the manifest was hoping for.

// captureThrough runs one request through the capture path under a policy
// built from the given rules, and returns the raw response.
func captureThrough(t *testing.T, rules []schema.EgressRule, host, path, body string) string {
	t.Helper()
	engine, err := policy.New(&schema.Egress{Default: schema.ModeBlock, Rules: rules})
	require.NoError(t, err)

	preq := policy.Request{Host: host, Port: 443, Method: http.MethodPost, Path: path, TLS: true}
	decision := engine.Evaluate(preq)
	require.Equal(t, schema.ModeCapture, decision.Mode,
		"the rule under test has to reach capture, or this proves nothing about capture")

	req := httptest.NewRequest(http.MethodPost, "https://"+host+path, strings.NewReader(body))
	var out bytes.Buffer
	p := &proxy{out: json.NewEncoder(&bytes.Buffer{})}
	rec := record{}
	p.capture(&out, req, preq, decision, &rec)
	return out.String()
}

// TestCapture_AWildcardOverAZapierHostIsRefusedRatherThanAnswered is the
// commercially damaging case, stated as the sidecar decides it.
func TestCapture_AWildcardOverAZapierHostIsRefusedRatherThanAnswered(t *testing.T) {
	t.Parallel()
	resp := captureThrough(t,
		[]schema.EgressRule{{Host: "*.zapier.com", Mode: schema.ModeCapture}},
		"hooks.zapier.com", "/hooks/catch/1234/abcd", `{"email":"lead@example.test"}`)

	require.True(t, strings.HasPrefix(resp, "HTTP/1.1 403"),
		"a rule that covers zapier.com rather than naming hooks.zapier.com must not "+
			"answer as Zapier would; got %q", firstLineOf(resp))
	require.NotContains(t, resp, "X-Antifailure-Captured: true",
		"nothing was captured, so nothing may claim it was")
}

// TestCapture_AWildcardOverACRMHostIsRefusedRatherThanAnswered is the same
// question asked of HubSpot, because a delivery path usually names more than
// one third party and a reader should not have to assume the second behaves
// like the first.
func TestCapture_AWildcardOverACRMHostIsRefusedRatherThanAnswered(t *testing.T) {
	t.Parallel()
	resp := captureThrough(t,
		[]schema.EgressRule{{Host: "*.hubapi.com", Mode: schema.ModeCapture}},
		"api.hubapi.com", "/crm/v3/objects/contacts", `{"properties":{"email":"lead@example.test"}}`)

	require.True(t, strings.HasPrefix(resp, "HTTP/1.1 403"),
		"got %q", firstLineOf(resp))
}

// TestCapture_ANamedZapierHostIsRecordedAndAnswered is the other half, and it
// is what makes the refusal above a boundary rather than a blanket no.
func TestCapture_ANamedZapierHostIsRecordedAndAnswered(t *testing.T) {
	t.Parallel()
	resp := captureThrough(t,
		[]schema.EgressRule{{Host: "hooks.zapier.com", Mode: schema.ModeCapture}},
		"hooks.zapier.com", "/hooks/catch/1234/abcd", `{"email":"lead@example.test"}`)

	require.True(t, strings.HasPrefix(resp, "HTTP/1.1 200"),
		"a host somebody wrote down is consent to capture it; got %q", firstLineOf(resp))
	require.Contains(t, resp, "X-Antifailure-Captured: true")
}

// TestCapture_ZapierHasNoHandlerSoTheBodyItAnswersIsNotZapiersShape is a
// stated limit rather than a passing test dressed up as one.
//
// Zapier's catch hook answers {"status":"success","attempt":...,"id":...,
// "request_id":...}, and a client that checks status carries on. This build
// has no Zapier handler, so a NAMED zapier host is answered by the generic
// shape, an empty object. The message is recorded either way, which is what
// makes it the right guess for a host somebody wrote down, and it is not the
// provider's own shape and no report should say it is.
func TestCapture_ZapierHasNoHandlerSoTheBodyItAnswersIsNotZapiersShape(t *testing.T) {
	t.Parallel()
	h, known := captureHandlerFor("hooks.zapier.com", "/hooks/catch/1234/abcd")
	require.False(t, known, "if a Zapier handler is added, this test is the one to update")
	require.Equal(t, "unknown", h.name)

	var out bytes.Buffer
	h.respond(&out)
	require.Contains(t, out.String(), "{}",
		"the generic shape, which a client checking status does not read as success")
}

func firstLineOf(s string) string {
	if i := strings.Index(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}
