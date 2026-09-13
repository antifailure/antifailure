package detect_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A dependency was dropped when its name looked like a store, whatever the
// named service actually runs. db is the conventional name for a database, so
// a service of the application's own called db, built from the repository,
// lost every dependency on it. The environment then starts web without waiting
// for the service web was declared to need.
//
// Whether a dependency is satisfied by the environment is a fact about what
// the named service runs, and the compose file in hand says that. The name
// only decides for a service this file does not declare.
func TestRun_ADependencyOnABuiltServiceCalledDbIsKept(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"package.json": `{"name":"acme-web","scripts":{"start":"next start"},"dependencies":{"next":"15.0.0"}}`,
		"db/Dockerfile": `FROM alpine
EXPOSE 9100
CMD ["/bin/ledger"]
`,
		"docker-compose.yml": `services:
  web:
    build: .
    ports:
      - "3000:3000"
    depends_on:
      - db
  db:
    build: ./db
`,
	}
	res := run(t, "myrepo", files)

	serviceNamed(t, res.Draft, "db")
	require.Equal(t, []string{"db"}, serviceNamed(t, res.Draft, "acme-web").DependsOn,
		"db is built from this repository, so web's dependency on it is a dependency on a declared service")
	requireDraftValidates(t, res.Draft, files)
}
