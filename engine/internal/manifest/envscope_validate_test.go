package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// scope: service, and the two ways its promise can be broken by a coincidence
// of names.
//
// The promise is that a value scoped to one service is not readable by another.
// It is kept by the name the value is stored under, which is spelled from the
// service and the variable, and a spelling made of the characters a variable
// name already uses can be arrived at from more than one direction. Both
// directions are refused here rather than left to hand one service another
// service's credential.

const twoServices = `
version: 1
name: supabase
services:
  - name: storage
    port: 3000
    env:
`

func TestParse_AcceptsAVariableScopedToItsService(t *testing.T) {
	t.Parallel()
	m := mustParse(t, twoServices+`      - name: DATABASE_URL
        scope: service
  - name: supavisor
    kind: worker
    env:
      - name: DATABASE_URL
        scope: service
`)
	require.Equal(t, schema.ScopeService, m.Services[0].Env[0].Scope)
	// Two services scoping one name is the case the field exists for, so it
	// must not be read as a collision: the two spell different stored names.
	require.Equal(t, "STORAGE__DATABASE_URL", m.Services[0].Env[0].StoredName("storage"))
	require.Equal(t, "SUPAVISOR__DATABASE_URL", m.Services[1].Env[0].StoredName("supavisor"))
}

func TestParse_RefusesAScopeThatIsNotAScope(t *testing.T) {
	t.Parallel()
	// Nothing validates a manifest against the JSON Schema at parse time, so
	// the enum in schemas/manifest.v1.json does not stand between an author and
	// this check.
	msg := messages(problems(t, mustFail(t, twoServices+`      - name: DATABASE_URL
        scope: environment
`)))
	require.Contains(t, msg, `The variable "DATABASE_URL" has the scope "environment", which is not a scope.`)
	require.Contains(t, msg, "The one scope is service")
}

func TestParse_RefusesAScopeOnALiteral(t *testing.T) {
	t.Parallel()
	// A literal is written under this service already, so it is this service's
	// own. Accepting the pair would mean scope sometimes describes a lookup and
	// sometimes describes nothing.
	msg := messages(problems(t, mustFail(t, twoServices+`      - name: LOG_LEVEL
        value: debug
        scope: service
`)))
	require.Contains(t, msg, `The variable "LOG_LEVEL" is scoped to storage and has a literal value.`)
}

func TestParse_RefusesAScopedNameAnotherServiceReadsWithoutAScope(t *testing.T) {
	t.Parallel()
	// storage's own DATABASE_URL is stored as STORAGE__DATABASE_URL, and a
	// service may read a variable of that exact name. Nothing about the scope
	// would stop it, so the value would be one service's own and readable by
	// another, which is the promise broken in one move.
	msg := messages(problems(t, mustFail(t, twoServices+`      - name: DATABASE_URL
        scope: service
  - name: supavisor
    kind: worker
    env:
      - name: STORAGE__DATABASE_URL
`)))
	require.Contains(t, msg,
		`The variable "DATABASE_URL" is scoped to storage and is stored as STORAGE__DATABASE_URL, `+
			`and supavisor reads STORAGE__DATABASE_URL without a scope.`)
	require.Contains(t, msg, "must not be readable by another")
}

func TestParse_RefusesTwoScopedNamesThatSpellTheSameStoredName(t *testing.T) {
	t.Parallel()
	// The other direction, and it is reachable because a service name may carry
	// two hyphens: a--b becomes A__B, so a--b's C and a's B__C are both stored
	// as A__B__C. Each service asked for its own value and they would share
	// one.
	msg := messages(problems(t, mustFail(t, `
version: 1
name: shop
services:
  - name: a
    port: 3000
    env:
      - name: B__C
        scope: service
  - name: a--b
    kind: worker
    env:
      - name: C
        scope: service
`[1:])))
	require.Contains(t, msg, "is stored as A__B__C, which is also where")
	require.Contains(t, msg, "must not be readable by another")
}

func TestParse_AcceptsOneServiceReadingItsOwnValueUnderTwoNames(t *testing.T) {
	t.Parallel()
	// The same stored name twice inside ONE service is not a leak. It is one
	// value delivered under two names, which is what a rename is for, and the
	// service that owns it is the only one that reads it.
	m := mustParse(t, twoServices+`      - name: DATABASE_URL
        from: PG_URL
        scope: service
      - name: POSTGRES_URL
        from: PG_URL
        scope: service
`)
	require.Equal(t, "STORAGE__PG_URL", m.Services[0].Env[0].StoredName("storage"))
	require.Equal(t, "STORAGE__PG_URL", m.Services[0].Env[1].StoredName("storage"))
}
