// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds

// The certificate authorities a connection to a branch is verified against.
//
// sslmode require encrypts and checks nothing: any server that answers the TLS
// handshake is accepted, so a connection to a preview environment's database
// could be intercepted by whoever sits between the environment and RDS, and the
// data on that connection is a copy of production. So the default is
// verify-full, which checks the certificate chain and the hostname, and the
// chain is checked against AWS's published RDS roots rather than the machine's
// system store, which does not carry them.

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// AWS's official RDS trust stores, downloaded September 10, 2026. AWS says to
// trust the root authorities rather than pin the rotating server or
// intermediate certificate.
// https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/UsingWithRDS.SSL.html
// https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem
//
// Byte for byte the files the aurora provider carries, because RDS and Aurora
// share one certificate authority hierarchy. Both packages pin the same
// digest in their tests, so a copy that drifted from the other fails there.
//
//go:embed data/rds-commercial.pem
var commercialRoots string

// https://truststore.pki.us-gov-west-1.rds.amazonaws.com/global/global-bundle.pem
//
//go:embed data/rds-gov.pem
var governmentRoots string

var _ provider.DatabaseTrust = (*Provider)(nil)

// trustBundle is the root set for the source instance's partition.
func (p *Provider) trustBundle() (string, error) {
	if p.closed.Load() {
		return "", fmt.Errorf("rds: provider is closed")
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
		return "", fmt.Errorf("rds: unsupported database trust partition")
	}
}

// TrustBundle is the public certificate bundle an application needs to verify
// a branch, which the engine installs inside service and migration containers.
//
// It answers only for a branch this provider would hand a connection string
// for, so it cannot be used to learn anything about a resource it does not
// own.
func (p *Provider) TrustBundle(ctx context.Context, b provider.Branch) (string, error) {
	if _, err := p.ConnString(ctx, b, provider.ConnDirect); err != nil {
		return "", err
	}
	if p.tlsMode == "disable" {
		return "", nil
	}
	return p.trustBundle()
}

// trustFile writes the bundle once, readable by this user only, for the
// connection strings this process hands to its own Postgres driver.
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
	dir, err := os.MkdirTemp("", "af-rds-trust-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "rds-ca.pem")
	if err := os.WriteFile(path, []byte(bundle), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	p.trustDir, p.trustPath = dir, path
	return path, nil
}

// closeTrust marks the provider closed and removes the bundle file it wrote.
func (p *Provider) closeTrust() error {
	p.trustMu.Lock()
	defer p.trustMu.Unlock()
	p.closed.Store(true)
	if p.trustDir == "" {
		return nil
	}
	return os.RemoveAll(p.trustDir)
}
