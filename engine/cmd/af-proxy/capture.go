package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/policy"
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
// would.
//
// It refuses when it has no handler for the host AND the rule that decided
// does not name that host. Both halves matter. A rule naming a host is
// somebody saying they want this captured, and generic capture is the right
// answer for a mail provider this build has never heard of: the message is
// recorded, the workflow carries on, and the shape is a guess the author asked
// for. A rule that merely covers a domain is not that consent, and answering
// 200 on its behalf is how *.amazonaws.com under a mail rule turned an S3 PUT
// into a delivered email.
func (p *proxy) capture(w io.Writer, req *http.Request, preq policy.Request, d policy.Decision, rec *record) {
	host := preq.Host
	h, known := captureHandlerFor(host, req.URL.Path)
	if !known && !d.NamesHost() {
		// Nothing is recorded. An entry in the inbox is a message somebody
		// can act on, and a message that was refused rather than accepted
		// would be read as one that arrived.
		rec.Status = http.StatusForbidden
		rec.Allowed = false
		rec.Reason = refusalReasonForUnhandledCapture(host, d)
		writeRawForbidden(w, refusalForUnhandledCapture(d, preq))
		return
	}

	body, err := io.ReadAll(io.LimitReader(req.Body, maxCaptured+1))
	if err != nil {
		rec.Error = err.Error()
		return
	}
	truncated := len(body) > maxCaptured
	if truncated {
		body = body[:maxCaptured]
	}
	_ = req.Body.Close()

	rec.Status = http.StatusOK
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
}

// whatDecided describes the thing that chose capture, for a sentence that has
// to read correctly whether a wildcard rule or the default made the choice.
func whatDecided(d policy.Decision) string {
	if d.RuleHost == "" {
		return "The default is capture, which names no host at all"
	}
	return "The rule for " + d.RuleHost + " covers a domain rather than naming this host"
}

// refusalReasonForUnhandledCapture is the one sentence the decision log gets.
func refusalReasonForUnhandledCapture(host string, d policy.Decision) string {
	return fmt.Sprintf(
		"%s was set to capture and this build has no capture handler for it. %s, so answering "+
			"as a provider would mean inventing a success for a service nobody named.",
		host, whatDecided(d))
}

// refusalForUnhandledCapture is what a developer reads in a stack trace.
func refusalForUnhandledCapture(d policy.Decision, req policy.Request) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Antifailure refused this request.\n\n")
	fmt.Fprintf(&b, "  %s\n\n", req.String())
	fmt.Fprintf(&b, "%s\n\n", d.Reason())
	fmt.Fprintf(&b, "Capture answers as the provider would, and this build has no capture handler for\n")
	fmt.Fprintf(&b, "%s. %s, so the only answer left is an\n", req.Host, whatDecided(d))
	fmt.Fprintf(&b, "invented success, and an invented success is believed: it is how an S3 PUT was\n")
	fmt.Fprintf(&b, "reported as a delivered email by a handler that thought it was holding one.\n\n")
	fmt.Fprintf(&b, "Name %s in a rule of its own if capturing it is what you meant, or give it a mode\n", req.Host)
	fmt.Fprintf(&b, "that says what should happen to it: block refuses it readably, mock answers from a\n")
	fmt.Fprintf(&b, "fixture, allow lets it through.\n\n")
	fmt.Fprintf(&b, "Ask about it with:\n\n  af net explain %s %s\n",
		req.Method, schemeOf(req)+"://"+req.Host+req.Path)
	return b.String()
}

// captureHandler knows one provider's request and response shapes.
type captureHandler struct {
	name    string
	kind    string
	parse   func(req *http.Request, body []byte) message
	respond func(w io.Writer)
}

// captureHandlerFor returns the handler for a host, and reports whether one
// was found rather than falling back to the generic shape.
//
// The second return is what makes the fallback a decision rather than a
// default. It used to be silent, and a silent fallback is exactly how a
// handler that believed it was holding an email came to answer for S3.
func captureHandlerFor(host, path string) (captureHandler, bool) {
	for _, h := range captureHandlers {
		if h.matches(host, path) {
			return h.handler, true
		}
	}
	return genericCapture, false
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
	{
		// SES v2 is JSON on a versioned path. Checked before the v1 entry
		// below, because both live on the same host and v1 answers for every
		// path that is not this one.
		matches: func(host, path string) bool {
			return isSESHost(host) && strings.HasPrefix(path, "/v2/email")
		},
		handler: captureHandler{
			name: "ses", kind: "email",
			parse: parseSESv2,
			// The v2 client reads MessageId off the response and an
			// application that stores it would otherwise store nothing.
			respond: jsonResponder(http.StatusOK, `{"MessageId":"af-captured-0000000000000000"}`),
		},
	},
	{
		// SES v1 is the query API: a form on /, with the operation in Action.
		//
		// The response is SendEmailResponse whatever the action was, and that
		// is a stated limit rather than an oversight: respond is handed a
		// writer and not the request, so it cannot see Action. SendEmail and
		// SendRawEmail are what an application sends, both carry a MessageId
		// in the same place, and a client parsing SendRawEmailResponse
		// specifically will not find its element. Naming that here is better
		// than a reader assuming it was checked.
		matches: func(host, _ string) bool { return isSESHost(host) },
		handler: captureHandler{
			name: "ses", kind: "email",
			parse: parseSESQuery,
			respond: xmlResponder(http.StatusOK,
				`<SendEmailResponse xmlns="https://email.amazonaws.com/doc/2010-03-31/">`+
					`<SendEmailResult><MessageId>af-captured-0000000000000000</MessageId></SendEmailResult>`+
					`<ResponseMetadata><RequestId>af-captured</RequestId></ResponseMetadata>`+
					`</SendEmailResponse>`),
		},
	},
	{
		// An incoming webhook answers with the two letters ok in plain text,
		// and a client that gets JSON instead reports a delivery failure for
		// a message that was captured perfectly well.
		matches: func(host, _ string) bool { return host == "hooks.slack.com" },
		handler: captureHandler{
			name: "slack", kind: "message",
			parse:   parseSlack,
			respond: func(w io.Writer) { writeCaptured(w, http.StatusOK, "text/plain", "ok") },
		},
	},
	{
		matches: func(host, _ string) bool {
			// The whole label, not the tail of one. HasSuffix alone would
			// answer for notslack.com, which is a host somebody else owns.
			return host == "slack.com" || strings.HasSuffix(host, ".slack.com")
		},
		handler: captureHandler{
			name: "slack", kind: "message",
			parse: parseSlack,
			// Every Slack Web API client checks ok before anything else, and
			// the empty object the generic handler used to return reads as
			// ok: false. A captured message looked like a failed one.
			respond: jsonResponder(http.StatusOK,
				`{"ok":true,"channel":"af-captured","ts":"0000000000.000000"}`),
		},
	},
}

// isSESHost reports whether a host is an SES API endpoint.
//
// email.<region>.amazonaws.com, and nothing else under amazonaws.com. The
// suffix is checked on the whole label rather than with Contains, because
// Contains("amazonaws.com") is the mistake this whole change exists to undo.
func isSESHost(host string) bool {
	return strings.HasPrefix(host, "email.") && strings.HasSuffix(host, ".amazonaws.com")
}

// genericCapture is what a host somebody named in a rule of its own gets when
// this build has no handler for it.
//
// It records the whole body rather than pretending to understand it, and
// answers 200 with an empty object. That is a guess, and it is the right guess
// where a person wrote the host down: the message is captured either way and
// the application usually carries on. It is the wrong guess where a wildcard
// swept the host in, which is why capture refuses that case rather than
// reaching this.
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

func xmlResponder(status int, body string) func(io.Writer) {
	return func(w io.Writer) { writeCaptured(w, status, "text/xml", body) }
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

// parseSESv2 reads the JSON body the v2 API takes.
func parseSESv2(_ *http.Request, body []byte) message {
	var payload struct {
		FromEmailAddress string `json:"FromEmailAddress"`
		Destination      struct {
			ToAddresses  []string `json:"ToAddresses"`
			CcAddresses  []string `json:"CcAddresses"`
			BccAddresses []string `json:"BccAddresses"`
		} `json:"Destination"`
		Content struct {
			Simple struct {
				Subject struct {
					Data string `json:"Data"`
				} `json:"Subject"`
				Body struct {
					Text struct {
						Data string `json:"Data"`
					} `json:"Text"`
					HTML struct {
						Data string `json:"Data"`
					} `json:"Html"`
				} `json:"Body"`
			} `json:"Simple"`
			Raw struct {
				Data string `json:"Data"`
			} `json:"Raw"`
		} `json:"Content"`
	}
	_ = json.Unmarshal(body, &payload)

	m := message{
		From:    payload.FromEmailAddress,
		Subject: payload.Content.Simple.Subject.Data,
		Text:    payload.Content.Simple.Body.Text.Data,
		HTML:    payload.Content.Simple.Body.HTML.Data,
	}
	m.To = append(m.To, payload.Destination.ToAddresses...)
	m.To = append(m.To, payload.Destination.CcAddresses...)
	m.To = append(m.To, payload.Destination.BccAddresses...)
	if m.Text == "" && m.HTML == "" && payload.Content.Raw.Data != "" {
		// A raw message is base64 MIME. It is kept as it arrived rather than
		// decoded here, because a half decoded MIME body in the inbox is
		// worse than an opaque one an agent can be told to decode.
		m.Text = payload.Content.Raw.Data
	}
	return m
}

// parseSESQuery reads the form the v1 query API takes.
//
// The recipient list is numbered from one, and the numbering is what makes
// this different from every other form provider here: Destination.ToAddresses
// with no index holds nothing at all.
func parseSESQuery(_ *http.Request, body []byte) message {
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return message{Text: string(body)}
	}
	m := message{
		From:    values.Get("Source"),
		Subject: values.Get("Message.Subject.Data"),
		Text:    values.Get("Message.Body.Text.Data"),
		HTML:    values.Get("Message.Body.Html.Data"),
	}
	for _, field := range []string{"ToAddresses", "CcAddresses", "BccAddresses"} {
		for i := 1; ; i++ {
			v := values.Get(fmt.Sprintf("Destination.%s.member.%d", field, i))
			if v == "" {
				break
			}
			m.To = append(m.To, v)
		}
	}
	if m.Text == "" && m.HTML == "" {
		m.Text = values.Get("RawMessage.Data")
	}
	return m
}

// parseSlack reads a chat.postMessage or an incoming webhook payload.
//
// Both shapes arrive here: the Web API takes JSON or a form and a webhook
// takes JSON, so the body is tried as JSON and read as a form when that finds
// no text.
func parseSlack(_ *http.Request, body []byte) message {
	var payload struct {
		Channel string          `json:"channel"`
		Text    string          `json:"text"`
		Blocks  json.RawMessage `json:"blocks"`
	}
	_ = json.Unmarshal(body, &payload)
	m := message{To: splitList(payload.Channel), Text: payload.Text}
	if m.Text == "" && len(payload.Blocks) > 0 {
		// Block Kit carries the words a workflow is waiting for, and dropping
		// them would leave a captured message with no body at all.
		m.Text = string(payload.Blocks)
	}
	if m.Text == "" {
		if values, formErr := url.ParseQuery(string(body)); formErr == nil {
			m.Text = values.Get("text")
			if m.To == nil {
				m.To = splitList(values.Get("channel"))
			}
		}
	}
	if m.Text == "" {
		m.Text = string(body)
	}
	return m
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
