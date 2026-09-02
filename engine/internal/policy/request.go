package policy

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ParseRequest turns a method and a URL into a request the engine can decide.
//
// It lives here rather than beside a caller because more than one thing now
// asks the policy about a hypothetical request: the command line, and the MCP
// server on behalf of a model. Two parsers would eventually disagree about
// what "api.stripe.com/v1" means, and the one that disagreed would be
// answering a different question than the one it was asked.
//
// The error is a plain one, deliberately. This package is compiled into the
// sidecar image and has no dependencies beyond the standard library and the
// schema, which is what lets the sidecar and af net explain share a decision
// rather than reimplement it. Callers that want a coded error wrap this one.
//
// It is strict about the URL because a typo that parsed as a relative path
// would be explained against an empty host, and the answer would be
// confidently wrong rather than obviously wrong.
func ParseRequest(method, raw string) (Request, error) {
	var req Request
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" || strings.ContainsAny(method, " \t/:") {
		return req, fmt.Errorf("%q is not an HTTP method", method)
	}

	if !strings.Contains(raw, "://") {
		// A bare host is what people type, so accept it rather than refusing
		// on a technicality, and assume the scheme the environment mostly
		// speaks.
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return req, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return req, fmt.Errorf("the scheme %q is not http or https", u.Scheme)
	}
	if u.Hostname() == "" {
		return req, fmt.Errorf("%q names no host", raw)
	}

	req.Host = u.Hostname()
	req.Method = method
	req.TLS = u.Scheme == "https"
	req.Path = u.EscapedPath()
	if req.Path == "" {
		req.Path = "/"
	}
	if p := u.Port(); p != "" {
		n, convErr := strconv.Atoi(p)
		if convErr != nil || n <= 0 || n > 65535 {
			return req, fmt.Errorf("the port %q is not valid", p)
		}
		req.Port = n
	}
	return req, nil
}
