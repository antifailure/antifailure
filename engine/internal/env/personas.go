package env

import (
	"context"
	"errors"
	"fmt"

	"github.com/antifailure/antifailure/engine/internal/personas"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Making the personas exist, which is what turns a manifest's list of accounts
// into accounts.
//
// This runs against the branch rather than against the golden, and it is
// idempotent, so it does not matter which came first. A persona provisioned
// into a golden is reconciled here and nothing changes; a persona that has
// never existed is created here. That property is what lets the same code be
// called from `af up` and again from `af test` without anybody tracking
// which already happened.
//
// It is called before the agents run, because the alternative is the state
// this repository was in until now: the runner was handed a name, an address
// and a derived password for an account nobody had created, the application
// refused the sign in, and the run reported a failing test against the
// application. A fabricated bug is worse than a missing feature, because
// somebody goes and looks for it.

// ProvisionPersonas creates or reconciles every persona in the manifest.
//
// Opens a session of its own, so it is the entry point for a caller that does
// not already hold one. A caller that does must use provisionPersonas
// instead: the lock is per environment and per process, and asking for it
// twice inside one command reports AF-RUN-003 against the command's own pid,
// which reads as "af up is already running" while af up is running.
//
// Returns nil and no error when the manifest declares no personas, which is a
// valid manifest: a workflow can be about a signed out visitor.
func (o *Orchestrator) ProvisionPersonas(ctx context.Context) (*personas.Result, error) {
	if len(o.opts.Manifest.Personas) == 0 {
		return nil, nil
	}
	s, err := o.open(ctx, "af personas")
	if err != nil {
		return nil, err
	}
	defer s.close()
	return o.provisionPersonas(ctx, s)
}

// provisionPersonas does the work against a session the caller already holds.
func (o *Orchestrator) provisionPersonas(
	ctx context.Context, s *session,
) (*personas.Result, error) {
	list := o.opts.Manifest.Personas
	if len(list) == 0 {
		return nil, nil
	}

	adapter, closeAdapter, err := o.personaAdapter(ctx, s)
	if err != nil {
		// Nowhere to write an account, and nothing that wanted one. Every
		// persona here signs in with `none`, which means the runner goes
		// straight to the workflow's start path and authenticates nothing, so
		// the missing table is a fact about the application rather than a
		// problem with it. Refusing here is what stopped examples/go-api, a
		// JSON API with no sign in, from ever running its one workflow.
		if errors.Is(err, personas.ErrNoUsersTable) && !personas.AnyNeedsAccount(list) {
			o.progress("no persona signs in, so no accounts were created")
			return nil, nil
		}
		return nil, err
	}
	defer closeAdapter()

	result, err := personas.Provision(ctx, adapter, o.personaDeriver(), list)
	if err != nil {
		return nil, err
	}

	// The second half of the objective, and the half that is about safety
	// rather than convenience: no real session survives into a branch.
	// Masking rewrites a customer's name and leaves the session row that
	// still authenticates as them, because a session token is not personal
	// data by any rule the verification scanner applies.
	if sql, ok := adapter.(*personas.SQLAdapter); ok {
		emptied, err := sql.TruncateSessions(ctx)
		if err != nil {
			return nil, err
		}
		for _, table := range emptied {
			o.progress(fmt.Sprintf("emptied %s so no real session reaches the branch", table))
		}
	}
	return result, nil
}

// personaDeriver returns the credential derivation for this environment.
//
// Both the adapter that writes the hash and the document that tells the runner
// the password go through this, so the two cannot disagree. Before this
// existed the derivation lived in one function with one caller and the comment
// claimed a second.
func (o *Orchestrator) personaDeriver() *personas.Deriver {
	policy := personas.PasswordPolicy{}
	if a := o.opts.Manifest.Auth; a != nil && a.Password != nil {
		policy = personas.PasswordPolicy{
			MinLength: a.Password.MinLength,
			Symbols:   a.Password.Symbols,
			Forbid:    a.Password.Forbid,
		}
	}
	return personas.NewDeriver(o.envID, policy)
}

// personaAdapter builds the adapter the manifest asks for, or the one
// detection finds.
//
// The returned function closes whatever the adapter needed, and is never nil.
func (o *Orchestrator) personaAdapter(
	ctx context.Context, s *session,
) (personas.Adapter, func(), error) {
	noop := func() {}
	auth := o.opts.Manifest.Auth
	if auth == nil {
		auth = &schema.Auth{Adapter: schema.AuthAuto}
	}

	switch auth.Adapter {
	case schema.AuthSeed:
		url, err := o.branchURL(ctx, s)
		if err != nil {
			return nil, noop, err
		}
		return personas.NewSeedAdapter(personas.SeedOptions{
			Command: auth.Seed, Dir: o.opts.Root, DatabaseURL: url,
		}), noop, nil

	case schema.AuthSupabaseAPI, schema.AuthClerk, schema.AuthAuth0, schema.AuthWorkOS:
		hosted, err := hostedFor(auth)
		if err != nil {
			return nil, noop, err
		}
		token, err := o.lookupSecret(ctx, auth.TokenEnv)
		if err != nil {
			return nil, noop, err
		}
		return personas.NewAPIAdapter(hosted, personas.APIOptions{
			Token: token, Sandbox: auth.Sandbox,
		}), noop, nil
	}

	// Everything left writes rows, so it needs the branch. Connected through
	// the session the caller holds rather than opening another, for the
	// reason on ProvisionPersonas.
	conn, err := connectSession(ctx, o, s)
	if err != nil {
		return nil, noop, err
	}
	done := func() { _ = conn.Close(context.Background()) }

	scheme, err := o.personaScheme(ctx, conn, auth)
	if err != nil {
		done()
		return nil, noop, err
	}
	return personas.NewSQLAdapter(conn, scheme, o.opts.Manifest.Name), done, nil
}

// personaScheme decides which tables the persona rows go in.
func (o *Orchestrator) personaScheme(
	ctx context.Context, conn personas.Conn, auth *schema.Auth,
) (personas.Scheme, error) {
	probe := personas.ConnProbe{Conn: conn}

	switch auth.Adapter {
	case schema.AuthSupabase:
		return withExtraSessions(personas.SchemeSupabase, auth), nil
	case schema.AuthNextAuth:
		return withExtraSessions(personas.SchemeNextAuth, auth), nil
	case schema.AuthDirect:
		if auth.Table != nil {
			return withExtraSessions(personas.GenericScheme(tableFrom(auth.Table), nil), auth), nil
		}
		// Described by the database rather than by the manifest, which is
		// allowed and is what the validator suggests.
		scheme, found, err := personas.InferGeneric(ctx, probe)
		if err != nil {
			return personas.Scheme{}, err
		}
		if !found {
			return personas.Scheme{}, fmt.Errorf(
				"the direct adapter is selected and %w; describe it with auth.table",
				personas.ErrNoUsersTable)
		}
		return withExtraSessions(scheme, auth), nil
	}

	// auto: the live schema first, because a table is a fact and a dependency
	// list is an intention.
	if scheme, found, err := personas.DetectFromSchema(ctx, probe); err != nil {
		return personas.Scheme{}, err
	} else if found {
		o.progress(fmt.Sprintf("personas will be created in the %s schema", scheme.Name))
		return withExtraSessions(scheme, auth), nil
	}

	scheme, found, err := personas.InferGeneric(ctx, probe)
	if err != nil {
		return personas.Scheme{}, err
	}
	if !found {
		return personas.Scheme{}, fmt.Errorf(
			"%w, so there is nowhere to create a persona; "+
				"describe the table with auth.table, or use auth.adapter: seed",
			personas.ErrNoUsersTable)
	}
	o.progress("personas will be created in " + scheme.Users.Name)
	return withExtraSessions(scheme, auth), nil
}

// withExtraSessions adds the manifest's own session tables to a scheme.
//
// An application usually keeps its own sessions alongside whatever its
// framework keeps, and those rows are live logins for the same reason.
func withExtraSessions(s personas.Scheme, auth *schema.Auth) personas.Scheme {
	if auth == nil || len(auth.Sessions) == 0 {
		return s
	}
	s.Sessions = append(append([]string{}, s.Sessions...), auth.Sessions...)
	return s
}

// tableFrom converts the manifest's description into the adapter's.
func tableFrom(t *schema.AuthTable) personas.Table {
	return personas.Table{
		Schema: t.Schema, Name: t.Name, ID: t.ID, Email: t.Email,
		Password: t.Password, Role: t.Role, JSON: t.JSON,
		Attributes: t.Attributes, Timestamps: t.Timestamps,
	}
}

// hostedFor returns the hosted provider the manifest names.
func hostedFor(auth *schema.Auth) (personas.Hosted, error) {
	switch auth.Adapter {
	case schema.AuthSupabaseAPI:
		return personas.SupabaseHosted{URL: auth.URL}, nil
	case schema.AuthClerk:
		return personas.ClerkHosted{}, nil
	case schema.AuthAuth0:
		return personas.Auth0Hosted{Domain: auth.Domain, Connection: auth.Connection}, nil
	case schema.AuthWorkOS:
		return personas.WorkOSHosted{}, nil
	default:
		return nil, fmt.Errorf("%q is not a hosted authentication provider", auth.Adapter)
	}
}

// lookupSecret reads one named value through the secret chain.
func (o *Orchestrator) lookupSecret(ctx context.Context, name string) (secrets.Value, error) {
	if name == "" {
		return secrets.Value{}, fmt.Errorf(
			"the selected authentication adapter needs an admin token and auth.token_env names none")
	}
	value, _, found, err := o.secretChain().Lookup(ctx, name)
	if err != nil {
		return secrets.Value{}, err
	}
	if !found {
		return secrets.Value{}, fmt.Errorf(
			"%s is not set in any configured secret source, and the authentication "+
				"adapter needs it to create personas", name)
	}
	return value, nil
}

// branchURL returns the connection string for this environment's branch.
//
// Handed to a seed command so that the usual seed script, one that writes
// rows, has somewhere to write them. Registered with the redactor on the way
// out, because it is about to be put into a child process's environment.
func (o *Orchestrator) branchURL(ctx context.Context, s *session) (secrets.Value, error) {
	url, err := s.dbProv.ConnString(ctx, provider.Branch{EnvID: o.envID}, provider.ConnDirect)
	if err != nil {
		return secrets.Value{}, err
	}
	o.opts.Redactor.Register(url.Reveal())
	return url, nil
}
