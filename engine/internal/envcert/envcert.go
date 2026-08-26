// Package envcert makes the certificate an environment trusts.
//
// The sidecar reads inside TLS wherever the policy needs the request, which
// means presenting a certificate for a host it is not. That only works if the
// services trust it, so the authority is generated here, handed to the
// sidecar, and injected into every service.
//
// Three properties are deliberate. It is per environment, so a certificate
// cannot outlive the thing it was for and sit in a trust store afterwards. It
// is short lived, thirty days, which is longer than any environment and
// shorter than anybody's memory. And it can sign leaves and nothing else, so
// possession of it does not let somebody mint another authority.
package envcert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"time"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// Authority is an environment's certificate authority.
type Authority struct {
	// CertPEM is the certificate, which every service is told to trust.
	CertPEM string
	// KeyPEM is the private key. It is a secret and typed as one: it goes to
	// the sidecar and nowhere else, and a log line carrying it would be the
	// worst leak in the product.
	KeyPEM secrets.Value
}

// Lifetime is how long an authority is valid.
//
// Longer than any environment, so a long lived one does not break, and short
// enough that a certificate someone forgot to remove stops working rather than
// lingering for years.
const Lifetime = 30 * 24 * time.Hour

// Generate returns a new authority for one environment.
func Generate(envID string, now time.Time) (*Authority, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFSEC010, "detail", err.Error())
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFSEC010, "detail", err.Error())
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			// Named for the environment, so somebody who finds one in a trust
			// store knows what it was for and that it should not be there.
			CommonName:   "Antifailure " + envID,
			Organization: []string{"Antifailure environment authority"},
		},
		// Backdated an hour, because a container whose clock is behind the
		// host's would otherwise reject a certificate issued a moment ago,
		// which is a common failure and a baffling one.
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(Lifetime),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		// One level. This signs leaves and nothing else.
		MaxPathLen:     0,
		MaxPathLenZero: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFSEC010, "detail", err.Error())
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFSEC010, "detail", err.Error())
	}
	return &Authority{
		CertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		KeyPEM: secrets.New(string(pem.EncodeToMemory(
			&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))),
	}, nil
}

// BundlePath is where the certificate is placed inside a service container.
const BundlePath = "/etc/antifailure/ca.crt"

// TrustEnv returns the variables that point a service's runtime at the
// certificate.
//
// There is no single way to do this, which is the whole problem. Node reads
// NODE_EXTRA_CA_CERTS, Python's requests reads REQUESTS_CA_BUNDLE, Go reads
// SSL_CERT_FILE, curl reads CURL_CA_BUNDLE, and each ignores the others. They
// are all set, because setting the wrong one is invisible until a request
// fails for a reason that looks nothing like a certificate.
func TrustEnv() map[string]string {
	return map[string]string{
		"NODE_EXTRA_CA_CERTS": BundlePath,
		"REQUESTS_CA_BUNDLE":  BundlePath,
		"SSL_CERT_FILE":       BundlePath,
		"CURL_CA_BUNDLE":      BundlePath,
		"GIT_SSL_CAINFO":      BundlePath,
		"AWS_CA_BUNDLE":       BundlePath,
		"DENO_CERT":           BundlePath,
	}
	// Deliberately absent: NODE_OPTIONS=--use-openssl-ca. It looks like it
	// helps and does the opposite, switching Node to OpenSSL's own store and
	// making it ignore NODE_EXTRA_CA_CERTS, so every HTTPS call fails with
	// "self-signed certificate in certificate chain" and nothing in the
	// environment explains why. Bun reads NODE_EXTRA_CA_CERTS too, so it needs
	// nothing of its own.
}
