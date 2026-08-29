package detect_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The requirement in one sentence: a repository that depends on
// @supabase/supabase-js should get the Supabase adapter in its manifest
// without anybody being asked. The rest of these are the same claim for the
// other four, plus the two cases where the right answer is to say nothing.

func packageJSONWith(dep string) map[string]string {
	return map[string]string{
		"package.json": `{
			"name": "shop",
			"scripts": {"dev": "next dev", "build": "next build"},
			"dependencies": {"next": "15.0.0", "` + dep + `": "1.0.0"}
		}`,
	}
}

func TestSupabaseIsDetectedFromTheDependencyList(t *testing.T) {
	m := run(t, "shop", packageJSONWith("@supabase/supabase-js")).Draft
	require.NotNil(t, m.Auth, "a Supabase project got no auth block")
	require.Equal(t, schema.AuthSupabase, m.Auth.Adapter)
	// Not hosted, so it needs neither a token nor a sandbox: the users are in
	// the branch's own database.
	require.Empty(t, m.Auth.TokenEnv)
	require.False(t, m.Auth.Sandbox)
}

func TestNextAuthIsDetectedFromTheDependencyList(t *testing.T) {
	m := run(t, "shop", packageJSONWith("next-auth")).Draft
	require.NotNil(t, m.Auth)
	require.Equal(t, schema.AuthNextAuth, m.Auth.Adapter)
}

func TestAHostedProviderIsDetectedAndAsksForItsToken(t *testing.T) {
	for _, c := range []struct {
		dep      string
		adapter  schema.AuthAdapter
		tokenEnv string
	}{
		{"@clerk/nextjs", schema.AuthClerk, "CLERK_SECRET_KEY"},
		{"@auth0/nextjs-auth0", schema.AuthAuth0, "AUTH0_MANAGEMENT_TOKEN"},
		{"@workos-inc/node", schema.AuthWorkOS, "WORKOS_API_KEY"},
	} {
		t.Run(c.dep, func(t *testing.T) {
			m := run(t, "shop", packageJSONWith(c.dep)).Draft
			require.NotNil(t, m.Auth)
			require.Equal(t, c.adapter, m.Auth.Adapter)
			require.Equal(t, c.tokenEnv, m.Auth.TokenEnv,
				"the variable holding the admin token is not named, so the adapter "+
					"has nothing to read")

			// Never set from a dependency list. A hosted adapter creating a
			// persona in the production tenant creates a user in the real
			// product, and the one setting that prevents it is the one a
			// person has to confirm.
			require.False(t, m.Auth.Sandbox,
				"detection declared a sandbox tenant on the user's behalf")
		})
	}
}

func TestClerkWinsOverSupabaseWhenBothArePresent(t *testing.T) {
	// A project can use Supabase for its database and Clerk for its users,
	// and Clerk is the one that decides whether a sign in works. Answering
	// with whichever the map reached first would be right about half the time
	// and different between runs.
	m := run(t, "shop", map[string]string{
		"package.json": `{
			"name": "shop",
			"scripts": {"dev": "next dev"},
			"dependencies": {
				"next": "15.0.0",
				"@supabase/supabase-js": "2.0.0",
				"@clerk/nextjs": "6.0.0"
			}
		}`,
	}).Draft
	require.NotNil(t, m.Auth)
	require.Equal(t, schema.AuthClerk, m.Auth.Adapter)
}

func TestAnApplicationThatOwnsItsUsersGetsNoAuthBlock(t *testing.T) {
	// The right answer is silence. With no block the engine reads the live
	// schema at run time, which is better evidence than a dependency list,
	// and `adapter: auto` in every manifest would be noise that says nothing.
	m := run(t, "shop", map[string]string{
		"package.json": `{
			"name": "shop",
			"scripts": {"dev": "next dev"},
			"dependencies": {"next": "15.0.0", "pg": "8.0.0"}
		}`,
	}).Draft
	require.Nil(t, m.Auth)
}

func TestWorkOSGetsAnEgressRuleLikeTheOtherIdentityProviders(t *testing.T) {
	// It was the only one of the four missing from the third party catalog,
	// so its API had no named rule and was reached under the default.
	m := run(t, "shop", packageJSONWith("@workos-inc/node")).Draft
	rule := ruleFor(t, m, "api.workos.com")

	// Mock rather than sandbox, and that is the existing mechanism working
	// rather than a mistake here: mergeEgress downgrades a sandbox rule with
	// no credential variable to mock, because the manifest validator refuses
	// a sandbox rule that has nothing to check a live key against. Clerk and
	// Auth0 land the same way for the same reason. Asserted as it behaves
	// rather than as the catalog declares, because the alternative is a test
	// that passes only if somebody changes egress policy, which is not this
	// lane's decision to make.
	require.Equal(t, schema.ModeMock, rule.Mode)
	require.NotEmpty(t, rule.Note, "a rule nobody can explain should never appear")
	require.Contains(t, rule.Note, "Sandbox needs a credential variable")
}
