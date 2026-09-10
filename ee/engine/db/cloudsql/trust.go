// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
package cloudsql

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

const instanceCA = "GOOGLE_MANAGED_INTERNAL_CA"
const sharedCA = "GOOGLE_MANAGED_CAS_CA"
const customerCA = "CUSTOMER_MANAGED_CAS_CA"

func validateTLSOptions(opts Options) error {
	switch opts.TLSMode {
	case "", "verify-ca", "verify-full":
		return nil
	case "disable":
		host, port, err := net.SplitHostPort(opts.ProxyAddress)
		if err != nil || port == "" {
			return fmt.Errorf("cloudsql: disabling TLS requires an explicitly configured local proxy")
		}
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return fmt.Errorf("cloudsql: disabling TLS is only supported on a loopback proxy connection")
		}
		return nil
	default:
		return fmt.Errorf("cloudsql: TLSMode %q does not verify server identity; use automatic verification, verify-ca, or verify-full", opts.TLSMode)
	}
}

// TrustBundle returns authenticated, instance-scoped CA material for the runtime.
// A private key is never returned. The runtime installs this PEM separately from
// the engine's private certificate files, whose lifetime is the Provider's.
func (p *Provider) TrustBundle(ctx context.Context, b provider.Branch) (string, error) {
	name := b.ProviderRef
	if name == "" {
		name = p.instanceName(branchPrefix, b.EnvID)
	}
	in, err := p.api.getInstance(ctx, name)
	if err != nil {
		return "", err
	}
	if err := p.requireBranchIdentity(in, b); err != nil {
		return "", err
	}
	_, _, _, bundle, err := p.trustFor(ctx, in)
	return bundle, err
}

// CA mode and DNS metadata come from the authenticated instance endpoint.
// Dedicated instance CAs establish identity without a DNS SAN. Shared CAs do
// not, so shared/custom CA modes must validate the instance's DNS name too.
// https://docs.cloud.google.com/sql/docs/postgres/authorize-ssl
func (p *Provider) trustFor(ctx context.Context, in *instance) (host string, port int, mode, bundle string, err error) {
	if p.closed.Load() {
		err = fmt.Errorf("cloudsql: provider is closed")
		return
	}
	if !p.owns(in) {
		err = fmt.Errorf("cloudsql: instance %q: %w", in.Name, ErrNotOurs)
		return
	}
	host, port, err = p.address(in)
	if err != nil {
		return
	}
	if p.opts.TLSMode == "disable" {
		mode = "disable"
		return
	}
	var authorities []sslCert
	switch in.Settings.IPConfiguration.ServerCAMode {
	case instanceCA:
		mode = "verify-ca"
		if in.ServerCACert.Cert == "" {
			err = fmt.Errorf("cloudsql: instance %q returned no current server CA certificate", in.Name)
			return
		}
		if in.ServerCACert.Instance != "" && in.ServerCACert.Instance != in.Name {
			err = fmt.Errorf("cloudsql: the current CA belongs to another instance")
			return
		}
		var response struct {
			Certs []sslCert `json:"certs"`
		}
		if err = p.api.do(ctx, "GET", p.api.instancePath(in.Name)+"/listServerCas", nil, &response); err != nil {
			return
		}
		authorities = append(response.Certs, in.ServerCACert)
		if p.opts.TLSMode == "verify-full" {
			mode = "verify-full"
			host, err = p.verifiedDNS(in, host)
			if err != nil {
				return
			}
		}
	case sharedCA, customerCA:
		if p.opts.TLSMode == "verify-ca" {
			err = fmt.Errorf("cloudsql: shared or customer CAs require hostname verification")
			return
		}
		mode = "verify-full"
		host, err = p.verifiedDNS(in, host)
		if err != nil {
			return
		}
		var response struct {
			CAs     []sslCert `json:"caCerts"`
			Servers []sslCert `json:"serverCerts"`
			Active  string    `json:"activeVersion"`
		}
		if err = p.api.do(ctx, "GET", p.api.instancePath(in.Name)+"/listServerCertificates", nil, &response); err != nil {
			return
		}
		authorities = response.CAs
		bundle, err = p.caBundle(in.Name, authorities)
		if err != nil {
			return
		}
		roots := x509.NewCertPool()
		roots.AppendCertsFromPEM([]byte(bundle))
		verified := false
		for _, server := range response.Servers {
			if response.Active == "" || server.SHA1Fingerprint != response.Active {
				continue
			}
			block, rest := pem.Decode([]byte(server.Cert))
			if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
				err = fmt.Errorf("cloudsql: active server certificate is malformed")
				return
			}
			certificate, parseErr := x509.ParseCertificate(block.Bytes)
			if parseErr != nil {
				err = fmt.Errorf("cloudsql: active server certificate is invalid: %w", parseErr)
				return
			}
			if _, verifyErr := certificate.Verify(x509.VerifyOptions{Roots: roots, DNSName: host, CurrentTime: p.now()}); verifyErr != nil {
				err = fmt.Errorf("cloudsql: active server identity could not be verified: %w", verifyErr)
				return
			}
			verified = true
		}
		if !verified {
			err = fmt.Errorf("cloudsql: no active server certificate establishes this instance's DNS identity")
			return
		}
		return
	default:
		err = fmt.Errorf("cloudsql: instance %q has unsupported or missing server CA mode %q", in.Name, in.Settings.IPConfiguration.ServerCAMode)
		return
	}
	bundle, err = p.caBundle(in.Name, authorities)
	return
}

func (p *Provider) verifiedDNS(in *instance, address string) (string, error) {
	name := strings.TrimSuffix(in.DNSName, ".")
	if name == "" || len(name) > 253 || net.ParseIP(name) != nil {
		return "", fmt.Errorf("cloudsql: instance %q has no verifiable DNS hostname", in.Name)
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("cloudsql: instance DNS name is malformed")
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' {
				return "", fmt.Errorf("cloudsql: instance DNS name is malformed")
			}
		}
	}
	if p.opts.ProxyAddress != "" && !strings.EqualFold(address, name) {
		return "", fmt.Errorf("cloudsql: proxy address cannot replace the certificate's DNS identity")
	}
	return name, nil
}

func (p *Provider) caBundle(instance string, authorities []sslCert) (string, error) {
	unique := map[string]bool{}
	usable := false
	for _, authority := range authorities {
		if authority.Instance != "" && authority.Instance != instance {
			return "", fmt.Errorf("cloudsql: CA certificate is bound to another instance")
		}
		rest := []byte(authority.Cert)
		if len(bytes.TrimSpace(rest)) == 0 {
			return "", fmt.Errorf("cloudsql: an empty CA certificate was returned")
		}
		for len(bytes.TrimSpace(rest)) > 0 {
			block, remaining := pem.Decode(rest)
			if block == nil || block.Type != "CERTIFICATE" {
				return "", fmt.Errorf("cloudsql: CA bundle contains malformed or non-certificate PEM")
			}
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil || !cert.IsCA || !cert.BasicConstraintsValid || cert.KeyUsage&x509.KeyUsageCertSign == 0 {
				return "", fmt.Errorf("cloudsql: CA bundle contains an invalid signing certificate")
			}
			if !p.now().Before(cert.NotBefore) && p.now().Before(cert.NotAfter) {
				usable = true
			}
			unique[string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))] = true
			rest = remaining
		}
	}
	if !usable {
		return "", fmt.Errorf("cloudsql: no currently valid CA certificate was returned")
	}
	ordered := make([]string, 0, len(unique))
	for cert := range unique {
		ordered = append(ordered, cert)
	}
	sort.Strings(ordered)
	return strings.Join(ordered, ""), nil
}

func (p *Provider) secureConnString(ctx context.Context, in *instance, user, password, database string) (secret.Value, error) {
	host, port, mode, bundle, err := p.trustFor(ctx, in)
	if err != nil {
		return secret.Value{}, err
	}
	u := &url.URL{Scheme: "postgresql", User: url.UserPassword(user, password), Host: net.JoinHostPort(host, strconv.Itoa(port)), Path: "/" + database}
	q := u.Query()
	q.Set("sslmode", mode)
	if bundle != "" {
		path, err := p.writeCertificate(bundle)
		if err != nil {
			return secret.Value{}, err
		}
		q.Set("sslrootcert", path)
	}
	u.RawQuery = q.Encode()
	return secret.New(u.String()), nil
}

func (p *Provider) writeCertificate(bundle string) (string, error) {
	p.certMu.Lock()
	defer p.certMu.Unlock()
	if p.closed.Load() {
		return "", fmt.Errorf("cloudsql: provider is closed")
	}
	if p.certDir == "" {
		dir, err := os.MkdirTemp("", "af-cloudsql-ca-")
		if err != nil {
			return "", err
		}
		p.certDir = dir
	}
	digest := sha256.Sum256([]byte(bundle))
	path := filepath.Join(p.certDir, hex.EncodeToString(digest[:])+".pem")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		return path, nil
	}
	if err != nil {
		return "", err
	}
	if _, err := file.WriteString(bundle); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func (p *Provider) closeCertificates() error {
	p.closed.Store(true)
	p.certMu.Lock()
	defer p.certMu.Unlock()
	if p.certDir == "" {
		return nil
	}
	err := os.RemoveAll(p.certDir)
	if err == nil {
		p.certDir = ""
	}
	return err
}

var _ provider.DatabaseTrust = (*Provider)(nil)
