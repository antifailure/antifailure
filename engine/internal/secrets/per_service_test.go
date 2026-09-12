package secrets_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The defect this file exists for, in the shape it was found in.
//
// Supabase's published compose file gives DATABASE_URL one value in its storage
// service and a different one in its supavisor service. Both are credentials,
// so neither can be a literal in a manifest and both have to resolve through
// the secrets chain. The chain was asked for a name and nothing else, the two
// declarations were merged by that name, the first place was kept and the
// second dropped, and both services were handed the same credential with
// nothing anywhere saying one of them was wrong.
//
// The distribution loop below is the orchestrator's applyResolved, which is
// what puts a resolved value into the environment a service receives.
func TestResolve_TwoServicesReadingOneNameFromDifferentPlacesEachGetTheirOwn(t *testing.T) {
	m := &schema.Manifest{Services: []schema.Service{
		{Name: "storage", Env: []schema.EnvVar{{Name: "DATABASE_URL", From: "STORAGE_DATABASE_URL"}}},
		{Name: "supavisor", Env: []schema.EnvVar{{Name: "DATABASE_URL", From: "SUPAVISOR_DATABASE_URL"}}},
	}}
	chain := secrets.NewChain(envSource("shell", map[string]string{
		"STORAGE_DATABASE_URL":   "fixture-storage-role",
		"SUPAVISOR_DATABASE_URL": "fixture-supavisor-role",
	}))
	resolved, err := secrets.Resolve(t.Context(), chain, secrets.Request{
		Services: secrets.DeclaredFor(m), EnvID: "af-1",
	})
	require.NoError(t, err)

	got := map[string]string{}
	for _, svc := range m.Services {
		env := map[string]secrets.Value{}
		for _, e := range svc.Env {
			env[e.Name] = secrets.New(e.Value)
		}
		for name := range env {
			if value, ok := resolved.Lookup(svc.Name, name); ok {
				env[name] = value
			}
		}
		got[svc.Name] = env["DATABASE_URL"].Reveal()
	}
	t.Logf("storage receives %s, supavisor receives %s", got["storage"], got["supavisor"])
	require.Equal(t, "fixture-storage-role", got["storage"])
	require.Equal(t, "fixture-supavisor-role", got["supavisor"])
}
