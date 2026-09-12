package env

import (
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// Cloud branches sit outside the application's contained network. A fixed
// listener routes only to the verified branch, including PostgreSQL's initial
// SSLRequest, which carries no hostname for an SNI proxy to inspect.
func relayDatabaseURLs(application, migration secret.Value, egress *schema.Egress) (secret.Value, secret.Value, []provider.DatabaseRoute, error) {
	used := map[int]bool{}
	if egress != nil {
		for _, rule := range egress.Rules {
			_, port, err := net.SplitHostPort(rule.Host)
			if err == nil {
				if number, err := strconv.Atoi(port); err == nil {
					used[number] = true
				}
			}
		}
	}
	var routes []provider.DatabaseRoute
	ports := map[string]int{}
	next := 45000
	rewrite := func(value secret.Value) (secret.Value, error) {
		if value.IsZero() {
			return value, nil
		}
		address, err := url.Parse(value.Reveal())
		if err != nil || (address.Scheme != "postgres" && address.Scheme != "postgresql") || address.Hostname() == "" {
			return secret.Value{}, fmt.Errorf("a remote database needs a PostgreSQL connection URL")
		}
		host := address.Hostname()
		port := address.Port()
		if port == "" {
			port = "5432"
		}
		query := address.Query()
		target := host
		if pinned := query.Get("hostaddr"); pinned != "" {
			target = pinned
			query.Del("hostaddr")
		}
		upstream := net.JoinHostPort(target, port)
		listen, exists := ports[upstream]
		if !exists {
			for next <= 65535 && used[next] {
				next++
			}
			if next > 65535 {
				return secret.Value{}, fmt.Errorf("no unclaimed port remains for the database relay")
			}
			listen = next
			used[listen] = true
			ports[upstream] = listen
			routes = append(routes, provider.DatabaseRoute{Port: listen, Upstream: upstream})
		}
		if net.ParseIP(host) != nil {
			if query.Get("sslmode") == "verify-full" {
				return secret.Value{}, fmt.Errorf("a database using hostname verification needs a DNS hostname rather than an IP address")
			}
			host = fmt.Sprintf("database-%d.af-database.invalid", listen)
		}
		address.Host = net.JoinHostPort(host, strconv.Itoa(listen))
		address.RawQuery = query.Encode()
		return secret.New(address.String()), nil
	}
	app, err := rewrite(application)
	if err != nil {
		return secret.Value{}, secret.Value{}, nil, err
	}
	migrate, err := rewrite(migration)
	if err != nil {
		return secret.Value{}, secret.Value{}, nil, err
	}
	if err := provider.ValidateDatabaseRoutes(routes); err != nil {
		return secret.Value{}, secret.Value{}, nil, err
	}
	return app, migrate, routes, nil
}
