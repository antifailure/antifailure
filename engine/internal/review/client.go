package review

// The provider client: the one place this package opens a connection, and it
// opens it exactly the way `af model test` does.
//
// It carries the user's own diff to the user's own model provider, with the
// user's own key, and nothing hosted by Antifailure is involved. That is the
// same BYOK trust model the model package already documents: the key stays on
// this machine and the call goes straight to the provider. The diff is the
// user's source, so sending it to the model the user chose is not a data
// boundary this product may cross on their behalf, it is the review they asked
// for.
//
// The call goes through the air gap guard, like every other outbound client in
// the engine. A guard test walks the source for a raw http.Client and fails the
// build if it finds one, so this builds its client with airgap.Client under a
// named site, and an air gapped installation refuses the call at the dial rather
// than reaching a provider it was sealed away from.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/model"
	"github.com/antifailure/antifailure/engine/pkg/airgap"
)

// reviewTimeout is how long one review call may take. A review reads a whole
// diff and writes structured findings, so it is a real completion rather than
// the one token probe, and it is given the room a real completion needs. It is
// still bounded, because a hung provider must not hold the run open.
const reviewTimeout = 120 * time.Second

// reviewMaxTokens bounds the model's answer. A review is a short list of
// concrete defects, not an essay, and a ceiling keeps a runaway response from
// spending the budget on prose the parser would discard anyway.
const reviewMaxTokens = 2048

// ProviderClient posts a review to a model provider through the air gap guard.
//
// It speaks both providers the model package declares, on the same two request
// shapes af model test uses: Anthropic's /v1/messages and OpenAI's
// /v1/chat/completions. The config decides which, so a user who set one key gets
// that provider with no further choice to make.
type ProviderClient struct {
	cfg  model.Config
	http *http.Client
}

// NewProviderClient builds the client for a resolved model configuration.
//
// The HTTP client is the guarded one, under the code reviewer's own air gap
// site, so a refusal in an air gapped run names the reviewer rather than some
// other outbound path, and the ledger attributes the call correctly.
func NewProviderClient(cfg model.Config) *ProviderClient {
	return &ProviderClient{cfg: cfg, http: airgap.Client(airgap.SiteReviewer, reviewTimeout)}
}

// Complete sends the prompts and returns the model's text.
//
// It returns an error for anything that is not a readable completion: a
// transport failure, a non-2xx status, or a body that is not the provider's own
// shape. The collector turns that error into a run note, never a finding,
// because a call that could not complete says nothing about the change. The key
// is redacted out of any error text, since a gateway can echo request headers
// into an error body.
func (c *ProviderClient) Complete(ctx context.Context, system, user string) (string, error) {
	body, err := json.Marshal(c.requestBody(system, user))
	if err != nil {
		return "", fmt.Errorf("building the review request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, c.cfg.Endpoint(), bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("building the review request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	// The one place the key is revealed, on the way into the request, exactly as
	// the model probe does it.
	if c.cfg.Provider.Name == "anthropic" {
		req.Header.Set("x-api-key", c.cfg.Key.Reveal())
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("authorization", "Bearer "+c.cfg.Key.Reveal())
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("the code reviewer could not reach the model: %s",
			c.redact(err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	// Bounded, because a gateway that is not what it claims can answer with a
	// great deal of HTML and a review response that fits in it is small.
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("the model answered %d to the code reviewer: %s",
			resp.StatusCode, c.redact(providerError(payload)))
	}

	text, ok := c.extractText(payload)
	if !ok {
		return "", fmt.Errorf(
			"the model answered the code reviewer with a body that is not the %s completion shape",
			c.cfg.Provider.Display)
	}
	return text, nil
}

// requestBody is the provider specific completion request.
func (c *ProviderClient) requestBody(system, user string) map[string]any {
	if c.cfg.Provider.Name == "anthropic" {
		return map[string]any{
			"model":      c.cfg.Model,
			"max_tokens": reviewMaxTokens,
			"system":     system,
			"messages":   []map[string]string{{"role": "user", "content": user}},
		}
	}
	// OpenAI and the gateways that speak its shape. Both token field names are
	// sent for the same reason the probe sends both: max_tokens is deprecated on
	// the newer models and max_completion_tokens is rejected by some older
	// gateways, and an endpoint that reads either gets a bounded response.
	return map[string]any{
		"model":                 c.cfg.Model,
		"max_tokens":            reviewMaxTokens,
		"max_completion_tokens": reviewMaxTokens,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	}
}

// extractText pulls the completion text out of the provider's response.
//
// Tolerant on this read boundary: it reads the provider's own shape and returns
// false when the body is not it, so a gateway answering 200 with an error page
// is reported as unreadable rather than parsed into an empty review that would
// look like a clean pass.
func (c *ProviderClient) extractText(payload []byte) (string, bool) {
	if c.cfg.Provider.Name == "anthropic" {
		var out struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(payload, &out); err != nil {
			return "", false
		}
		var b strings.Builder
		for _, part := range out.Content {
			if part.Type == "text" || part.Text != "" {
				b.WriteString(part.Text)
			}
		}
		if b.Len() == 0 {
			return "", false
		}
		return b.String(), true
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return "", false
	}
	if len(out.Choices) == 0 {
		return "", false
	}
	return out.Choices[0].Message.Content, true
}

// redact removes the key from text on its way into an error, the same guard the
// model probe applies: a message that quotes the key is the one thing an error
// path must never carry.
func (c *ProviderClient) redact(text string) string {
	key := c.cfg.Key.Reveal()
	if key == "" || text == "" {
		return text
	}
	text = strings.ReplaceAll(text, key, "[redacted]")
	if len(key) > 8 {
		text = strings.ReplaceAll(text, key[len(key)-8:], "[redacted]")
	}
	return text
}

// providerError digs the human sentence out of an error body, so a status alone
// is not all a note carries. Both providers nest it under "error"; a bare
// message and a plain string are read too, for the gateways that vary.
func providerError(payload []byte) string {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return "the endpoint returned no body"
	}
	var nested struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(trimmed, &nested); err == nil {
		for _, candidate := range []string{nested.Error.Message, nested.Message} {
			if s := strings.TrimSpace(candidate); s != "" {
				return clip(s, 240)
			}
		}
	}
	if trimmed[0] == '<' {
		return "the endpoint answered with a non-JSON body"
	}
	return clip(string(trimmed), 240)
}
