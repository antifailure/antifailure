package personas_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/personas"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The gate that decides whether a missing users table stops the run.
//
// It is one boolean guarding a `return nil, err` in env.provisionPersonas, and
// getting it wrong is invisible in either direction. Too strict and a JSON API
// with no sign in can never run a workflow, which is where this came from:
// examples/go-api was refused for lacking somewhere to create an account that
// its only persona was never going to use. Too loose and a run whose personas
// really do sign in carries on past having created none of them, and every
// workflow fails later at the login form for a reason nothing reported.
func TestWhetherAMissingUsersTableStopsTheRun(t *testing.T) {
	t.Parallel()

	visitor := schema.Persona{Name: "visitor", Login: schema.LoginNone}
	member := schema.Persona{Name: "member", Login: schema.LoginPassword}
	// A persona with no login written down at all. The zero value is not
	// LoginNone, so this one needs an account, and reading the field as
	// "anything but password signs in without one" would get it backwards.
	unset := schema.Persona{Name: "unset"}

	other := errors.New("connect: connection refused")

	for _, tc := range []struct {
		name string
		err  error
		list []schema.Persona
		want bool
	}{
		{"nobody signs in", personas.ErrNoUsersTable, []schema.Persona{visitor}, true},
		{"wrapped, because the adapter adds context", fmt.Errorf("looking for a users table: %w", personas.ErrNoUsersTable), []schema.Persona{visitor}, true},
		{"one of several signs in", personas.ErrNoUsersTable, []schema.Persona{visitor, member}, false},
		{"a login nobody wrote down", personas.ErrNoUsersTable, []schema.Persona{unset}, false},
		{"a different failure entirely", other, []schema.Persona{visitor}, false},
		{"no personas at all", personas.ErrNoUsersTable, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, personas.NoAccountNeeded(tc.err, tc.list))
		})
	}
}
