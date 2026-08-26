package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/mockpack"
	"github.com/antifailure/antifailure/engine/internal/policy"
	"github.com/antifailure/antifailure/engine/pkg/livekey"
)

// Sandbox mode swaps the credential and lets the request through to the
// provider's own sandbox.
//
// It is the mode that runs a billing flow end to end. Stripe's sandbox charges
// nothing and behaves like Stripe, which is worth more than any mock, so where
// a provider offers one it is used.
//
// The swap happens here rather than in the application's configuration for a
// reason worth stating: an application configured with a sandbox key is an
// application somebody can misconfigure. An environment that replaces the
// credential on the way out is one where the mistake cannot be made, because
// whatever the application sends is discarded before it leaves.

// tripwire refuses a request carrying a credential that works against
// production.
//
// This runs on every allowed request, in every mode, and it is a refusal
// rather than a redaction. Redaction protects the logs. This protects the
// customer whose card would have been charged.
func (p *proxy) tripwire(req *http.Request, host string) []livekey.Finding {
	found := livekey.ScanHeaders(req.Header)
	if len(found) > 0 {
		return found
	}
	// The query string carries them more often than it should, and a key in a
	// URL is a key in every access log between here and the origin.
	if q := req.URL.RawQuery; q != "" {
		if f := livekey.Scan(q, "the query string"); len(f) > 0 {
			return f
		}
	}
	_ = host
	return nil
}

// refuseLiveCredential writes the refusal for a tripped wire.
//
// It says what kind of credential it was and where it was, and never what it
// was. A message that echoed the key back would put it in the logs of the
// thing refusing it.
func refusalForLiveCredential(req policy.Request, found []livekey.Finding) string {
	var b strings.Builder
	b.WriteString("Antifailure refused this request because it carries a live credential.\n\n")
	b.WriteString("  " + req.String() + "\n\n")
	b.WriteString("  Found: " + livekey.Describe(found) + "\n\n")
	b.WriteString("An environment holds a copy of production data and runs unreviewed code, so a\n")
	b.WriteString("credential that can act on production must never reach it. Point this service at\n")
	b.WriteString("a test credential; if the host has a sandbox, set the rule to sandbox mode and\n")
	b.WriteString("the environment will substitute one for you.\n")
	return b.String()
}

// applySandbox replaces the credential on an outbound request.
//
// Which header to replace is decided per provider rather than generically,
// because the providers disagree: Stripe reads a bearer token, SendGrid reads
// one too, Twilio uses basic auth, and several read a header of their own
// invention. Replacing the wrong one leaves the original in place and forwards
// a request that still carries whatever the application sent.
func applySandbox(req *http.Request, host, credential string) {
	if credential == "" {
		return
	}
	switch {
	case strings.Contains(host, "stripe.com"),
		strings.Contains(host, "sendgrid.com"),
		strings.Contains(host, "resend.com"),
		strings.Contains(host, "openai.com"),
		strings.Contains(host, "anthropic.com"):
		req.Header.Set("Authorization", "Bearer "+credential)
	case strings.Contains(host, "postmarkapp.com"):
		req.Header.Set("X-Postmark-Server-Token", credential)
	case strings.Contains(host, "twilio.com"):
		// Basic auth, where the account identifier is the username. The
		// application's own username is kept, because the sandbox account is
		// still that account.
		if user, _, ok := req.BasicAuth(); ok {
			req.SetBasicAuth(user, credential)
		}
	default:
		// The common case for everything else. Both are set because a
		// provider that reads one usually ignores the other, and setting the
		// wrong one alone leaves the application's credential in place.
		req.Header.Set("Authorization", "Bearer "+credential)
		if req.Header.Get("X-Api-Key") != "" {
			req.Header.Set("X-Api-Key", credential)
		}
	}
}

// serveMock answers a request from a fixture pack, and reports whether it did.
//
// A miss is a refusal that names the request and hands back a route to paste
// in, rather than an empty 200. An application that receives nothing usually
// carries on and fails somewhere unrelated, which is the failure this mode
// exists to avoid rather than cause.
func (p *proxy) serveMock(w io.Writer, req *http.Request, host string, rec *record) bool {
	body, _ := io.ReadAll(io.LimitReader(req.Body, 1<<20))
	_ = req.Body.Close()

	resp, ok := p.mocks.Answer(host, req.Method, req.URL.Path, body)
	if !ok {
		rec.Status = http.StatusNotFound
		rec.Allowed = false
		rec.Reason = "No fixture matched this request, and mock mode answers only from fixtures."
		writeRawStatus(w, http.StatusNotFound, "text/plain; charset=utf-8",
			mockMissBody(host, req))
		return true
	}
	rec.Status = resp.Status
	rec.Pack = resp.Pack
	rec.Fixture = resp.Route
	rec.Bytes = int64(len(resp.Body))

	contentType := "application/json"
	if resp.Headers["Content-Type"] != "" {
		contentType = resp.Headers["Content-Type"]
	}
	writeRawStatus(w, resp.Status, contentType, string(resp.Body))
	return true
}

func mockMissBody(host string, req *http.Request) string {
	var b strings.Builder
	b.WriteString("Antifailure has no fixture for this request.\n\n")
	b.WriteString("  " + req.Method + " https://" + host + req.URL.Path + "\n\n")
	b.WriteString("This host is set to mock, which answers only from fixtures, so nothing was\n")
	b.WriteString("sent and nothing was invented. Add a route like this one to a pack and point\n")
	b.WriteString("the rule's fixtures field at it:\n\n")
	b.WriteString(mockpack.Skeleton(host, req.Method, req.URL.Path))
	b.WriteString("\n")
	return b.String()
}

func writeRawStatus(w io.Writer, status int, contentType, body string) {
	fmt.Fprintf(w,
		"HTTP/1.1 %d %s\r\nContent-Type: %s\r\nX-Antifailure-Mock: true\r\n"+
			"Content-Length: %d\r\nConnection: close\r\n\r\n%s",
		status, http.StatusText(status), contentType, len(body), body)
}
