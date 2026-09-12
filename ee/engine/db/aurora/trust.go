// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
package aurora

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// Official AWS RDS trust stores, downloaded September 10, 2026. AWS requires
// root CAs rather than pinning the rotating server or intermediate certificate.
// https://docs.aws.amazon.com/AmazonRDS/latest/AuroraUserGuide/UsingWithRDS.SSL.html
// https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem
//
//go:embed data/rds-commercial.pem
var commercialRoots string

// https://truststore.pki.us-gov-west-1.rds.amazonaws.com/global/global-bundle.pem
//
//go:embed data/rds-gov.pem
var governmentRoots string

var _ provider.DatabaseTrust = (*Provider)(nil)

func (p *Provider) trustBundle() (string, error) {
	if p.closed.Load() {
		return "", fmt.Errorf("aurora: provider is closed")
	}
	if p.trustOverride != "" {
		return p.trustOverride, nil
	}
	switch p.partition {
	case "aws":
		return commercialRoots, nil
	case "aws-us-gov":
		return governmentRoots, nil
	default:
		return "", fmt.Errorf("aurora: unsupported database trust partition")
	}
}

func (p *Provider) TrustBundle(ctx context.Context, b provider.Branch) (string, error) {
	if _, err := p.ConnString(ctx, b, provider.ConnDirect); err != nil {
		return "", err
	}
	if p.tlsMode == "disable" {
		return "", nil
	}
	return p.trustBundle()
}

func (p *Provider) trustFile() (string, error) {
	p.trustMu.Lock()
	defer p.trustMu.Unlock()
	bundle, err := p.trustBundle()
	if err != nil {
		return "", err
	}
	if p.trustPath != "" {
		return p.trustPath, nil
	}
	dir, err := os.MkdirTemp("", "af-aurora-trust-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "rds-ca.pem")
	if err := os.WriteFile(path, []byte(bundle), 0600); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	p.trustDir, p.trustPath = dir, path
	return path, nil
}

func (p *Provider) closeTrust() error {
	p.trustMu.Lock()
	defer p.trustMu.Unlock()
	p.closed.Store(true)
	if p.trustDir == "" {
		return nil
	}
	return os.RemoveAll(p.trustDir)
}
