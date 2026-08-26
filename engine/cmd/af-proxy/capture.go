package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Capture answers a message provider without sending anything.
//
// This is the mode that makes an agent able to finish a sign up. A welcome
// email, a magic link, a one time code: the workflow is waiting on one, and in
// a preview environment nobody should receive it. So the request is read, the
// message is recorded, and the provider's documented success shape is returned
// so the application's own error handling never fires.
//
// Returning the right shape matters more than it sounds. An application that
// gets a 200 with the wrong body from its mail provider often carries on and
// fails somewhere else entirely, three steps later, in a way that looks like
// an application bug. Each provider below returns what its own client library
// expects to parse.
//
// The message is written to the decision log rather than to a file, for the
// same reason the decisions are: a file needs a volume, a volume needs
// cleaning up, and a volume is one more thing that can outlive the environment.

// message is one captured message, as it appears in the log.
type message struct {
	Event    string   `json:"event"`
	Env      string   `json:"env,omitempty"`
	At       string   `json:"at"`
	Seq      uint64   `json:"seq"`
	Provider string   `json:"provider"`
	Kind     string   `json:"kind"`
	From     string   `json:"from,omitempty"`
	To       []string `json:"to,omitempty"`
	Subject  string   `json:"subject,omitempty"`
	Text     string   `json:"text,omitempty"`
	HTML     string   `json:"html,omitempty"`
	// Links are every URL found in the body, most likely first. An agent
	// following a magic link needs exactly this and should not have to parse
	// HTML to get it.
	Links []string `json:"links,omitempty"`
	// Code is a one time code found in the body, if there is one.
	Code string `json:"code,omitempty"`
	Host string `json:"host"`
	Path string `json:"path"`
}

// maxCaptured bounds what is kept from one message.
//
// A marketing email with an inlined image is not what this is for, and a log
// line the size of an image makes every other line unreadable.
const maxCaptured = 128 << 10

// capture reads a request, records the message, and answers as the provider
// would. It reports whether it handled the request.
func (p *proxy) capture(w io.Writer, req *http.Request, host string) bool {
	body, err := io.ReadAll(io.LimitReader(req.Body, maxCaptured+1))
	if err != nil {
		return false
	}
	truncated := len(body) > maxCaptured
	if truncated {
		body = body[:maxCaptured]
	}
	_ = req.Body.Close()

	h := captureHandlerFor(host, req.URL.Path)
	msg := h.parse(req, body)
	msg.Event = "message"
	msg.Provider = h.name
	msg.Host = host
	msg.Path = req.URL.Path
	if msg.Kind == "" {
		msg.Kind = h.kind
	}
	msg.Links = extractLinks(msg.Text + "\n" + msg.HTML)
	msg.Code = extractCode(msg.Text + "\n" + msg.HTML + "\n" + msg.Subject)

	p.emitMessage(msg)
	h.respond(w)
	return true
}

// captureHandler knows one provider's request and response shapes.
type captureHandler struct {
	name    string
	kind    string
	parse   func(req *http.Request, body []byte) message
	respond func(w io.Writer)
}

func captureHandlerFor(host, path string) captureHandler {
	for _, h := range captureHandlers {
		if h.matches(host, path) {
			return h.handler
		}
	}
	return genericCapture
}

var captureHandlers = []struct {
	matches func(host, path string) bool
	handler captureHandler
}{
	{
		matches: func(host, _ string) bool { return strings.Contains(host, "resend.com") },
		handler: captureHandler{
			name: "resend", kind: "email",
			parse: parseResend,
			// Resend answers with the id its client returns to the caller. An
			// application that stores it would otherwise store an empty
			// string and fail on a later lookup.
			respond: jsonResponder(http.StatusOK, `{"id":"af_captured_00000000-0000-4000-8000-000000000000"}`),
		},
	},
	{
		matches: func(host, _ string) bool { return strings.Contains(host, "sendgrid.com") },
		handler: captureHandler{
			name: "sendgrid", kind: "email",
			parse: parseSendGrid,
			// 202 with no body, which is what SendGrid returns and what its
			// client checks for.
			respond: func(w io.Writer) { writeCaptured(w, http.StatusAccepted, "", "") },
		},
	},
	{
		matches: func(host, _ string) bool { return strings.Contains(host, "postmarkapp.com") },
		handler: captureHandler{
			name: "postmark", kind: "email",
			parse: parsePostmark,
			respond: jsonResponder(http.StatusOK,
				`{"To":"captured","SubmittedAt":"2000-01-01T00:00:00Z","MessageID":"af-captured","ErrorCode":0,"Message":"OK"}`),
		},
	},
	{
		matches: func(host, _ string) bool { return strings.Contains(host, "mailgun.net") },
		handler: captureHandler{
			name: "mailgun", kind: "email",
			parse:   parseFormEmail,
			respond: jsonResponder(http.StatusOK, `{"id":"<af-captured@antifailure>","message":"Queued. Thank you."}`),
		},
	},
	{
		matches: func(host, path string) bool {
			return strings.Contains(host, "twilio.com") && strings.Contains(path, "Messages")
		},
		handler: captureHandler{
			name: "twilio", kind: "sms",
			parse: parseTwilio,
			respond: jsonResponder(http.StatusCreated,
				`{"sid":"SMafcaptured00000000000000000000","status":"queued","error_code":null}`),
		},
	},
}

// genericCapture is what an unrecognised provider gets.
//
// It records the whole body rather than pretending to understand it, and
// answers 200 with an empty object. That is a guess, and it is a better guess
// than a refusal: the message is captured either way, and the application
// usually carries on.
var genericCapture = captureHandler{
	name: "unknown", kind: "message",
	parse: func(req *http.Request, body []byte) message {
		return message{Text: string(body)}
	},
	respond: jsonResponder(http.StatusOK, `{}`),
}

func jsonResponder(status int, body string) func(io.Writer) {
	return func(w io.Writer) { writeCaptured(w, status, "application/json", body) }
}

func writeCaptured(w io.Writer, status int, contentType, body string) {
	header := "HTTP/1.1 " + itoa(status) + " " + http.StatusText(status) + "\r\n"
	if contentType != "" {
		header += "Content-Type: " + contentType + "\r\n"
	}
	header += "X-Antifailure-Captured: true\r\n"
	header += "Content-Length: " + itoa(len(body)) + "\r\n"
	header += "Connection: close\r\n\r\n"
	_, _ = io.WriteString(w, header+body)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func parseResend(_ *http.Request, body []byte) message {
	var payload struct {
		From    string          `json:"from"`
		To      json.RawMessage `json:"to"`
		Subject string          `json:"subject"`
		HTML    string          `json:"html"`
		Text    string          `json:"text"`
	}
	_ = json.Unmarshal(body, &payload)
	return message{
		From: payload.From, To: recipients(payload.To),
		Subject: payload.Subject, HTML: payload.HTML, Text: payload.Text,
	}
}

func parseSendGrid(_ *http.Request, body []byte) message {
	var payload struct {
		From struct {
			Email string `json:"email"`
		} `json:"from"`
		Subject          string `json:"subject"`
		Personalizations []struct {
			To []struct {
				Email string `json:"email"`
			} `json:"to"`
			Subject string `json:"subject"`
		} `json:"personalizations"`
		Content []struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"content"`
	}
	_ = json.Unmarshal(body, &payload)

	m := message{From: payload.From.Email, Subject: payload.Subject}
	for _, pers := range payload.Personalizations {
		if m.Subject == "" {
			m.Subject = pers.Subject
		}
		for _, to := range pers.To {
			m.To = append(m.To, to.Email)
		}
	}
	for _, c := range payload.Content {
		switch c.Type {
		case "text/html":
			m.HTML = c.Value
		default:
			m.Text = c.Value
		}
	}
	return m
}

func parsePostmark(_ *http.Request, body []byte) message {
	var payload struct {
		From      string `json:"From"`
		To        string `json:"To"`
		Subject   string `json:"Subject"`
		HTMLBody  string `json:"HtmlBody"`
		TextBody  string `json:"TextBody"`
		MessageID string `json:"MessageStream"`
	}
	_ = json.Unmarshal(body, &payload)
	return message{
		From: payload.From, To: splitList(payload.To), Subject: payload.Subject,
		HTML: payload.HTMLBody, Text: payload.TextBody,
	}
}

// parseFormEmail handles the providers that take a form rather than JSON.
func parseFormEmail(_ *http.Request, body []byte) message {
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return message{Text: string(body)}
	}
	return message{
		From: values.Get("from"), To: splitList(values.Get("to")),
		Subject: values.Get("subject"), Text: values.Get("text"), HTML: values.Get("html"),
	}
}

func parseTwilio(_ *http.Request, body []byte) message {
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return message{Text: string(body)}
	}
	return message{
		From: values.Get("From"), To: splitList(values.Get("To")),
		Text: values.Get("Body"), Kind: "sms",
	}
}

// recipients accepts the several shapes a to field arrives in.
//
// Resend takes a string or an array, and a decoder that insists on one of them
// drops the other silently. The shape of external data is not a guess.
func recipients(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return splitList(one)
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return many
	}
	return nil
}

func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// linkPattern finds URLs in text and in HTML attributes alike.
var linkPattern = regexp.MustCompile(`https?://[^\s"'<>)\]]+`)

// extractLinks returns the URLs in a body, most likely first.
//
// The ordering is the useful part. An agent following a magic link wants the
// one that signs it in, not the unsubscribe footer, and a list in document
// order puts them in whatever order the template happened to use.
func extractLinks(body string) []string {
	found := linkPattern.FindAllString(body, -1)
	if len(found) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, raw := range found {
		link := strings.TrimRight(raw, ".,;:!?")
		link = strings.ReplaceAll(link, "&amp;", "&")
		if seen[link] {
			continue
		}
		seen[link] = true
		out = append(out, link)
	}
	sort.SliceStable(out, func(i, j int) bool { return linkScore(out[i]) > linkScore(out[j]) })
	return out
}

// linkScore ranks a URL by how likely it is to be the one the workflow needs.
func linkScore(link string) int {
	lower := strings.ToLower(link)
	score := 0
	for _, want := range []string{
		"verify", "confirm", "magic", "login", "signin", "sign-in", "activate",
		"reset", "invite", "token", "auth",
	} {
		if strings.Contains(lower, want) {
			score += 10
		}
	}
	for _, avoid := range []string{"unsubscribe", "preferences", "privacy", "terms", "twitter", "facebook"} {
		if strings.Contains(lower, avoid) {
			score -= 20
		}
	}
	// A long opaque segment is usually a token rather than a marketing link.
	for _, seg := range strings.Split(link, "/") {
		if len(seg) >= 20 && !strings.Contains(seg, ".") {
			score += 5
		}
	}
	return score
}

// codePattern finds a standalone run of digits, which is what a one time code
// looks like in every email that carries one.
var codePattern = regexp.MustCompile(`\b(\d{4,8})\b`)

// extractCode returns a one time code from a body.
//
// Years and amounts are the false positives that matter, so a run of digits
// that is part of a longer number, or that reads as a year, is skipped.
func extractCode(body string) string {
	for _, m := range codePattern.FindAllStringSubmatch(body, -1) {
		code := m[1]
		if len(code) == 4 {
			if n := atoiSafe(code); n >= 1900 && n <= 2200 {
				continue // a year
			}
		}
		return code
	}
	return ""
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return -1
		}
		n = n*10 + int(r-'0')
	}
	return n
}
