package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Synth asks a model to invent a response, and marks everything downstream of
// it as unverified rather than passed.
//
// It is an escape hatch, not a mode to rely on, and the design says so at
// every turn. A workflow that touched a synthesized response reports
// unverified, which is neither a pass nor a failure, because the answer came
// from a model rather than from the thing being tested. The response header
// says so too, so an application logging it can tell.
//
// It exists because the alternative is worse. Somebody exploring a provider
// with no sandbox and no fixtures gets a refusal and stops; with this they get
// something shaped right, keep moving, and are told plainly that nothing they
// just saw is evidence.
//
// The key is the user's, read from this process's environment. With none, a
// synth rule refuses and says which variable to set.

// synthConfig is where a model is reached.
type synthConfig struct {
	provider string
	apiKey   string
	model    string
	baseURL  string
}

// synthFromEnvironment reads a configuration, or nothing.
func synthFromEnvironment(getenv func(string) string) *synthConfig {
	if key := getenv("ANTHROPIC_API_KEY"); key != "" {
		return &synthConfig{
			provider: "anthropic", apiKey: key,
			model:   orDefault(getenv("AF_MODEL"), "claude-sonnet-5"),
			baseURL: orDefault(getenv("ANTHROPIC_BASE_URL"), "https://api.anthropic.com"),
		}
	}
	if key := getenv("OPENAI_API_KEY"); key != "" {
		return &synthConfig{
			provider: "openai", apiKey: key,
			model:   orDefault(getenv("AF_MODEL"), "gpt-4.1"),
			baseURL: orDefault(getenv("OPENAI_BASE_URL"), "https://api.openai.com"),
		}
	}
	return nil
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// serveSynth answers a request from a model, and reports whether it did.
func (p *proxy) serveSynth(w io.Writer, req *http.Request, host string, rec *record) bool {
	if p.synth == nil {
		rec.Status = http.StatusForbidden
		rec.Allowed = false
		rec.Reason = "This host is set to synth and no model key is available."
		writeRawStatus(w, http.StatusForbidden, "text/plain; charset=utf-8",
			"Antifailure could not synthesize a response.\n\n"+
				"  "+req.Method+" https://"+host+req.URL.Path+"\n\n"+
				"This host is set to synth, which asks a model to invent a response. Set\n"+
				"ANTHROPIC_API_KEY or OPENAI_API_KEY, or set the rule to mock and write a\n"+
				"fixture, which is the better answer for anything you intend to rely on.\n")
		return true
	}

	body, _ := io.ReadAll(io.LimitReader(req.Body, 64<<10))
	_ = req.Body.Close()

	answer, err := p.synth.complete(synthPrompt(req, host, body))
	if err != nil {
		rec.Status = http.StatusBadGateway
		rec.Allowed = false
		rec.Error = err.Error()
		writeRawStatus(w, http.StatusBadGateway, "text/plain; charset=utf-8",
			"Antifailure could not reach the model to synthesize a response: "+err.Error()+"\n")
		return true
	}

	status, payload := parseSynth(answer)
	rec.Status = status
	rec.Bytes = int64(len(payload))
	rec.Synthesized = true

	// The header is not decoration. An application that logs its responses can
	// tell afterwards which ones were invented, and a workflow that touched
	// one reports unverified rather than passed.
	// Not checked: a failure here means the client hung up mid response.
	_, _ = fmt.Fprintf(w,
		"HTTP/1.1 %d %s\r\nContent-Type: application/json\r\n"+
			"X-Antifailure-Synthesized: true\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		status, http.StatusText(status), len(payload), payload)
	return true
}

// synthPrompt describes the request and asks for the provider's own shape.
func synthPrompt(req *http.Request, host string, body []byte) string {
	trimmed := string(body)
	if len(trimmed) > 4000 {
		trimmed = trimmed[:4000] + "\n... (truncated)"
	}
	return strings.Join([]string{
		"An application in a test environment made this request to a third party API.",
		"That API is unreachable, so invent the response it would most likely have given.",
		"",
		req.Method + " https://" + host + req.URL.RequestURI(),
		"",
		"Request body:",
		orDefault(trimmed, "(empty)"),
		"",
		"Answer with one JSON object and nothing else:",
		`{"status": <http status>, "body": <the response body as a JSON value>}`,
		"",
		"Use the shape this provider actually returns, including the fields its own",
		"client libraries parse. Use obviously fake values: example.test addresses,",
		"identifiers prefixed with the provider's own convention, and never anything",
		"that could be mistaken for real data.",
	}, "\n")
}

// parseSynth reads the model's answer, and gives up readably.
func parseSynth(raw string) (int, string) {
	candidates := []string{raw}
	if i, j := strings.Index(raw, "{"), strings.LastIndex(raw, "}"); i >= 0 && j > i {
		candidates = append([]string{raw[i : j+1]}, candidates...)
	}
	for _, candidate := range candidates {
		var out struct {
			Status int             `json:"status"`
			Body   json.RawMessage `json:"body"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(candidate)), &out); err != nil {
			continue
		}
		if out.Status == 0 {
			out.Status = 200
		}
		if len(out.Body) == 0 {
			out.Body = []byte("{}")
		}
		return out.Status, string(out.Body)
	}
	// A model that did not answer with a shape is reported rather than
	// guessed at. An empty 200 would let the application carry on with
	// nothing, which is the failure this mode is already closest to.
	return http.StatusBadGateway,
		`{"error":{"message":"Antifailure could not read the synthesized response."}}`
}

// complete sends one request to the model.
func (c *synthConfig) complete(prompt string) (string, error) {
	var body []byte
	var req *http.Request
	var err error

	client := &http.Client{Timeout: 60 * time.Second}
	if c.provider == "anthropic" {
		body, _ = json.Marshal(map[string]any{
			"model": c.model, "max_tokens": 1024,
			"messages": []map[string]string{{"role": "user", "content": prompt}},
		})
		req, err = http.NewRequest(http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("x-api-key", c.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		body, _ = json.Marshal(map[string]any{
			"model": c.model, "max_tokens": 1024,
			"messages": []map[string]string{{"role": "user", "content": prompt}},
		})
		req, err = http.NewRequest(http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("authorization", "Bearer "+c.apiKey)
	}
	req.Header.Set("content-type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("the model answered %d", resp.StatusCode)
	}

	if c.provider == "anthropic" {
		var out struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(payload, &out); err != nil {
			return "", err
		}
		var b strings.Builder
		for _, part := range out.Content {
			b.WriteString(part.Text)
		}
		return b.String(), nil
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("the model returned no choices")
	}
	return out.Choices[0].Message.Content, nil
}
