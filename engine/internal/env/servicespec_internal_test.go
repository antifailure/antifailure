package env

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The boundary a manifest field is silently discarded at.
//
// serviceSpec is the only place a schema.Service becomes something a runtime
// receives, and until this file there was no test on it at all. That is how
// health_timeout came to be validated, defaulted, printed by af explain and
// reported over MCP without ever being assigned into the spec: the field IS
// read, so no sweep of unread manifest fields could see it, and the runtimes
// each have their own default so nothing ever looked wrong.
//
// A field somebody adds to schema.Service and forgets here fails one of these.

func TestServiceSpec_CarriesTheInstanceCountTheManifestAsked(t *testing.T) {
	t.Parallel()
	spec := serviceSpec(schema.Service{Name: "roller", Kind: schema.ServiceWorker, Replicas: 3}, "img")
	require.Equal(t, 3, spec.Replicas)
	require.Equal(t, 3, spec.Instances())
}

func TestServiceSpec_AnUndeclaredInstanceCountIsOne(t *testing.T) {
	t.Parallel()
	// Every manifest written before instance counts existed arrives here with
	// zero, and zero instances is not what any of them meant.
	spec := serviceSpec(schema.Service{Name: "web", Kind: schema.ServiceWeb, Port: 3000}, "img")
	require.Zero(t, spec.Replicas)
	require.Equal(t, 1, spec.Instances())
}

func TestServiceSpec_CarriesTheHealthTimeout(t *testing.T) {
	t.Parallel()
	// The field with no writer. A service given ten minutes to start was
	// killed after three, because the runtime never saw the number and fell
	// back to its own default.
	spec := serviceSpec(schema.Service{Name: "web", Port: 3000, HealthTimeout: "600s"}, "img")
	require.Equal(t, 10*time.Minute, spec.HealthTimeout)
}

func TestServiceSpec_LeavesTheHealthTimeoutToTheRuntimeWhenItCannotBeRead(t *testing.T) {
	t.Parallel()
	// Zero, which every runtime reads as "use your own default". An
	// unparseable duration is refused at validation, so a manifest carrying
	// one never reaches here, and failing the whole run over a bound on a
	// wait would be a worse answer than no bound.
	for _, bad := range []string{"", "soon", "0s"} {
		spec := serviceSpec(schema.Service{Name: "web", Port: 3000, HealthTimeout: bad}, "img")
		require.Zero(t, spec.HealthTimeout, "health_timeout %q", bad)
	}
}

func TestServiceSpec_DefaultsAKindlessServiceToAWorker(t *testing.T) {
	t.Parallel()
	// A service with no kind runs, rather than being handed to a runtime that
	// has to decide what an empty string means. Normalization fills this in
	// for a parsed manifest; this is the fallback for one built in code.
	spec := serviceSpec(schema.Service{Name: "thing"}, "img")
	require.Equal(t, "worker", spec.Kind)
}

func TestServiceSpec_DoesNotCarryTheMigration(t *testing.T) {
	t.Parallel()
	// Set by buildServices and NOT by buildPreviousRelease, which runs the
	// previous commit's services against a database the current commit has
	// already migrated. Running the old migration again there is the one
	// thing it must not do, so the field is the caller's decision rather than
	// this function's.
	spec := serviceSpec(schema.Service{Name: "api", Migrate: "npm run migrate"}, "img")
	require.Empty(t, spec.Migrate)
}
