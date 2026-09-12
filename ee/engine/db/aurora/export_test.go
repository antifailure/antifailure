package aurora

import (
	"context"
	"database/sql"
	"net/http"
)

// Shared Postgres fixtures cannot model a separate role catalog per clone.
// This seam exists only in the test binary, never in a shipped provider.
func SetLoginCatalogForTest(p *Provider, lookup func(context.Context, *sql.DB) ([]string, error)) {
	p.loginCatalog = lookup
}
func CustomerLoginsForTest(ctx context.Context, db *sql.DB) ([]string, error) {
	return customerLogins(ctx, db)
}
func SetTrustForTest(p *Provider, pem string) { p.trustOverride = pem }

func SetHTTPForTest(p *Provider, transport http.RoundTripper) {
	p.api.http = &http.Client{Transport: transport}
}
