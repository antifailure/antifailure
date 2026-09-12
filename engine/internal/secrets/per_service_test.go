package secrets_test

import (
	"context"
	"errors"
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

// scopeFake is a source whose answers and failures are set per test. Its own,
// rather than the package's env fake, because two of the cells below need a
// source that FAILS for one name and answers for another, which is the
// difference the whole chain rests on.
type scopeFake struct {
	label  string
	values map[string]string
	fails  map[string]error
	asked  []string
}

func (f *scopeFake) Name() string { return f.label }

func (f *scopeFake) Available(context.Context) (bool, string) { return true, "" }

func (f *scopeFake) Lookup(_ context.Context, name string) (secrets.Value, bool, error) {
	f.asked = append(f.asked, name)
	if err, ok := f.fails[name]; ok {
		return secrets.Value{}, false, err
	}
	v, ok := f.values[name]
	if !ok {
		return secrets.Value{}, false, nil
	}
	return secrets.NewFrom(v, f.label), true, nil
}

func scoped(name string) schema.EnvVar {
	return schema.EnvVar{Name: name, Scope: schema.ScopeService}
}

// asService is one service's declarations.
func asService(name string, vars ...schema.EnvVar) secrets.ServiceVars {
	return secrets.ServiceVars{Service: name, Vars: vars}
}

func TestScopedName_IsSpelledFromTheServiceAndTheVariable(t *testing.T) {
	t.Parallel()
	require.Equal(t, "STORAGE__DATABASE_URL", schema.ScopedName("storage", "DATABASE_URL"))
	// A service name may carry hyphens and a variable name may not, so the
	// hyphens become underscores or no source could hold the name.
	require.Equal(t, "OBJECT_STORE__DATABASE_URL", schema.ScopedName("object-store", "DATABASE_URL"))
}

func TestStoredName_SaysWhereEachDeclarationIsLookedUp(t *testing.T) {
	t.Parallel()
	require.Equal(t, "DATABASE_URL", schema.EnvVar{Name: "DATABASE_URL"}.StoredName("storage"))
	require.Equal(t, "PROD_URL", schema.EnvVar{Name: "DATABASE_URL", From: "PROD_URL"}.StoredName("storage"))
	require.Equal(t, "STORAGE__DATABASE_URL", scoped("DATABASE_URL").StoredName("storage"))
	// A scope applies to the stored name, so a scoped rename is still the
	// service's own.
	require.Equal(t, "STORAGE__PROD_URL",
		schema.EnvVar{Name: "DATABASE_URL", From: "PROD_URL", Scope: schema.ScopeService}.StoredName("storage"))
	// A literal is not looked up at all.
	require.Empty(t, schema.EnvVar{Name: "NODE_ENV", Value: "preview"}.StoredName("storage"))
}

// ---------------------------------------------------------------------------
// The orderings. One cell per row, each named for the case it holds.
// ---------------------------------------------------------------------------

func TestScope_AScopedNameAndTheBareNameBothPresent(t *testing.T) {
	t.Parallel()
	// A scoped lookup asks for the scoped name and does not fall back to the
	// bare one. Falling back is what re-creates the defect: a single bare value
	// would be handed to every service that scoped the name, which is the one
	// thing the scope exists to prevent.
	src := &scopeFake{label: "store", values: map[string]string{
		"STORAGE__DATABASE_URL": "fixture-storage-role",
		"DATABASE_URL":          "fixture-shared-role",
	}}
	got, err := secrets.Resolve(t.Context(), secrets.NewChain(src), secrets.Request{
		Services: []secrets.ServiceVars{
			asService("storage", scoped("DATABASE_URL")),
			asService("rest", required("DATABASE_URL")),
		},
		EnvID: "af-1",
	})
	require.NoError(t, err)

	storage, ok := got.Lookup("storage", "DATABASE_URL")
	require.True(t, ok)
	require.Equal(t, "fixture-storage-role", storage.Reveal())

	rest, ok := got.Lookup("rest", "DATABASE_URL")
	require.True(t, ok)
	require.Equal(t, "fixture-shared-role", rest.Reveal(),
		"a service that did not scope the name still reads the bare one")
}

func TestScope_OneNameScopedToTwoServices(t *testing.T) {
	t.Parallel()
	src := &scopeFake{label: "store", values: map[string]string{
		"STORAGE__DATABASE_URL":   "fixture-storage-role",
		"SUPAVISOR__DATABASE_URL": "fixture-supavisor-role",
	}}
	got, err := secrets.Resolve(t.Context(), secrets.NewChain(src), secrets.Request{
		Services: []secrets.ServiceVars{
			asService("storage", scoped("DATABASE_URL")),
			asService("supavisor", scoped("DATABASE_URL")),
		},
		EnvID: "af-1",
	})
	require.NoError(t, err)

	storage, _ := got.Lookup("storage", "DATABASE_URL")
	supavisor, _ := got.Lookup("supavisor", "DATABASE_URL")
	require.Equal(t, "fixture-storage-role", storage.Reveal())
	require.Equal(t, "fixture-supavisor-role", supavisor.Reveal())
	require.NotEqual(t, storage.Reveal(), supavisor.Reveal())
}

func TestScope_AValueScopedToOneServiceIsNotReadableByAnother(t *testing.T) {
	t.Parallel()
	// The promise the scope makes. Storage's credential must not be in the
	// environment-wide map, where any service declaring the name would find it,
	// and must not be in another service's own map either.
	src := &scopeFake{label: "store", values: map[string]string{
		"STORAGE__DATABASE_URL": "fixture-storage-role",
	}}
	got, err := secrets.Resolve(t.Context(), secrets.NewChain(src), secrets.Request{
		Services: []secrets.ServiceVars{asService("storage", scoped("DATABASE_URL"))},
		EnvID:    "af-1",
	})
	require.NoError(t, err)

	require.NotContains(t, got.Service, "DATABASE_URL",
		"a scoped value in the environment-wide map is readable by every service")
	require.NotContains(t, got.Scoped, "supavisor")
	_, ok := got.Lookup("supavisor", "DATABASE_URL")
	require.False(t, ok, "another service resolved storage's own value")
	// And the redactor still receives it, because a value that reached one
	// service is as secret as one that reached all of them.
	revealed := make([]string, 0, 1)
	for _, v := range got.Values() {
		revealed = append(revealed, v.Reveal())
	}
	require.Contains(t, revealed, "fixture-storage-role")
}

func TestScope_AScopedNameForAServiceNothingResolvedFor(t *testing.T) {
	t.Parallel()
	// Asking for a service that has no value of its own falls back to the
	// environment's, and asking for one that has neither is a miss rather than
	// a panic on a nil inner map.
	src := &scopeFake{label: "store", values: map[string]string{"SHARED": "fixture-shared"}}
	got, err := secrets.Resolve(t.Context(), secrets.NewChain(src), secrets.Request{
		Services: []secrets.ServiceVars{asService("web", required("SHARED"))},
		EnvID:    "af-1",
	})
	require.NoError(t, err)

	value, ok := got.Lookup("no-such-service", "SHARED")
	require.True(t, ok, "an environment-wide value answers for any service")
	require.Equal(t, "fixture-shared", value.Reveal())
	_, ok = got.Lookup("no-such-service", "NOT_DECLARED")
	require.False(t, ok)
}

func TestScope_AScopedNameAbsentFromItsSourceNamesTheScopedName(t *testing.T) {
	t.Parallel()
	// The message has to say where to put the value, and for a scoped variable
	// that is the scoped spelling. Reporting DATABASE_URL would send somebody
	// to set a name the resolver will never ask for.
	src := &scopeFake{label: "store", values: map[string]string{"DATABASE_URL": "fixture-shared"}}
	got, err := secrets.Resolve(t.Context(), secrets.NewChain(src), secrets.Request{
		Services: []secrets.ServiceVars{asService("storage", scoped("DATABASE_URL"))},
		EnvID:    "af-1",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"STORAGE__DATABASE_URL"}, missingNames(got.Missing))
}

func TestScope_ASourceThatRefusesAScopedNameDoesNotFallThrough(t *testing.T) {
	t.Parallel()
	// The difference the whole chain rests on. A MISS falls through to the next
	// source; a FAILURE does not, because falling through would hand the
	// application a different value than it got yesterday. A scope must not
	// change that, so a refusal on the scoped name is returned even though a
	// later source holds it.
	refusing := &scopeFake{
		label: "the company secret manager",
		fails: map[string]error{"STORAGE__DATABASE_URL": errors.New("the token expired")},
	}
	later := &scopeFake{label: "store", values: map[string]string{
		"STORAGE__DATABASE_URL": "fixture-storage-role",
	}}
	_, err := secrets.Resolve(t.Context(), secrets.NewChain(refusing, later), secrets.Request{
		Services: []secrets.ServiceVars{asService("storage", scoped("DATABASE_URL"))},
		EnvID:    "af-1",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "the company secret manager")
	require.Contains(t, err.Error(), "the token expired")
	require.NotContains(t, err.Error(), "fixture-storage-role",
		"the value the later source held must not appear in the message")
	require.Empty(t, later.asked, "the chain must not have reached past the refusal")
}

func TestScope_AMissOnAScopedNameStillFallsThroughToTheNextSource(t *testing.T) {
	t.Parallel()
	// The other half of the same rule, because a scope that turned a miss into
	// a stop would break every chain with more than one source in it.
	empty := &scopeFake{label: "this shell's environment"}
	later := &scopeFake{label: "store", values: map[string]string{
		"STORAGE__DATABASE_URL": "fixture-storage-role",
	}}
	got, err := secrets.Resolve(t.Context(), secrets.NewChain(empty, later), secrets.Request{
		Services: []secrets.ServiceVars{asService("storage", scoped("DATABASE_URL"))},
		EnvID:    "af-1",
	})
	require.NoError(t, err)
	value, ok := got.Lookup("storage", "DATABASE_URL")
	require.True(t, ok)
	require.Equal(t, "fixture-storage-role", value.Reveal())
	require.Equal(t, []string{"STORAGE__DATABASE_URL"}, empty.asked)
}

func TestScope_TwoServicesResolveInOneRehearsalWhicheverOrderTheyAreDeclaredIn(t *testing.T) {
	t.Parallel()
	// One rehearsal resolves every service, so the two are resolved at once and
	// the answer must not depend on which was written first. Declaration order
	// is exactly what the old merge was sensitive to: it kept the first place
	// it saw.
	src := func() *scopeFake {
		return &scopeFake{label: "store", values: map[string]string{
			"STORAGE_DATABASE_URL":   "fixture-storage-role",
			"SUPAVISOR_DATABASE_URL": "fixture-supavisor-role",
		}}
	}
	storage := asService("storage", schema.EnvVar{Name: "DATABASE_URL", From: "STORAGE_DATABASE_URL"})
	supavisor := asService("supavisor", schema.EnvVar{Name: "DATABASE_URL", From: "SUPAVISOR_DATABASE_URL"})

	forward, err := secrets.Resolve(t.Context(), secrets.NewChain(src()), secrets.Request{
		Services: []secrets.ServiceVars{storage, supavisor}, EnvID: "af-1",
	})
	require.NoError(t, err)
	backward, err := secrets.Resolve(t.Context(), secrets.NewChain(src()), secrets.Request{
		Services: []secrets.ServiceVars{supavisor, storage}, EnvID: "af-1",
	})
	require.NoError(t, err)

	for _, got := range []*secrets.Resolved{forward, backward} {
		one, _ := got.Lookup("storage", "DATABASE_URL")
		two, _ := got.Lookup("supavisor", "DATABASE_URL")
		require.Equal(t, "fixture-storage-role", one.Reveal())
		require.Equal(t, "fixture-supavisor-role", two.Reveal())
	}
	// And the audit record is the same either way, so two runs can be compared.
	require.Equal(t, resolutionNames(forward.Resolutions), resolutionNames(backward.Resolutions))
}

// ---------------------------------------------------------------------------
// The refusals. Silence is the actual bug, so these matter as much as the
// feature above.
// ---------------------------------------------------------------------------

func TestScope_ASandboxCredentialTwoServicesReadDifferentlyIsRefused(t *testing.T) {
	t.Parallel()
	// The proxy holds one value per credential for the whole environment and
	// substitutes it into every request to that provider whichever service
	// sent it. There is no value it could use for two, and keeping the first
	// would hand a service another service's key.
	src := &scopeFake{label: "store", values: map[string]string{
		"WEB_STRIPE_KEY":    "sk_test_web",
		"WORKER_STRIPE_KEY": "sk_test_worker",
	}}
	_, err := secrets.Resolve(t.Context(), secrets.NewChain(src), secrets.Request{
		Services: []secrets.ServiceVars{
			asService("web", schema.EnvVar{Name: "STRIPE_SECRET_KEY", From: "WEB_STRIPE_KEY"}),
			asService("worker", schema.EnvVar{Name: "STRIPE_SECRET_KEY", From: "WORKER_STRIPE_KEY"}),
		},
		Sandbox: []string{"STRIPE_SECRET_KEY"},
		EnvID:   "af-1",
	})
	require.Error(t, err)
	var conflict *secrets.SandboxConflictError
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, "STRIPE_SECRET_KEY", conflict.Name)
	require.Equal(t, []string{"web", "worker"}, conflict.Services,
		"the message has to name the variable and both services")
	require.Contains(t, err.Error(), "STRIPE_SECRET_KEY")
	require.Contains(t, err.Error(), "web and worker")
	// Never a value, in the one place a value is guaranteed to be printed.
	require.NotContains(t, err.Error(), "sk_test_web")
	require.NotContains(t, err.Error(), "sk_test_worker")
}

func TestScope_ASandboxCredentialScopedToOneServiceIsRefused(t *testing.T) {
	t.Parallel()
	// Refused even though one value could be stored, because the proxy would
	// substitute that one service's credential into every other service's
	// requests to the same provider, which is the leak read the other way
	// round.
	src := &scopeFake{label: "store", values: map[string]string{
		"WEB__STRIPE_SECRET_KEY": "sk_test_web",
	}}
	_, err := secrets.Resolve(t.Context(), secrets.NewChain(src), secrets.Request{
		Services: []secrets.ServiceVars{asService("web", scoped("STRIPE_SECRET_KEY"))},
		Sandbox:  []string{"STRIPE_SECRET_KEY"},
		EnvID:    "af-1",
	})
	require.Error(t, err)
	var conflict *secrets.SandboxConflictError
	require.ErrorAs(t, err, &conflict)
	require.True(t, conflict.Scoped)
	require.Contains(t, err.Error(), "scope: service")
	require.NotContains(t, err.Error(), "sk_test_web")
}

func TestScope_ASandboxCredentialEveryServiceReadsTheSameWayIsStillAllowed(t *testing.T) {
	t.Parallel()
	// The refusal is about two values, not about two services. Two services
	// naming one credential the same way is the ordinary case and must keep
	// working, or the refusal above would be a regression dressed as a check.
	src := &scopeFake{label: "store", values: map[string]string{"STRIPE_SECRET_KEY": "sk_test_shared"}}
	got, err := secrets.Resolve(t.Context(), secrets.NewChain(src), secrets.Request{
		Services: []secrets.ServiceVars{
			asService("web", required("STRIPE_SECRET_KEY")),
			asService("worker", required("STRIPE_SECRET_KEY")),
		},
		Sandbox: []string{"STRIPE_SECRET_KEY"},
		EnvID:   "af-1",
	})
	require.NoError(t, err)
	require.Equal(t, "sk_test_shared", got.Sidecar["STRIPE_SECRET_KEY"].Reveal())
}

func TestScope_TheAuditRecordNamesTheServiceOnlyWhenTheValueIsOneServicesOwn(t *testing.T) {
	t.Parallel()
	// A record of an environment that uses no per service value has to be what
	// it was, or every existing audit trail changes shape for nothing.
	src := &scopeFake{label: "store", values: map[string]string{
		"SHARED":                "fixture-shared",
		"STORAGE__DATABASE_URL": "fixture-storage-role",
	}}
	got, err := secrets.Resolve(t.Context(), secrets.NewChain(src), secrets.Request{
		Services: []secrets.ServiceVars{
			asService("storage", scoped("DATABASE_URL"), required("SHARED")),
		},
		EnvID: "af-1",
	})
	require.NoError(t, err)

	fields := secrets.AuditFields(got.Resolutions)
	byName := map[string]map[string]string{}
	for _, f := range fields {
		byName[f["name"]] = f
	}
	require.NotContains(t, byName["SHARED"], "service")
	require.Equal(t, "storage", byName["DATABASE_URL (from STORAGE__DATABASE_URL)"]["service"])
	// And no record carries a value, which is the rule the whole record exists
	// under.
	for _, f := range fields {
		for _, v := range f {
			require.NotEqual(t, "fixture-storage-role", v)
			require.NotEqual(t, "fixture-shared", v)
		}
	}
}
