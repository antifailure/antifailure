package detect

import (
	"context"
	"fmt"
	"sort"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Choosing where personas come from, from what the repository depends on.
//
// The same idea as the third party analyzer next door, and for the same
// reason: a user should not have to know that Supabase keeps its users in an
// auth schema, or that Clerk will not accept a row at all. The dependency list
// already says which it is.
//
// This writes a starting point rather than a final answer. The manifest it
// produces is read at run time by engine/internal/personas, which checks the
// live schema and prefers what it finds there, because a package is an
// intention and a table is a fact. Both exist because they answer at different
// times: this runs at `af init`, before there is a database to look at.

// KindAuth is a finding about where the application keeps its users.
const KindAuth Kind = "auth"

// AuthAnalyzer maps authentication dependencies to a persona adapter.
type AuthAnalyzer struct{}

// Name identifies the analyzer.
func (*AuthAnalyzer) Name() string { return "auth" }

// authScheme is one authentication system this can recognise.
type authScheme struct {
	// Adapter is what the manifest gets.
	Adapter schema.AuthAdapter
	// Packages identify it.
	Packages []string
	// Hosted marks an adapter that reaches an API rather than the database,
	// which is what decides whether a sandbox and a token are needed.
	Hosted bool
	// TokenEnv is the variable its admin credential conventionally lives in.
	TokenEnv string
	// Why explains the choice, and is written into the manifest so that a
	// setting nobody can explain never appears.
	Why string
}

// authSchemes is the catalog, ordered most specific first. A repository using
// Clerk may also depend on a Postgres client, and Clerk is the one that
// decides whether a sign in works.
var authSchemes = []authScheme{
	{
		Adapter: schema.AuthClerk, Hosted: true, TokenEnv: "CLERK_SECRET_KEY",
		Packages: []string{"@clerk/nextjs", "@clerk/clerk-sdk-node", "@clerk/backend",
			"@clerk/clerk-js", "@clerk/express", "@clerk/fastify"},
		Why: "Clerk owns the user table, so personas are created through its API " +
			"against a development instance.",
	},
	{
		Adapter: schema.AuthAuth0, Hosted: true, TokenEnv: "AUTH0_MANAGEMENT_TOKEN",
		Packages: []string{"auth0", "@auth0/nextjs-auth0", "express-openid-connect",
			"@auth0/auth0-react", "@auth0/auth0-spa-js"},
		Why: "Auth0 owns the user table, so personas are created through the " +
			"Management API against a development tenant.",
	},
	{
		Adapter: schema.AuthWorkOS, Hosted: true, TokenEnv: "WORKOS_API_KEY",
		Packages: []string{"@workos-inc/node", "@workos-inc/authkit-nextjs", "workos"},
		Why: "WorkOS owns the user table, so personas are created through User " +
			"Management against the staging environment.",
	},
	{
		Adapter: schema.AuthSupabase,
		Packages: []string{"@supabase/supabase-js", "@supabase/ssr",
			"@supabase/auth-helpers-nextjs", "@supabase/auth-js", "@supabase/gotrue-js"},
		Why: "Supabase keeps users in its auth schema, so personas are created " +
			"there rather than in a table of the application's own.",
	},
	{
		Adapter: schema.AuthNextAuth,
		Packages: []string{"next-auth", "@auth/core", "@auth/pg-adapter",
			"@auth/prisma-adapter", "@auth/drizzle-adapter", "@next-auth/prisma-adapter"},
		Why: "NextAuth keeps users in its own tables, so personas are created " +
			"there. It has no password column, so personas sign in by magic link.",
	},
}

// Analyze reports which authentication system the repository uses.
func (a *AuthAnalyzer) Analyze(_ context.Context, r *Repo) ([]Finding, error) {
	deps := collectDependencies(r)

	// Sorted so that a repository depending on two of these gets the same
	// answer every run rather than whichever map iteration reached first.
	names := make([]string, 0, len(deps))
	for name := range deps {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, scheme := range authSchemes {
		for _, pkg := range scheme.Packages {
			file, ok := deps[pkg]
			if !ok {
				continue
			}
			extra := map[string]string{"why": scheme.Why}
			if scheme.Hosted {
				extra["hosted"] = "true"
				extra["token_env"] = scheme.TokenEnv
			}
			return []Finding{{
				Kind: KindAuth, Subject: string(scheme.Adapter),
				Value: string(scheme.Adapter), Confidence: High,
				Evidence: file,
				Detail:   fmt.Sprintf("%s depends on %s.", file, pkg),
				Extra:    extra,
				Analyzer: a.Name(),
			}}, nil
		}
	}
	return nil, nil
}
