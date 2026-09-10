// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
package azurepg

import (
	"context"
	_ "embed"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// Microsoft's PostgreSQL TLS guide lists these public roots. Downloaded from
// Microsoft PKI and DigiCert over verified HTTPS on 2026-09-10. This is a root
// bundle, not a leaf pin, so normal server certificate rotation still works.
// https://learn.microsoft.com/azure/postgresql/security/security-tls-how-to-connect
// Microsoft RSA 2017: c741f70f4b2a8d88bf2e71c14122ef53ef10eba0cfa5e64cfa20f418853073e0
// Microsoft ECC 2017: 358df39d764af9e1b766e9c972df352ee15cfac227af6ad1d70e8e4a6edcba02
// DigiCert G2: cb3ccbb76031e5e0138f8dd39a23f9de47ffc35e43c1144cea27d46a5ab1cb5f
// DigiCert G1: 4348a0e9444c78cb265e058d5e8944b4d84f9662bd26db257f8934a443c70161
//
//go:embed azure-roots.pem
var azureRoots string

func (p *Provider) initializeTrust() error {
	if p.tlsMode() != "verify-full" {
		endpoint, err := url.Parse(p.api.endpoint)
		if err != nil {
			return err
		}
		host := endpoint.Hostname()
		if host != "localhost" && !net.ParseIP(host).IsLoopback() {
			return fmt.Errorf("azurepg: remote databases require verify-full TLS; weaker modes are limited to loopback API fixtures")
		}
		return nil
	}
	dir, err := os.MkdirTemp("", "af-azurepg-ca-")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "roots.pem")
	if err := os.WriteFile(path, []byte(azureRoots), 0600); err != nil {
		_ = os.RemoveAll(dir)
		return err
	}
	p.trustDir, p.trustFile = dir, path
	return nil
}

func (p *Provider) TrustBundle(context.Context, provider.Branch) (string, error) {
	if p.closed.Load() {
		return "", fmt.Errorf("azurepg: provider is closed")
	}
	if p.trustFile == "" {
		return "", nil
	}
	return azureRoots, nil
}

var _ provider.DatabaseTrust = (*Provider)(nil)
