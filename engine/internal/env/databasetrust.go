package env

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/url"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

func databaseTrust(ctx context.Context, db provider.Database, branch provider.Branch) (string, error) {
	source, ok := db.(provider.DatabaseTrust)
	if !ok {
		return "", nil
	}
	bundle, err := source.TrustBundle(ctx, branch)
	if err != nil {
		return "", fmt.Errorf("reading database trust certificates: %w", err)
	}
	if bundle == "" {
		return "", nil
	}
	rest := []byte(bundle)
	certificates := 0
	var normalized strings.Builder
	for len(strings.TrimSpace(string(rest))) > 0 {
		block, remaining := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" {
			return "", fmt.Errorf("database trust must contain only public CA certificates")
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !certificate.IsCA {
			return "", fmt.Errorf("database trust contains an invalid CA certificate")
		}
		certificates++
		normalized.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}))
		rest = remaining
	}
	if certificates == 0 {
		return "", fmt.Errorf("database trust bundle contains no certificates")
	}
	return normalized.String(), nil
}

func installDatabaseTrust(spec *provider.EnvSpec, bundle string) error {
	if bundle == "" {
		return nil
	}
	for _, value := range []*secret.Value{&spec.DatabaseURL, &spec.MigrationDatabaseURL} {
		if value.IsZero() {
			continue
		}
		address, err := url.Parse(value.Reveal())
		if err != nil {
			return fmt.Errorf("database connection URL is invalid")
		}
		query := address.Query()
		if mode := query.Get("sslmode"); mode != "verify-ca" && mode != "verify-full" {
			return fmt.Errorf("a database trust bundle requires certificate verification in its connection URL")
		}
		query.Set("sslrootcert", provider.DatabaseTrustBundlePath)
		address.RawQuery = query.Encode()
		*value = secret.New(address.String())
	}
	spec.DatabaseCACertPEM = bundle
	return nil
}
