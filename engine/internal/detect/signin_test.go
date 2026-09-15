package detect_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/detect"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The drafted personas' login strategy, which decides whether the documented
// first run can reach a verdict at all.
//
// The failure: personas were drafted with login: password whatever the
// repository was. On a repository that serves JSON and owns no users table,
// af up refuses with AF-DB-022, "the personas could not be created, so signing
// in will not work", and af test exits 3 with no verdict. The quickstart
// therefore never went green on the commonest shape a stranger tries, and the
// next steps it prints inherit the persona form that caused it. This
// repository's own examples/go-api sets login: none by hand and says in a
// comment that it said password until af ci was run against it.
//
// A password persona needs somewhere to create the account and a form to type
// the password into. So the draft keeps password whenever the repository shows
// either, and only a repository that shows neither gets personas that never
// sign in.

// goAPI is a JSON service with no users table and nothing rendered: the shape
// examples/go-api has, reduced to what detection reads.
var goAPI = map[string]string{
	"go.mod":  "module example.test/orders\n\ngo 1.24\n",
	"main.go": "package main\n\nfunc main() {}\n",
	"migrations/0001_init.sql": `CREATE TABLE IF NOT EXISTS customers (
  id    bigserial PRIMARY KEY,
  email text NOT NULL
);
CREATE TABLE IF NOT EXISTS orders (
  id          bigserial PRIMARY KEY,
  customer_id bigint NOT NULL REFERENCES customers(id),
  total_cents bigint NOT NULL
);
`,
}

// withFiles is a fixture plus extra files, so each case says only what it adds.
func withFiles(base map[string]string, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func TestPersonasSignInWithAPasswordOnlyWhenTheRepositoryCouldHaveOne(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  schema.LoginStrategy
		// why names the evidence the decision must rest on, and is checked
		// against the finding so that a case cannot pass for the wrong reason.
		why string
	}{
		{
			name:  "a JSON API with no users table and nothing rendered",
			files: goAPI,
			want:  schema.LoginNone,
		},
		{
			name: "the same API once it declares a users table",
			files: withFiles(goAPI, map[string]string{
				"migrations/0002_users.sql": "CREATE TABLE users (\n  id bigserial PRIMARY KEY,\n  email text NOT NULL UNIQUE,\n  password_hash text\n);\n",
			}),
			want: schema.LoginPassword,
			why:  "users-table",
		},
		{
			name: "a users table under a schema, quoted, in one statement with others",
			files: withFiles(goAPI, map[string]string{
				"db/schema.sql": "CREATE TABLE \"app\".\"accounts\" (\n  id uuid PRIMARY KEY,\n  email varchar(255) NOT NULL\n);\n",
			}),
			want: schema.LoginPassword,
			why:  "users-table",
		},
		{
			name: "a Prisma schema with a User model",
			files: map[string]string{
				"package.json":         `{"name":"api","dependencies":{"@prisma/client":"5.0.0"},"scripts":{"start":"node server.js"}}`,
				"prisma/schema.prisma": "generator client {\n  provider = \"prisma-client-js\"\n}\n\nmodel User {\n  id    Int    @id @default(autoincrement())\n  email String @unique\n}\n",
			},
			want: schema.LoginPassword,
			why:  "users-table",
		},
		{
			name: "a repository that renders pages",
			files: withFiles(goAPI, map[string]string{
				"web/templates/login.html": "<form method=\"post\"><input name=\"email\"></form>\n",
			}),
			want: schema.LoginPassword,
			why:  "ui",
		},
		{
			name: "a Next.js application, whose pages are components",
			files: map[string]string{
				"package.json":  `{"name":"web","dependencies":{"next":"15.0.0","react":"19.0.0"},"scripts":{"dev":"next dev","start":"next start"}}`,
				"app/page.tsx":  "export default function Page() { return <main>hello</main> }\n",
				"app/login.tsx": "export default function Login() { return <form/> }\n",
			},
			want: schema.LoginPassword,
			why:  "ui",
		},
		{
			name: "a Next.js application whose pages are plain .js files",
			files: map[string]string{
				// Nothing here declares markup or a users table. The framework
				// is the signal, and without it a web application would have
				// been drafted with personas that never sign in.
				"package.json":   `{"name":"web","dependencies":{"next":"15.0.0"},"scripts":{"start":"next start"}}`,
				"pages/index.js": "export default function Home() { return null }\n",
			},
			want: schema.LoginPassword,
		},
		{
			name: "an API whose users live in a provider that owns them",
			files: withFiles(goAPI, map[string]string{
				"package.json": `{"name":"api","dependencies":{"@clerk/backend":"1.0.0"},"scripts":{"start":"node server.js"}}`,
			}),
			want: schema.LoginPassword,
			// The auth analyzer's finding carries this one, not the sign in
			// analyzer's: a provider owns the table, so nothing local declares
			// it and nothing needs to be rendered here.
		},
		{
			name: "a table named like a users table with no address in it",
			files: withFiles(goAPI, map[string]string{
				// Provisioning identifies a persona by address, so the run time
				// inference refuses this table too. Drafting a password persona
				// against it would fail in exactly the way this change exists
				// to stop.
				"migrations/0002_members.sql": "CREATE TABLE members (\n  id bigserial PRIMARY KEY,\n  nickname text NOT NULL\n);\n",
			}),
			want: schema.LoginNone,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := run(t, "repo", tc.files)

			require.NotEmpty(t, res.Draft.Personas, "no personas were drafted at all")
			for _, p := range res.Draft.Personas {
				require.Equal(t, tc.want, p.Login, "persona %q", p.Name)
			}

			surfaces := detect.OfKind(res.Findings, detect.KindSignInSurface)
			if tc.why == "" {
				if tc.want == schema.LoginNone {
					require.Empty(t, surfaces,
						"a sign in surface was reported for a repository that has none")
				}
				return
			}
			require.NotEmpty(t, surfaces, "nothing reported the surface this case is about")
			var subjects []string
			for _, f := range surfaces {
				subjects = append(subjects, f.Subject)
				require.NotEmpty(t, f.Evidence, "a finding with no evidence names nothing")
			}
			require.Contains(t, subjects, tc.why,
				"the decision did not rest on the evidence this case is about")
		})
	}
}

// TestTheDraftedPersonaFormIsOneProvisioningAccepts ties the draft to the run
// time rule rather than to a second opinion about it: the table names detection
// looks for in the repository are the names provisioning looks for in the
// database. Two lists that drift apart would have af init propose a persona
// form af up then refuses, which is the defect this changed.
func TestTheDraftedPersonaFormIsOneProvisioningAccepts(t *testing.T) {
	t.Parallel()
	for _, table := range []string{"users", "user", "accounts", "app_users", "members"} {
		t.Run(table, func(t *testing.T) {
			t.Parallel()
			res := run(t, "repo", withFiles(goAPI, map[string]string{
				"migrations/0002_people.sql": "CREATE TABLE " + table +
					" (\n  id bigserial PRIMARY KEY,\n  email text NOT NULL\n);\n",
			}))
			for _, p := range res.Draft.Personas {
				require.Equal(t, schema.LoginPassword, p.Login,
					"%s is a users table provisioning accepts, so the persona should sign in", table)
			}
		})
	}
}
