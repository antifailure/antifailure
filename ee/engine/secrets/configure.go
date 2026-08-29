package secrets

// Building the sources an installation has actually configured.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// The rule here is that an explicit request fails loudly and an implicit one
// skips quietly, and it is the only interesting decision in the file.
//
// Somebody who writes AF_SECRET_SOURCES=vault has said what they want. If Vault
// is then misconfigured, starting anyway with Vault silently absent means their
// variables resolve out of .env instead, the environment comes up, and it comes
// up with the wrong values. So a named source that cannot be built stops the
// process with the reason.
//
// Somebody who sets no such variable has said nothing, and their machine may
// happen to carry AWS credentials for something entirely unrelated. Building an
// AWS source out of those and putting it in the chain would be this tool
// deciding on its own behalf to send a variable name to somebody's AWS account.
// So without the variable nothing is auto-detected at all, and af secrets
// sources says so.

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// SourcesEnv names the variable that lists which stores to use, in order.
const SourcesEnv = "AF_SECRET_SOURCES"

// Known are the stores this build can talk to, for the error message that
// lists them when somebody names one that does not exist.
func Known() []string {
	out := []string{"vault", "aws", "azure", "gcp"}
	sort.Strings(out)
	return out
}

// FromEnvironment builds the sources named by AF_SECRET_SOURCES, in order.
//
// The order is the order given, and it matters: two stores that both hold a
// variable answer in the order they were named. It is left to the operator
// rather than fixed here because which store is authoritative is an
// organization's decision and not ours.
func FromEnvironment(getenv func(string) string) ([]*Source, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	names := strings.FieldsFunc(getenv(SourcesEnv), func(r rune) bool {
		return r == ',' || r == ' '
	})
	if len(names) == 0 {
		return nil, nil
	}

	out := make([]*Source, 0, len(names))
	for _, raw := range names {
		name := strings.ToLower(strings.TrimSpace(raw))
		source, err := build(name, getenv)
		if err != nil {
			// Named rather than skipped. See the note at the top of this file:
			// starting without a source somebody asked for means resolving
			// their variables somewhere else and coming up with the wrong ones.
			return nil, fmt.Errorf("%s names %s and it cannot be used: %w", SourcesEnv, name, err)
		}
		out = append(out, source)
	}
	return out, nil
}

func build(name string, getenv func(string) string) (*Source, error) {
	switch name {
	case "vault":
		return NewVault(VaultConfig{
			Address:     getenv("VAULT_ADDR"),
			Token:       getenv("VAULT_TOKEN"),
			RoleID:      getenv("VAULT_ROLE_ID"),
			SecretID:    getenv("VAULT_SECRET_ID"),
			Namespace:   getenv("VAULT_NAMESPACE"),
			Mount:       getenv("AF_VAULT_MOUNT"),
			Path:        getenv("AF_VAULT_PATH"),
			PathPerName: truthy(getenv("AF_VAULT_PATH_PER_NAME")),
			Field:       getenv("AF_VAULT_FIELD"),
			KVv1:        truthy(getenv("AF_VAULT_KV_V1")),
		})
	case "aws":
		return NewAWSSecretsManager(AWSConfig{
			Region:   getenv("AWS_REGION"),
			Prefix:   getenv("AF_AWS_SECRET_PREFIX"),
			SecretID: getenv("AF_AWS_SECRET_ID"),
			Endpoint: getenv("AF_AWS_SECRETSMANAGER_ENDPOINT"),
			Getenv:   getenv,
		})
	case "azure":
		return NewAzureKeyVault(AzureConfig{
			VaultURL:     getenv("AZURE_KEY_VAULT_URL"),
			TenantID:     getenv("AZURE_TENANT_ID"),
			ClientID:     getenv("AZURE_CLIENT_ID"),
			ClientSecret: getenv("AZURE_CLIENT_SECRET"),
			Authority:    getenv("AZURE_AUTHORITY_HOST"),
			Getenv:       getenv,
		})
	case "gcp":
		return NewGCPSecretManager(GCPConfig{
			Project:  getenv("GOOGLE_CLOUD_PROJECT"),
			Prefix:   getenv("AF_GCP_SECRET_PREFIX"),
			Version:  getenv("AF_GCP_SECRET_VERSION"),
			Endpoint: getenv("AF_GCP_SECRETMANAGER_ENDPOINT"),
			Getenv:   getenv,
		})
	default:
		return nil, fmt.Errorf("there is no such store in this build; it knows %s",
			strings.Join(Known(), ", "))
	}
}

// truthy reads the values people actually write in an environment variable.
//
// "false" and "0" are false and everything else that is set is true, because a
// variable set to "no" meaning yes would be worse than either.
func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// Describe reports what each source is and whether it can be used, for a
// startup line and for af explain.
//
// It asks Available, which means it makes whatever call each source's
// reachability check makes, and that is the point: an operator starting the
// enterprise binary should find out that their Vault token expired at startup
// rather than on the first environment somebody tries to create.
func Describe(ctx context.Context, sources []*Source) []string {
	out := make([]string, 0, len(sources))
	for _, s := range sources {
		if ok, why := s.Available(ctx); ok {
			out = append(out, s.Name())
		} else {
			out = append(out, s.Name()+" ("+why+")")
		}
	}
	return out
}

// RegisterFromEnvironment builds the configured sources and plugs them in.
//
// The one call an embedding binary makes. It returns what was registered so the
// binary can print it, because an operator debugging a variable that resolved
// from the wrong place needs to know which stores are in the chain before
// anything else.
func RegisterFromEnvironment(reg *extension.Registry, getenv func(string) string) ([]*Source, error) {
	sources, err := FromEnvironment(getenv)
	if err != nil {
		return nil, err
	}
	Register(reg, sources...)
	return sources, nil
}
