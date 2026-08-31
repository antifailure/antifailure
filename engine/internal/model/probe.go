package model

// Proving a key works, and saying what is wrong when it does not.
//
// Somebody who has just pasted a key wants to know it works before they spend
// twenty minutes bringing an environment up. That is the whole reason this
// exists as a command rather than as something they find out from a run.
//
// The classification is the valuable half. "The model could not be reached" is
// true for a revoked key, an exhausted balance, a typo in a model name, a
// firewall, and a local model that is not running, and it is useless for all
// five: each one has a different fix and a person who is told only that the
// call failed will try the wrong one first. So every outcome this can tell
// apart is told apart, and every one of them carries the next thing to do.
//
// Nothing here prints the key, including on the error path, and that is not
// left to good intentions. A custom endpoint is a first class path in this
// product, a gateway is somebody else's code, and gateways do echo request
// headers into error bodies. So provider text is passed through a redaction
// that removes the key before it can reach a terminal, a log, or a support
// bundle.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Outcome is what a probe found.
type Outcome string

const (
	// OutcomeOK means the provider answered a real completion.
	OutcomeOK Outcome = "ok"
	// OutcomeKeyRejected means the key is not accepted.
	OutcomeKeyRejected Outcome = "key-rejected"
	// OutcomeNoCredit means the account cannot pay for the call.
	OutcomeNoCredit Outcome = "no-credit"
	// OutcomeUnknownModel means the model name is not one the endpoint serves.
	OutcomeUnknownModel Outcome = "unknown-model"
	// OutcomeRateLimited means the key is fine and the call was throttled.
	OutcomeRateLimited Outcome = "rate-limited"
	// OutcomeProviderDown means the endpoint answered with a server error.
	OutcomeProviderDown Outcome = "provider-down"
	// OutcomeUnreachable means nothing answered at all.
	OutcomeUnreachable Outcome = "unreachable"
	// OutcomeTimedOut means the endpoint accepted the connection and did not
	// answer in time.
	OutcomeTimedOut Outcome = "timed-out"
	// OutcomeUnreadable means something answered with a shape this could not
	// read, which is almost always a gateway that is not what it claims.
	OutcomeUnreadable Outcome = "unreadable"
)

// Result is one probe.
type Result struct {
	Outcome Outcome
	// Detail is what happened, in the provider's own words where there are
	// any, with the key redacted out.
	Detail string
	// NextStep is the one thing to do about it.
	NextStep string
	// Status is the HTTP status, or zero when nothing answered.
	Status int
	// Latency is how long the call took.
	Latency time.Duration
}

// OK reports whether the key works.
func (r Result) OK() bool { return r.Outcome == OutcomeOK }

// Probe sends one cheap completion and reports what came back.
//
// max_tokens is 1 and the prompt is two characters, because this is a command
// people are encouraged to run whenever they are unsure and it must never be a
// reason not to. It is a real call rather than a key-shape check: a well formed
// key that was revoked this morning passes every shape check there is.
func Probe(ctx context.Context, client *http.Client, cfg Config, now func() time.Time) Result {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	body, err := json.Marshal(requestFor(cfg))
	if err != nil {
		return Result{Outcome: OutcomeUnreadable, Detail: err.Error()}
	}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, cfg.Endpoint(), bytes.NewReader(body))
	if err != nil {
		return Result{
			Outcome:  OutcomeUnreachable,
			Detail:   err.Error(),
			NextStep: fmt.Sprintf("Check that %s is a URL.", cfg.BaseURL),
		}
	}
	req.Header.Set("content-type", "application/json")
	// The one place the key is revealed, on the way into a request.
	if cfg.Provider.Name == "anthropic" {
		req.Header.Set("x-api-key", cfg.Key.Reveal())
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("authorization", "Bearer "+cfg.Key.Reveal())
	}

	started := now()
	resp, err := client.Do(req)
	if err != nil {
		res := transportFailure(cfg, err)
		res.Latency = now().Sub(started)
		return res
	}
	defer func() { _ = resp.Body.Close() }()

	// Bounded, because an endpoint that is not what it claims can answer with
	// a great deal of HTML and this only needs the first error message in it.
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	res := classify(cfg, resp.StatusCode, payload)
	res.Status = resp.StatusCode
	res.Latency = now().Sub(started)
	// Every path out of here goes through this. A detail assembled from an
	// endpoint's response is untrusted text and the key is the one thing that
	// must never be in it.
	res.Detail = redactKey(res.Detail, cfg.Key.Reveal())
	return res
}

func requestFor(cfg Config) map[string]any {
	req := map[string]any{
		"model":      cfg.Model,
		"max_tokens": 1,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
	}
	if cfg.Provider.Name == "openai" {
		// max_tokens is deprecated for the newer OpenAI models and rejected by
		// some gateways that only implement the current shape. Both names are
		// sent: an endpoint that reads either gets a bounded response, and one
		// that rejects an unknown field would reject a great deal else too.
		req["max_completion_tokens"] = 1
	}
	return req
}

// transportFailure separates "nothing answered" from "it took too long".
//
// They read the same in a stack trace and they mean different things to the
// person: an unreachable endpoint is a firewall or a URL, and a timeout is
// usually a local model that is loading weights.
func transportFailure(cfg Config, err error) Result {
	host := hostOf(cfg.BaseURL)
	if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
		return Result{
			Outcome: OutcomeTimedOut,
			Detail:  fmt.Sprintf("%s accepted the connection and did not answer in time.", host),
			NextStep: "Raise the timeout with --timeout. A local model loading weights for " +
				"the first time routinely takes longer than a hosted one ever does.",
		}
	}
	next := fmt.Sprintf(
		"Check that this machine can reach %s. This call is made by af itself and "+
			"is not subject to any manifest's egress policy, so a 'default: block' "+
			"manifest is not what is stopping it.", host)
	if cfg.Custom() {
		next = fmt.Sprintf(
			"Check that %s is running and reachable from this machine. It is a "+
				"custom endpoint, so nothing is wrong with the provider.", cfg.BaseURL)
	}
	return Result{
		Outcome:  OutcomeUnreachable,
		Detail:   redactKey(err.Error(), ""),
		NextStep: next,
	}
}

func isTimeout(err error) bool {
	var t interface{ Timeout() bool }
	return errors.As(err, &t) && t.Timeout()
}

func hostOf(rawURL string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(rawURL, "https://"), "http://")
	if i := strings.IndexAny(s, "/:"); i > 0 {
		return s[:i]
	}
	return s
}

// classify turns a status and a body into something worth reading.
//
// The status alone is not enough and neither is the message alone. Anthropic
// reports an exhausted balance as a 400, which is the same status it uses for a
// malformed request; OpenAI reports it as a 429, which is the same status it
// uses for ordinary throttling. Both are distinguishable from the message, and
// telling somebody to wait and retry when their balance is empty wastes their
// afternoon.
func classify(cfg Config, status int, payload []byte) Result {
	message := providerMessage(payload)
	lower := strings.ToLower(message)

	switch {
	case status >= 200 && status < 300:
		if !readableCompletion(cfg, payload) {
			return Result{
				Outcome: OutcomeUnreadable,
				Detail: fmt.Sprintf(
					"%s answered %d with a body that is not a %s completion.",
					hostOf(cfg.BaseURL), status, cfg.Provider.Name),
				NextStep: fmt.Sprintf(
					"Point %s at an endpoint that speaks the %s API. A gateway has to "+
						"answer %s with the provider's own response shape.",
					cfg.Provider.BaseURLVar, cfg.Provider.Name, cfg.Provider.Path),
			}
		}
		return Result{
			Outcome: OutcomeOK,
			Detail: fmt.Sprintf("%s answered as %s.",
				hostOf(cfg.BaseURL), cfg.Model),
		}

	case status == http.StatusUnauthorized, status == http.StatusForbidden,
		strings.Contains(lower, "authentication_error"),
		strings.Contains(lower, "invalid api key"),
		strings.Contains(lower, "invalid x-api-key"):
		return Result{
			Outcome: OutcomeKeyRejected,
			Detail:  orDefault(message, fmt.Sprintf("the endpoint answered %d.", status)),
			NextStep: fmt.Sprintf(
				"The key is not accepted. Store the right one with 'af model set %s'. "+
					"A key that worked yesterday and does not today was revoked or rotated.",
				cfg.Provider.Name),
		}

	case status == http.StatusPaymentRequired,
		strings.Contains(lower, "credit balance"),
		strings.Contains(lower, "insufficient_quota"),
		strings.Contains(lower, "exceeded your current quota"),
		strings.Contains(lower, "billing"):
		return Result{
			Outcome: OutcomeNoCredit,
			Detail:  orDefault(message, "the account cannot pay for this call."),
			NextStep: fmt.Sprintf(
				"The key is valid and the account has nothing to spend. Add credit at %s. "+
					"Retrying will not help.", cfg.Provider.Name),
		}

	case status == http.StatusNotFound, strings.Contains(lower, "model"):
		// A 404 against a custom endpoint is much more often a gateway that
		// does not serve this path than a model that does not exist, and
		// sending a self-hoster to check their model name would be the wrong
		// half of the message.
		if cfg.Custom() && status == http.StatusNotFound {
			return Result{
				Outcome: OutcomeUnknownModel,
				Detail: orDefault(message, fmt.Sprintf(
					"%s answered 404 for %s.", cfg.BaseURL, cfg.Provider.Path)),
				NextStep: fmt.Sprintf(
					"Either %s does not serve %s, or it does not have a model called %q. "+
						"%s must be the base of the API, without %s on the end.",
					cfg.BaseURL, cfg.Provider.Path, cfg.Model,
					cfg.Provider.BaseURLVar, cfg.Provider.Path),
			}
		}
		return Result{
			Outcome: OutcomeUnknownModel,
			Detail:  orDefault(message, fmt.Sprintf("%s is not a model here.", cfg.Model)),
			NextStep: fmt.Sprintf(
				"Set %s to a model this key can use, or unset it to use %s.",
				ModelVar, cfg.Provider.DefaultModel),
		}

	case status == http.StatusTooManyRequests:
		return Result{
			Outcome: OutcomeRateLimited,
			Detail:  orDefault(message, "the endpoint is throttling this key."),
			NextStep: "The key works. This is a rate limit, so wait and run it again; " +
				"nothing needs to be changed.",
		}

	case status >= 500:
		return Result{
			Outcome: OutcomeProviderDown,
			Detail:  orDefault(message, fmt.Sprintf("the endpoint answered %d.", status)),
			NextStep: "This says nothing about the key. Run it again in a few minutes, " +
				"and check the provider's status page if it persists.",
		}
	}

	return Result{
		Outcome: OutcomeUnreadable,
		Detail: orDefault(message, fmt.Sprintf(
			"%s answered %d.", hostOf(cfg.BaseURL), status)),
		NextStep: fmt.Sprintf("Check that %s is the base of a %s compatible API.",
			cfg.BaseURL, cfg.Provider.Name),
	}
}

// readableCompletion reports whether a 200 is actually a completion.
//
// Asked because a 200 is not evidence on its own. A reverse proxy in front of a
// model that is not running answers 200 with an HTML error page, a
// misconfigured gateway answers 200 with an empty object, and both would
// otherwise be reported as a working key: the worst failure this command has,
// because it certifies a setup that will fail on the first real run.
func readableCompletion(cfg Config, payload []byte) bool {
	if cfg.Provider.Name == "anthropic" {
		var out struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal(payload, &out); err != nil {
			return false
		}
		return out.Type == "message" || len(out.Content) > 0
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return false
	}
	return len(out.Choices) > 0
}

// providerMessage digs the human sentence out of an error body.
//
// Both providers nest it under "error", and gateways vary: some copy the
// provider's shape, some return a bare {"message": ...}, some return a plain
// string. Each of those is read rather than only the first, because the
// sentence is the most useful thing on the screen when this fails and falling
// back to "the endpoint answered 400" throws it away.
func providerMessage(payload []byte) string {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return ""
	}

	var nested struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}
	if err := json.Unmarshal(trimmed, &nested); err == nil {
		for _, candidate := range []string{
			nested.Error.Message, nested.Message, nested.Detail, nested.Error.Type,
		} {
			if s := strings.TrimSpace(candidate); s != "" {
				return clip(s)
			}
		}
	}

	var plain string
	if err := json.Unmarshal(trimmed, &plain); err == nil && strings.TrimSpace(plain) != "" {
		return clip(plain)
	}

	// Not JSON at all, which is a gateway answering with HTML or a proxy
	// notice. Worth showing, clipped, because the first line of it usually
	// names what is really in front of the model.
	if trimmed[0] == '<' {
		return ""
	}
	return clip(string(trimmed))
}

func clip(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	const limit = 240
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}

// redactKey removes a key from text on its way to a person.
//
// The suffix is replaced too. A message that quotes only the last few
// characters of a key is not a leak in the way the whole key is, and it is
// still more than anything here has a reason to print.
func redactKey(text, key string) string {
	if text == "" {
		return text
	}
	if key != "" {
		text = strings.ReplaceAll(text, key, "[redacted]")
		if len(key) > 8 {
			text = strings.ReplaceAll(text, key[len(key)-8:], "[redacted]")
		}
	}
	return text
}

func orDefault(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
