// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds

import "net/http"

// WrapHTTPForTest wraps the client control plane requests already go out on,
// instead of replacing it the way SetHTTPForTest does.
//
// The live run uses it to time every request, and wrapping rather than
// replacing is the point: each request still leaves through the air gap
// guard's client, so the run measures the path a customer's requests take
// rather than a transport that exists only in a test.
func WrapHTTPForTest(p *Provider, wrap func(http.RoundTripper) http.RoundTripper) {
	base := *p.api.httpClient()
	transport := base.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	base.Transport = wrap(transport)
	p.api.http = &base
}
