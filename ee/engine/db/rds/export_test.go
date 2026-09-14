// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds

// Seams that exist only in the test binary, never in a shipped provider.

import (
	"context"
	"database/sql"
	"net/http"
)

// SetLoginCatalogForTest replaces the query that lists inherited logins.
//
// The fixture's instances are databases on one shared Postgres, which has one
// role catalog for all of them and for every other suite on the server, so the
// production query would find, and try to disable, logins that belong to
// somebody else. Tests scope it to roles their own fixture created.
func SetLoginCatalogForTest(p *Provider, lookup func(context.Context, *sql.DB) ([]string, error)) {
	p.loginCatalog = lookup
}

// CustomerLoginsForTest is the production catalog query, for the one test
// that checks what it finds without letting the provider act on it.
func CustomerLoginsForTest(ctx context.Context, db *sql.DB) ([]string, error) {
	return customerLogins(ctx, db)
}

// SetTrustForTest replaces the RDS roots with a test certificate authority.
func SetTrustForTest(p *Provider, pem string) { p.trustOverride = pem }

// SetHTTPForTest replaces the transport the control plane client uses, so a
// test can lose a response or hold a request mid flight.
func SetHTTPForTest(p *Provider, transport http.RoundTripper) {
	p.api.http = &http.Client{Transport: transport}
}
