package env

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/personas"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The refusal a new user is most likely to meet had no code at all.
//
// The path: af init writes a manifest with two personas that sign in with a
// password, af up succeeds, and af test stops with
//
//	Error: no users table could be found, so there is nowhere to create a
//	persona; describe the table with auth.table, or use auth.adapter: seed
//
// No AF number, no Next, no More, from a binary whose refusal one command
// earlier is AF-MAN-001 with all three. Standard 7 is not a formatting rule
// here: a person whose first af test has just failed needs to be told that
// auth.table, auth.adapter: seed and login: none are the three ways out, and a
// bare sentence tells them none of it.
//
// This asserts the wiring rather than the string, because the string is the
// catalog's and errcheck already holds the catalog to its own rules.

// noSchemaConn is a database with nothing in it, which is what a branch of an
// empty golden is and what every candidate users table probe finds.
//
// A fake at the Conn seam rather than a real Postgres, because the subject is
// which error personaScheme RETURNS when nothing is found, and reaching that
// with a real database would mean creating one and then carefully not creating
// a users table in it.
type noSchemaConn struct{}

func (noSchemaConn) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (noSchemaConn) QueryRow(context.Context, string, ...any) pgx.Row { return falseRow{} }
func (noSchemaConn) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("no rows here")
}

// falseRow answers every existence probe with no.
type falseRow struct{}

func (falseRow) Scan(dest ...any) error {
	for _, d := range dest {
		if b, ok := d.(*bool); ok {
			*b = false
		}
	}
	return nil
}

func orchestratorForPersonaTest(t *testing.T) *Orchestrator {
	t.Helper()
	o, err := New(Options{
		Root:     t.TempDir(),
		Branch:   "test",
		Manifest: &schema.Manifest{Version: 1, Name: "fixture"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// The real call site, not an error this test built itself.
//
// The first version of this asserted the properties of an aferrors.Wrap it
// constructed in the test body, which meant mutating the production call site
// back to an uncoded fmt.Errorf left it green. A test that builds its own
// subject is decoration, and this repository has shipped that shape before.
func TestTheMissingUsersTableCarriesItsCode(t *testing.T) {
	o := orchestratorForPersonaTest(t)

	_, err := o.personaScheme(t.Context(), noSchemaConn{}, &schema.Auth{})
	if err == nil {
		t.Fatal("a database with no users table produced a scheme")
	}

	var coded *aferrors.Error
	if !errors.As(err, &coded) {
		t.Fatalf("the refusal a first af test meets carries no catalog code: %v", err)
	}
	if coded.Entry.Code != aferrors.AFDB022 {
		t.Errorf("the refusal carries %s, want %s", coded.Entry.Code, aferrors.AFDB022)
	}
	if coded.Entry.NextStep == "" {
		t.Error("the code has no next step, so the user is told what is wrong and not what to do")
	}
	// A configuration problem rather than a generic failure, so a script can
	// tell it from a workflow that genuinely failed.
	if got := aferrors.ExitCodeOf(err); got != aferrors.ExitConfiguration {
		t.Errorf("the refusal exits %d, want %d", got, aferrors.ExitConfiguration)
	}
}

// The direct adapter reaches a different return in the same function, and it
// was uncoded too.
func TestTheDirectAdapterRefusalAlsoCarriesItsCode(t *testing.T) {
	o := orchestratorForPersonaTest(t)

	_, err := o.personaScheme(t.Context(), noSchemaConn{},
		&schema.Auth{Adapter: schema.AuthDirect})
	if err == nil {
		t.Fatal("the direct adapter produced a scheme with no users table")
	}
	var coded *aferrors.Error
	if !errors.As(err, &coded) || coded.Entry.Code != aferrors.AFDB022 {
		t.Errorf("the direct adapter refusal carries no code: %v", err)
	}
}

// And the sentinel has to survive the wrapping, or the tolerance that lets a
// manifest where nobody signs in carry on would silently stop working and
// examples/go-api would be refused again for a table it never wanted.
func TestWrappingTheRefusalKeepsTheSentinelNoAccountNeededLooksFor(t *testing.T) {
	o := orchestratorForPersonaTest(t)

	_, err := o.personaScheme(t.Context(), noSchemaConn{}, &schema.Auth{})
	if err == nil {
		t.Fatal("a database with no users table produced a scheme")
	}
	if !errors.Is(err, personas.ErrNoUsersTable) {
		t.Fatal("the coded error no longer matches the sentinel, so a persona with login none is refused again")
	}
	if !personas.NoAccountNeeded(err, nil) {
		t.Error("NoAccountNeeded no longer recognises the refusal, so a manifest where nobody " +
			"signs in fails on a users table it does not need")
	}
}
