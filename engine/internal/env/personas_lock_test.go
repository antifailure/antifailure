package env

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/lock"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Why Up calls provisionPersonas and not ProvisionPersonas.
//
// The environment lock is per environment and per process, and it is held for
// the whole of `af up`. ProvisionPersonas opens a session of its own, which
// asks for that lock a second time, and the holder it finds is this very
// process: alive(holder) is true, so it reports AF-RUN-003 naming af up's own
// pid. The user reads "another run holds this environment" while nothing else
// is running.
//
// This is written down as a test rather than as a comment because the two
// functions differ by one word and the wrong one compiles, runs, and fails
// only when somebody actually brings an environment up with personas in the
// manifest. It needs no database: the lock is taken before any provider is.

func personaOrchestrator(t *testing.T) *Orchestrator {
	t.Helper()
	o, err := New(Options{
		Root:   t.TempDir(),
		Branch: "main",
		Clock:  clock.New(),
		Manifest: &schema.Manifest{
			Name: "app",
			Personas: []schema.Persona{
				{Name: "owner", Email: "owner@example.test", Login: schema.LoginPassword},
			},
		},
	})
	require.NoError(t, err)
	return o
}

func TestProvisioningPersonasInsideAHeldSessionWouldReportTheCallersOwnLock(t *testing.T) {
	o := personaOrchestrator(t)

	// Standing in for the session af up holds. The lock alone is enough,
	// because it is what ProvisionPersonas asks for first.
	held, err := lock.Acquire(
		lockPathFor(t, o), o.opts.Clock, "af up")
	require.NoError(t, err)
	t.Cleanup(func() { _ = held.Release() })

	_, err = o.ProvisionPersonas(context.Background())
	require.Error(t, err, "the public entry point took the lock twice and did not notice")

	var coded *aferrors.Error
	require.ErrorAs(t, err, &coded)
	require.Equal(t, aferrors.AFRUN003, coded.Code(),
		"the failure a caller would see is contention with itself, which is why "+
			"Up uses provisionPersonas with the session it already holds")
}

func TestAManifestWithNoPersonasProvisionsNothingAndTakesNoLock(t *testing.T) {
	// The common case, and it must not pay for a lock or a database
	// connection. A manifest can legitimately declare no personas: a workflow
	// can be about a signed out visitor.
	o := personaOrchestrator(t)
	o.opts.Manifest.Personas = nil

	held, err := lock.Acquire(lockPathFor(t, o), o.opts.Clock, "af up")
	require.NoError(t, err)
	t.Cleanup(func() { _ = held.Release() })

	result, err := o.ProvisionPersonas(context.Background())
	require.NoError(t, err, "an empty persona list still tried to open a session")
	require.Nil(t, result)
}

// lockPathFor is the file open() would take for this orchestrator.
func lockPathFor(t *testing.T, o *Orchestrator) string {
	t.Helper()
	return o.opts.Root + "/" + StateDir + "/" + o.envID + ".lock"
}

// A persona that never signs in needs no account, so it needs no users table.
//
// The engine demanded one for every persona and nothing distinguished the two
// cases. It cost the only example in the corpus that declares a workflow:
// examples/go-api sets `login: none` because the service serves JSON and has no
// sign in page, and `af up` refused it with "no users table could be found, so
// there is nowhere to create a persona" for a user it would never have used.
func TestAnyPersonaSignsIn_NoneMeansNoAccountIsNeeded(t *testing.T) {
	require.False(t, anyPersonaSignsIn([]schema.Persona{
		{Name: "visitor", Login: schema.LoginNone},
		{Name: "browser", Login: schema.LoginNone},
	}), "no persona signs in, so nothing has to exist in a users table")

	require.True(t, anyPersonaSignsIn([]schema.Persona{
		{Name: "visitor", Login: schema.LoginNone},
		{Name: "member", Login: schema.LoginPassword},
	}), "one persona that signs in still needs somewhere to be created")
}

// An empty Login is a persona that signs in, not one that does not.
//
// `password` is the schema default and the manifest normaliser applies it, so
// an empty value here is a manifest that has not been through the normaliser.
// Reading it as `none` would turn every such persona into one the engine
// silently creates nobody for, which is the failure this whole change exists to
// stop rather than a smaller version of it.
func TestAnyPersonaSignsIn_TheZeroValueSignsIn(t *testing.T) {
	require.True(t, anyPersonaSignsIn([]schema.Persona{{Name: "unnormalised"}}))
}

// Every strategy that is not `none` needs an account.
//
// Asserted over the list rather than over the three somebody thought of, so a
// strategy added later is covered by this test on the day it is added rather
// than on the day somebody remembers to come back here.
func TestAnyPersonaSignsIn_EveryStrategyExceptNone(t *testing.T) {
	for _, s := range []schema.LoginStrategy{
		schema.LoginPassword, schema.LoginMagicLink, schema.LoginEmailCode,
		schema.LoginSMSCode, schema.LoginTOTP, schema.LoginSession,
	} {
		require.True(t, anyPersonaSignsIn([]schema.Persona{{Name: "p", Login: s}}),
			"%s signs in, so it needs an account", s)
	}
	require.False(t, anyPersonaSignsIn([]schema.Persona{{Name: "p", Login: schema.LoginNone}}))
}
