package local

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"

	"github.com/antifailure/antifailure/engine/internal/proxyimage"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

const configurationLabel = "dev.antifailure.configuration"

func configurationFingerprint(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(strconv.Itoa(len(part)) + ":"))
		_, _ = hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// The label describes what was installed before the process started. Comparing
// only the image reuses old database URLs and trust files after a new Up.
func (r *Runtime) serviceFingerprint(spec provider.EnvSpec, service provider.ServiceSpec, proxyIP, override string) string {
	command := service.Command
	if override != "" {
		command = override
	}
	parts := []string{
		service.Image, command, service.Kind, service.Migrate,
		strconv.Itoa(service.Port), strconv.FormatInt(service.CPUMillis, 10), strconv.FormatInt(service.MemoryBytes, 10),
		proxyIP, proxyimage.Tag(), spec.CACertPEM, spec.DatabaseCACertPEM, spec.MigrationDatabaseURL.Reveal(),
	}
	environment := r.envList(spec, service)
	parts = append(parts, strconv.Itoa(len(environment)))
	parts = append(parts, environment...)
	for _, route := range spec.DatabaseRoutes {
		parts = append(parts, strconv.Itoa(route.Port), route.Upstream)
	}
	return configurationFingerprint(parts...)
}
