package secrets

// One HTTP client for four stores, so that the parts every adapter gets wrong
// are got right once.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Three of those parts are worth naming.
//
// A timeout that is on the request rather than only on the client, because a
// store that accepts a connection and then never answers is the failure mode
// that hangs an af up for ever, and a client timeout does not cover a body that
// arrives one byte at a time.
//
// A bounded read of the response, because these are secret stores and a
// response is a JSON document with one value in it. An unbounded ReadAll is how
// a misconfigured address pointed at something that streams turns a lookup into
// an out of memory kill.
//
// A body that never reaches an error message. Every one of these responses can
// contain the secret, so the errors here carry the status and the store's own
// error field and never the document. That is the same rule the dotenv parser
// follows when it refuses to quote a line it could not parse.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// httpTimeout bounds a single request.
//
// Ten seconds, because this is on the path of af up and a developer waiting for
// an environment should be told a store is unreachable rather than watching a
// spinner. A store that needs longer than ten seconds to return one secret is a
// store that is already failing.
const httpTimeout = 10 * time.Second

// maxBody bounds the response.
//
// A megabyte. The largest legitimate response here is a secret holding a
// certificate chain, which is measured in kilobytes.
const maxBody = 1 << 20

// client is the shared transport.
//
// One client rather than one per adapter, so connections are reused across the
// several lookups a single af up makes. The timeout is deliberately longer than
// httpTimeout: the per-request context is the real bound, and a client timeout
// that fired first would produce a less specific error.
var client = &http.Client{Timeout: 30 * time.Second}

// request is one call to a store.
type request struct {
	method  string
	url     string
	headers map[string]string
	// body is sent as-is when set. Nil means no body.
	body []byte
	// query is appended when set.
	query map[string]string
	// timeout overrides httpTimeout for one call.
	//
	// It exists for one case and it is worth its weight: the EC2 instance
	// metadata address is a link-local address that nothing answers on a
	// laptop, and the connection does not fail fast, it hangs. At the shared
	// timeout that is ten seconds added to every af up on every machine that is
	// not an EC2 instance, to discover something that a machine either is or is
	// not within a few milliseconds.
	timeout time.Duration
}

// response is what came back, with the body already bounded.
type response struct {
	status int
	body   []byte
}

// do makes the call.
//
// It returns an error only for a failure to complete the exchange. A store that
// answers with 403 is a successful exchange carrying a refusal, and the caller
// decides what a status means, because "404" means "no such secret" to one
// store and "no such vault" to another.
func do(ctx context.Context, req request) (*response, error) {
	timeout := req.timeout
	if timeout <= 0 {
		timeout = httpTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var body io.Reader
	if req.body != nil {
		body = bytes.NewReader(req.body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, req.method, req.url, body)
	if err != nil {
		return nil, err
	}
	if len(req.query) > 0 {
		q := httpReq.URL.Query()
		for k, v := range req.query {
			q.Set(k, v)
		}
		httpReq.URL.RawQuery = q.Encode()
	}
	for k, v := range req.headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		// The URL is in this error and the URL can carry a token in a query
		// string for some stores, so it is stripped rather than passed through.
		return nil, fmt.Errorf("%s", scrubURL(err.Error()))
	}
	defer func() {
		// Drained before closing so the connection can be reused rather than
		// torn down, which matters when twenty variables are resolved in a row.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
		_ = resp.Body.Close()
	}()

	read, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("reading the response failed after %d bytes", len(read))
	}
	return &response{status: resp.StatusCode, body: read}, nil
}

// decode reads a JSON response into v.
//
// The error deliberately does not quote the document. A store returns the
// secret in the same shape it returns an error, so a decode failure that
// printed what it was given would print the secret exactly when something is
// already going wrong.
func (r *response) decode(v any) error {
	if err := json.Unmarshal(r.body, v); err != nil {
		return fmt.Errorf("the store answered %d with %d bytes that are not the JSON expected",
			r.status, len(r.body))
	}
	return nil
}

// rejected reports whether a status means the credential was refused rather
// than the request.
//
// 401 and 403 only. A 400 is a request this code built wrongly and retrying
// with a fresh token changes nothing, and a 429 is a rate limit that a refresh
// would make worse.
func (r *response) rejected() bool {
	return r.status == http.StatusUnauthorized || r.status == http.StatusForbidden
}

// scrubURL removes a query string from a message.
//
// Some stores take a token as a query parameter, and a transport error quotes
// the URL it failed on. Rather than deciding per store which parameters are
// safe, everything after the question mark goes.
func scrubURL(s string) string {
	var out strings.Builder
	for {
		i := strings.IndexByte(s, '?')
		if i < 0 {
			out.WriteString(s)
			return out.String()
		}
		out.WriteString(s[:i])
		out.WriteString("?[redacted]")
		// Resume after the parameters, which end at the next space or quote in
		// an error message.
		rest := s[i+1:]
		j := strings.IndexAny(rest, ` "`)
		if j < 0 {
			return out.String()
		}
		s = rest[j:]
	}
}
