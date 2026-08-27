package personas

// The schemes are a data table for the same reason detect/thirdparty.go is:
// adding support for a framework should be a value somebody can review rather
// than code somebody has to follow.
//
// The column names below are the real ones. Where a column is generated,
// defaulted or an enum, that is noted, because those are the three ways an
// insert against a schema you have not read fails.

// SchemeSupabase is Supabase Auth, whose users live in the auth schema.
//
// Three details are load bearing and none of them are guessable:
//
// auth.users.id has no default, so the insert has to supply one. A missing id
// is a not-null violation rather than a generated key.
//
// auth.identities.email is a generated column, computed from identity_data,
// so writing it is an error. The identity row is written through provider_id
// and identity_data instead, and provider_id for the email provider is the
// user's own id as text.
//
// email_confirmed_at is what makes an account usable. Supabase with email
// confirmation on refuses a sign in for a user whose address is unconfirmed,
// and the spec calls this out: persona rows are created confirmed. A persona
// waiting for a confirmation mail nobody will send is an environment that
// looks broken.
var SchemeSupabase = Scheme{
	Name: "supabase",
	Users: Table{
		Schema: "auth", Name: "users",
		ID: "id", IDIsUUID: true,
		Email:    "email",
		Password: "encrypted_password",
		Phone:    "phone",
		Role:     "role",
		Fixed: map[string]string{
			// Confirmed on creation, so the persona can sign in at once.
			"email_confirmed_at": "now()",
			// The defaults Supabase's own signup writes. Stated rather than
			// left out, because a null aud or role makes a token the API
			// gateway will not accept.
			"aud":               "'authenticated'",
			"instance_id":       "'00000000-0000-0000-0000-000000000000'::uuid",
			"raw_app_meta_data": `'{"provider":"email","providers":["email"]}'::jsonb`,
		},
		JSON:       "raw_user_meta_data",
		Timestamps: []string{"created_at", "updated_at"},
	},
	Identities: &Table{
		Schema: "auth", Name: "identities",
		// ID here is the column pointing at the account, and Email is the
		// provider's own identifier for it, which for the email provider is
		// the user id as text. The generated email column is not written.
		ID: "user_id", Email: "provider_id",
		JSON: "identity_data",
		Fixed: map[string]string{
			"id":       "gen_random_uuid()",
			"provider": "'email'",
		},
		Timestamps: []string{"created_at", "updated_at", "last_sign_in_at"},
	},
	Factors: &Table{
		Schema: "auth", Name: "mfa_factors",
		ID: "user_id", Password: "secret",
		Fixed: map[string]string{
			"id": "gen_random_uuid()",
			// Both are enums in Supabase, so the literal has to be one of the
			// values the type declares. Verified rather than unverified: an
			// unverified factor is one the user is still enrolling, and the
			// agent has nowhere to complete that.
			"factor_type":   "'totp'",
			"status":        "'verified'",
			"friendly_name": "'Antifailure'",
		},
		Timestamps: []string{"created_at", "updated_at"},
	},
	Sessions: []string{
		"auth.sessions", "auth.refresh_tokens", "auth.mfa_amr_claims",
		"auth.mfa_challenges", "auth.one_time_tokens", "auth.flow_state",
		"auth.saml_relay_states",
	},
	Packages: []string{
		"@supabase/supabase-js", "@supabase/ssr", "@supabase/auth-helpers-nextjs",
		"@supabase/auth-js", "@supabase/gotrue-js", "supabase",
	},
	Probe: "auth.users",
}

// SchemeNextAuth is NextAuth and Auth.js, in the shape their Postgres adapter
// creates.
//
// NextAuth has no password column, which is not an omission: it is built for
// OAuth and email sign in, and a project wanting passwords adds a credentials
// provider and a column of its own. So a persona here signs in by magic link
// unless the project has that column, and the generic scheme is what describes
// it when it does.
//
// The verification_token table is where a magic link's token lives, and it is
// truncated for the same reason sessions are: a token in a published golden is
// a live sign in link belonging to a real user.
var SchemeNextAuth = Scheme{
	Name: "nextauth",
	Users: Table{
		Schema: "public", Name: "users",
		// The adapter's users.id is a SERIAL, so the database fills it and
		// supplying one would fight the sequence.
		ID:    "id",
		Email: "email",
		Fixed: map[string]string{
			// Verified on creation, for the same reason Supabase's personas
			// are confirmed on creation.
			//
			// Written unquoted here and quoted on the way into the statement,
			// which preserves the camel case the adapter declares. Quoting it
			// here as well would produce a triple quoted identifier.
			"emailVerified": "now()",
		},
	},
	Sessions: []string{"public.sessions", "public.verification_token", "public.accounts"},
	Packages: []string{
		"next-auth", "@auth/core", "@auth/pg-adapter", "@auth/prisma-adapter",
		"@auth/drizzle-adapter", "@next-auth/prisma-adapter",
	},
	Probe: "public.verification_token",
}

// GenericScheme returns a scheme for an application that owns its users table.
//
// This is the adapter the spec calls "configured by column names", and it is
// the one most applications need. Everything is named explicitly because
// guessing a column name is how provisioning writes a row the application
// cannot read.
func GenericScheme(t Table, sessions []string) Scheme {
	if t.Schema == "" {
		t.Schema = "public"
	}
	if t.ID == "" {
		t.ID = "id"
	}
	if t.Email == "" {
		t.Email = "email"
	}
	return Scheme{Name: "generic", Users: t, Sessions: sessions}
}

// BuiltinSchemes are the schemes detection can choose from.
//
// Ordered most specific first: an application using Supabase Auth may also
// have a public.users table of its own, and the auth schema is the one that
// decides whether a sign in works.
var BuiltinSchemes = []Scheme{SchemeSupabase, SchemeNextAuth}
