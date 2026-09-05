package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Verdict is what one probe found out.
type Verdict string

const (
	// Present: the control plane answered as something that has this route.
	Present Verdict = "present"
	// Absent: the control plane itself answered 404. This is the failure the
	// command exists for.
	Absent Verdict = "absent"
	// Unknown: this run did not find out. Never a pass.
	Unknown Verdict = "unknown"
	// NotProbed: the inventory says a probe of this route writes something and
	// the run was not told it could. Never a pass either.
	NotProbed Verdict = "not probed"
)

// Result is one row of the report.
type Result struct {
	Route   Route
	Verdict Verdict
	Status  int
	Detail  string
}

// probeAll asks a live origin about every route in the inventory.
func probeAll(origin string, routes []Route, allowWrites bool, timeout time.Duration, attempts int, out io.Writer) error {
	origin = strings.TrimRight(origin, "/")
	client := &http.Client{
		Timeout: timeout,
		// A redirect is an answer. Following it would ask a different server a
		// different question, and 302 is exactly how /auth/github says it is
		// there.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	_, _ = fmt.Fprintf(out, "\nasking %s what it actually serves\n\n", origin)
	results := make([]Result, 0, len(routes))
	for _, r := range routes {
		results = append(results, probeOne(client, origin, r, allowWrites, attempts))
	}

	width := 0
	for _, res := range results {
		if n := len(res.Route.Method + " " + res.Route.Path); n > width {
			width = n
		}
	}
	for _, res := range results {
		status := "   -"
		if res.Status != 0 {
			status = fmt.Sprintf("%4d", res.Status)
		}
		_, _ = fmt.Fprintf(out, "  %-*s  %s  %-10s  %s\n", width, res.Route.Method+" "+res.Route.Path, status, res.Verdict, res.Detail)
	}

	var absent, unknown, notProbed []Result
	for _, res := range results {
		switch res.Verdict {
		case Absent:
			absent = append(absent, res)
		case Unknown:
			unknown = append(unknown, res)
		case NotProbed:
			notProbed = append(notProbed, res)
		}
	}

	var b strings.Builder
	if len(absent) > 0 {
		fmt.Fprintf(&b, "\n%s does not serve %d route(s) this site calls:\n", origin, len(absent))
		for _, res := range absent {
			fmt.Fprintf(&b, "\n  %s %s\n", res.Route.Method, res.Route.Path)
			fmt.Fprintf(&b, "    called from www/%s\n", res.Route.CalledFrom)
			fmt.Fprintf(&b, "    %s\n", res.Route.WhenMissingLine())
		}
		b.WriteString("\nThe site publishes on every merge to main and the control plane only moves on a\n")
		b.WriteString("`v*` tag promoted to production, so a route merged but not yet released is live\n")
		b.WriteString("on the front end and missing on the back. Promote the control plane before\n")
		b.WriteString("publishing a site that calls it.\n")
	}
	if len(unknown) > 0 {
		fmt.Fprintf(&b, "\n%d route(s) could not be established either way, which is not a pass:\n", len(unknown))
		for _, res := range unknown {
			fmt.Fprintf(&b, "  %s %s: %s\n", res.Route.Method, res.Route.Path, res.Detail)
		}
	}
	if len(notProbed) > 0 {
		fmt.Fprintf(&b, "\n%d route(s) were NOT CHECKED:\n", len(notProbed))
		for _, res := range notProbed {
			fmt.Fprintf(&b, "  %s %s: %s\n", res.Route.Method, res.Route.Path, res.Detail)
		}
		b.WriteString("\nPass -allow-write-probes to send them, having read what the inventory says each\n")
		b.WriteString("one costs. They are refused rather than skipped because a route this command\n")
		b.WriteString("quietly passed over is a route nothing checks at all.\n")
	}
	if b.Len() > 0 {
		return errors.New(b.String())
	}
	_, _ = fmt.Fprintf(out, "\nall %d route(s) the site calls are served by %s\n", len(results), origin)
	return nil
}

// WhenMissingLine is the sentence describing what a person loses. It lives on
// Route rather than here so the inventory owns the wording.
func (r Route) WhenMissingLine() string {
	if r.WhenMissing != "" {
		return r.WhenMissing
	}
	return "the site calls it and it is not there"
}

func probeOne(client *http.Client, origin string, r Route, allowWrites bool, attempts int) Result {
	if !r.Inert() && !allowWrites {
		return Result{Route: r, Verdict: NotProbed, Detail: "the inventory says probing it writes something: " + firstSentence(r.ProbeReason)}
	}

	var (
		last       string
		lastStatus int
	)
	for attempt := 1; attempt <= attempts; attempt++ {
		res, retry, detail := attemptOne(client, origin, r)
		if !retry {
			return res
		}
		last, lastStatus = detail, res.Status
		if attempt < attempts {
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
		}
	}
	return Result{Route: r, Verdict: Unknown, Status: lastStatus, Detail: fmt.Sprintf("no usable answer in %d attempts: %s", attempts, last)}
}

// attemptOne sends one probe. The bool says whether the caller should try
// again: a transport failure and a 5xx are both worth a retry, and neither is
// ever reported as a pass if the retries run out.
func attemptOne(client *http.Client, origin string, r Route) (Result, bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), client.Timeout)
	defer cancel()

	var body io.Reader
	if r.Method == "POST" {
		// The body every inert POST probe sends. It is valid JSON, so a route
		// that refuses malformed JSON is not answered by the wrong branch, and
		// it carries no field any handler here accepts, so validation refuses
		// it on its first check.
		body = strings.NewReader(`{}`)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, origin+r.Path, body)
	if err != nil {
		return Result{Route: r, Verdict: Unknown, Detail: err.Error()}, false, err.Error()
	}
	if r.Method == "POST" {
		req.Header.Set("content-type", "application/json")
	}
	// No Origin header, deliberately. Three of these routes are guarded by an
	// origin check that refuses anything that is not the marketing site, and
	// that refusal is what makes the probe unable to reach the handler. Sending
	// an Origin would defeat the one property that makes this safe.
	//
	// No x-request-id either. The control plane honours a caller-supplied one,
	// so sending one would put our own value in the response and destroy the
	// only evidence that the application, rather than something in front of it,
	// answered.
	req.Header.Set("user-agent", "antifailure-routecheck/1 (+deployed route contract gate)")
	req.Header.Set("accept", "application/json, text/html;q=0.5")

	resp, err := client.Do(req)
	if err != nil {
		return Result{Route: r}, true, "transport: " + err.Error()
	}
	defer resp.Body.Close()
	preview, _ := io.ReadAll(io.LimitReader(resp.Body, 512))

	if resp.Header.Get("x-request-id") == "" {
		// Every response the control plane produces carries one, minted by the
		// middleware that runs before routing, including both of its 404s. A
		// reply without one did not come from the application: a WAF, a CDN
		// error page, or an ingress with no revision behind it. It cannot be
		// read as either answer.
		detail := fmt.Sprintf("HTTP %d with no x-request-id, so the control plane did not answer this: %s", resp.StatusCode, oneLine(preview))
		return Result{Route: r, Verdict: Unknown, Status: resp.StatusCode, Detail: detail}, true, detail
	}
	if resp.StatusCode >= 500 {
		detail := fmt.Sprintf("HTTP %d: %s", resp.StatusCode, oneLine(preview))
		return Result{Route: r, Verdict: Unknown, Status: resp.StatusCode, Detail: detail}, true, detail
	}
	if resp.StatusCode == http.StatusNotFound {
		// The control plane answers 404 for a path it does not have AND for a
		// path it has under a different method, which is why the probe uses the
		// method the site itself uses. For these four routes a 404 cannot mean
		// anything else: each one refuses this exact request with 400, 403, 429
		// or a redirect when it is present, and none of them has a branch that
		// answers 404. That is a property of the four handlers, recorded in the
		// inventory's probeReason, not a general rule about HTTP.
		return Result{Route: r, Verdict: Absent, Status: 404, Detail: "the control plane answered 404 to the request the site makes"}, false, ""
	}
	return Result{
		Route:   r,
		Verdict: Present,
		Status:  resp.StatusCode,
		Detail:  "answered as a route that exists: " + oneLine(preview),
	}, false, ""
}

func oneLine(b []byte) string {
	s := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(string(b), "\n", " "), "\r", ""))
	if s == "" {
		return "(no body)"
	}
	if len(s) > 90 {
		return s[:90] + "..."
	}
	return s
}

func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i+1]
	}
	return s
}
