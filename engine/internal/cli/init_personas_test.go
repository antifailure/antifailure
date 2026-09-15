package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The documented first run on a JSON API, which is the commonest repository
// shape a stranger tries and the one it could not finish.
//
// af init wrote two personas that sign in with a password. af up then refused
// with AF-DB-022, "the personas could not be created, so signing in will not
// work", because a JSON API owns no users table, and af test exited 3 with no
// verdict. Every later step in the quickstart inherited that persona form. The
// manifest af init writes for such a repository now says login: none, which is
// the form provisioning accepts when there is nowhere to create an account,
// and af init says so under Assumed rather than leaving it to be discovered.
func TestInit_AJSONAPIGetsPersonasThatDoNotSignIn(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.test/orders\n\ngo 1.24\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "migrations"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "migrations", "0001_init.sql"),
		[]byte("CREATE TABLE IF NOT EXISTS customers (\n  id bigserial PRIMARY KEY,\n  email text NOT NULL\n);\n"+
			"CREATE TABLE IF NOT EXISTS orders (\n  id bigserial PRIMARY KEY,\n  customer_id bigint NOT NULL\n);\n"), 0o600))

	got := runCLI(t, dir, nil, "init", "--non-interactive")
	require.Zero(t, got.code, got.stderr)

	body, err := os.ReadFile(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)
	// Anchored to the field, because the note beside the personas mentions
	// 'login: none' in prose for the reader who has to change it.
	require.Regexp(t, `(?m)^\s+login: none$`, string(body),
		"a password persona on a service with no users table is refused by af up")
	require.NotRegexp(t, `(?m)^\s+login: password$`, string(body))

	// Said, not silent. A reader whose application does have a sign in needs to
	// know the line is there and that it was a guess.
	require.Contains(t, prose(got.stdout), "they never sign in (login: none)")
	require.Contains(t, prose(got.stdout), "nothing here declares a users table")

	// And said in the file too, which is what a reader who never ran the
	// command has. Including what the drafted workflow cannot do here.
	require.Contains(t, string(body), "nothing here renders a page")
	require.Contains(t, string(body), "no form to fill")

	// And the draft is still one af up would accept.
	explained := runCLI(t, dir, nil, "explain")
	require.Zero(t, explained.code, explained.stderr)
}

// The other direction, which must not regress: a repository that renders pages
// keeps the personas that sign in with a password.
func TestInit_ARepositoryWithPagesKeepsPasswordPersonas(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte(
		`{"name":"shopfront","scripts":{"start":"next start"},"dependencies":{"next":"15.1.0"}}`), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "app"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app", "page.tsx"),
		[]byte("export default function Page() { return <main>hello</main> }\n"), 0o600))

	got := runCLI(t, dir, nil, "init", "--non-interactive")
	require.Zero(t, got.code, got.stderr)

	body, err := os.ReadFile(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)
	require.Regexp(t, `(?m)^\s+login: password$`, string(body))
	require.NotRegexp(t, `(?m)^\s+login: none$`, string(body))
	require.Contains(t, prose(got.stdout), "they sign in with a password")
}
