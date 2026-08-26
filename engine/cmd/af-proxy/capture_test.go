package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func parseWith(t *testing.T, host, path, body string) message {
	t.Helper()
	h := captureHandlerFor(host, path)
	req, err := http.NewRequest(http.MethodPost, "https://"+host+path, strings.NewReader(body))
	require.NoError(t, err)
	m := h.parse(req, []byte(body))
	m.Provider = h.name
	if m.Kind == "" {
		m.Kind = h.kind
	}
	m.Links = extractLinks(m.Text + "\n" + m.HTML)
	m.Code = extractCode(m.Text + "\n" + m.HTML + "\n" + m.Subject)
	return m
}

func TestCapture_Resend(t *testing.T) {
	t.Parallel()
	m := parseWith(t, "api.resend.com", "/emails", `{
		"from":"Shopfront <hello@shopfront.test>",
		"to":["a@example.test","b@example.test"],
		"subject":"Confirm your email",
		"html":"<p><a href=\"https://shopfront.test/verify?token=abc\">Confirm</a></p>"
	}`)
	require.Equal(t, "resend", m.Provider)
	require.Equal(t, "email", m.Kind)
	require.Equal(t, "Shopfront <hello@shopfront.test>", m.From)
	require.Equal(t, []string{"a@example.test", "b@example.test"}, m.To)
	require.Equal(t, "Confirm your email", m.Subject)
	require.Equal(t, "https://shopfront.test/verify?token=abc", firstLink(m))
}

func TestCapture_ResendAcceptsAStringRecipient(t *testing.T) {
	t.Parallel()
	// Resend takes a string or an array, and a decoder that insists on one of
	// them drops the other silently. The shape of external data is not a
	// guess, and a captured message with no recipient is a workflow that
	// waits forever.
	m := parseWith(t, "api.resend.com", "/emails", `{"to":"one@example.test","subject":"x"}`)
	require.Equal(t, []string{"one@example.test"}, m.To)
}

func TestCapture_SendGrid(t *testing.T) {
	t.Parallel()
	m := parseWith(t, "api.sendgrid.com", "/v3/mail/send", `{
		"from":{"email":"hello@shopfront.test"},
		"personalizations":[{"to":[{"email":"a@example.test"}],"subject":"Your code"}],
		"content":[{"type":"text/plain","value":"Your code is 481920"},
		           {"type":"text/html","value":"<b>481920</b>"}]
	}`)
	require.Equal(t, "sendgrid", m.Provider)
	require.Equal(t, "hello@shopfront.test", m.From)
	require.Equal(t, []string{"a@example.test"}, m.To)
	require.Equal(t, "Your code", m.Subject, "the subject can live on the personalization")
	require.Equal(t, "Your code is 481920", m.Text)
	require.Equal(t, "481920", m.Code)
}

func TestCapture_Postmark(t *testing.T) {
	t.Parallel()
	m := parseWith(t, "api.postmarkapp.com", "/email", `{
		"From":"hello@shopfront.test","To":"a@example.test, b@example.test",
		"Subject":"Reset your password","TextBody":"Go to https://shopfront.test/reset?t=xyz"
	}`)
	require.Equal(t, "postmark", m.Provider)
	require.Equal(t, []string{"a@example.test", "b@example.test"}, m.To)
	require.Equal(t, "https://shopfront.test/reset?t=xyz", firstLink(m))
}

func TestCapture_MailgunTakesAForm(t *testing.T) {
	t.Parallel()
	m := parseWith(t, "api.mailgun.net", "/v3/shopfront.test/messages",
		"from=hello%40shopfront.test&to=a%40example.test&subject=Hello&text=Hi+there")
	require.Equal(t, "mailgun", m.Provider)
	require.Equal(t, "hello@shopfront.test", m.From)
	require.Equal(t, "Hi there", m.Text)
}

func TestCapture_TwilioIsSMSNotEmail(t *testing.T) {
	t.Parallel()
	m := parseWith(t, "api.twilio.com", "/2010-04-01/Accounts/AC123/Messages.json",
		"From=%2B15550000000&To=%2B15551111111&Body=Your+code+is+481920")
	require.Equal(t, "twilio", m.Provider)
	require.Equal(t, "sms", m.Kind, "an SMS is not an email and a workflow waiting for one should know")
	require.Equal(t, "481920", m.Code)
}

func TestCapture_UnknownProviderKeepsTheBody(t *testing.T) {
	t.Parallel()
	// Recording it is better than refusing it. The message is captured either
	// way and the application usually carries on.
	m := parseWith(t, "mail.someone-else.test", "/send", `{"anything":"at all"}`)
	require.Equal(t, "unknown", m.Provider)
	require.Contains(t, m.Text, "anything")
}

func TestExtractLinks_PutsTheOneTheWorkflowNeedsFirst(t *testing.T) {
	t.Parallel()
	// An agent following a magic link wants the one that signs it in, not the
	// unsubscribe footer, and document order gives whatever the template used.
	links := extractLinks(`
		<a href="https://shopfront.test/unsubscribe">Unsubscribe</a>
		<a href="https://shopfront.test/privacy">Privacy</a>
		<a href="https://shopfront.test/verify?token=Ax91ZqLm44PPqrs82kdl">Confirm your email</a>
	`)
	require.Equal(t, "https://shopfront.test/verify?token=Ax91ZqLm44PPqrs82kdl", links[0])
	require.Contains(t, links, "https://shopfront.test/unsubscribe")
}

func TestExtractLinks_CleansUpWhatItFinds(t *testing.T) {
	t.Parallel()
	links := extractLinks("Visit https://shopfront.test/go?a=1&amp;b=2 now, or https://shopfront.test/go?a=1&amp;b=2.")
	require.Equal(t, []string{"https://shopfront.test/go?a=1&b=2"}, links,
		"entities are decoded, trailing punctuation is dropped, and duplicates collapse")
	require.Empty(t, extractLinks("no links here"))
}

func TestExtractCode_FindsAOneTimeCode(t *testing.T) {
	t.Parallel()
	require.Equal(t, "481920", extractCode("Your code is 481920"))
	require.Equal(t, "4821", extractCode("Enter 4821 to continue"))
	require.Empty(t, extractCode("nothing numeric here"))
}

func TestExtractCode_SkipsAYear(t *testing.T) {
	t.Parallel()
	// Every transactional email has a copyright footer, and a four digit year
	// looks exactly like a four digit code. Returning 2026 as the code is how
	// an agent types the wrong thing into the form and reports a failure.
	require.Equal(t, "739104", extractCode("© 2026 Shopfront. Your code is 739104."))
}

func TestRespond_ReturnsTheShapeEachClientParses(t *testing.T) {
	t.Parallel()
	// An application that gets the wrong shape from its mail provider often
	// carries on and fails three steps later, in a way that looks like an
	// application bug.
	cases := map[string]struct{ host, wantStatus, wantBody string }{
		"resend":   {"api.resend.com", "200", `"id"`},
		"sendgrid": {"api.sendgrid.com", "202", ""},
		"postmark": {"api.postmarkapp.com", "200", `"ErrorCode":0`},
		"mailgun":  {"api.mailgun.net", "200", `"message"`},
		"twilio":   {"api.twilio.com", "201", `"sid"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var b strings.Builder
			path := "/send"
			if name == "twilio" {
				path = "/2010-04-01/Accounts/AC1/Messages.json"
			}
			captureHandlerFor(tc.host, path).respond(&b)
			out := b.String()
			require.Contains(t, out, "HTTP/1.1 "+tc.wantStatus)
			require.Contains(t, out, "X-Antifailure-Captured: true")
			if tc.wantBody != "" {
				require.Contains(t, out, tc.wantBody)
			}
		})
	}
}

func TestItoa_MatchesTheStandardLibrary(t *testing.T) {
	t.Parallel()
	// Hand written because the sidecar's build is standard library only and
	// this keeps the response writer allocation free; it still has to be right.
	for _, n := range []int{0, 1, 9, 10, 99, 100, 200, 202, 404, 12345} {
		require.Equal(t, itoaReference(n), itoa(n))
	}
}

func itoaReference(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// firstLink is what an agent reads, and what the engine side exposes as Link.
func firstLink(m message) string {
	if len(m.Links) == 0 {
		return ""
	}
	return m.Links[0]
}
