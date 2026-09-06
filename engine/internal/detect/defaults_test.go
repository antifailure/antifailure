package detect_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/detect"
)

// Every question has a default, and the default is the language's own.
//
// A port nothing in the repository names used to be a question with no
// default, so an unattended run had nothing to take and refused. For a pull
// request check with nobody at the keyboard that meant no check at all. A
// port that is wrong is found in seconds by a readiness probe that never
// answers; a check that never ran is found by nobody.

func questionNamed(t *testing.T, res *detect.Result, id string) detect.Question {
	t.Helper()
	for _, q := range res.Questions {
		if q.ID == id {
			return q
		}
	}
	var ids []string
	for _, q := range res.Questions {
		ids = append(ids, q.ID)
	}
	t.Fatalf("no question %q; the questions are %v", id, ids)
	return detect.Question{}
}

func TestDefaults_APortNothingNamesIsTheLanguages(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		files map[string]string
		port  string
	}{
		{"node", map[string]string{
			// A start script and no framework: a service the node analyzer
			// declares with no port of its own.
			"package.json": `{"name":"web","scripts":{"start":"node server.js"},"dependencies":{"pino":"9.0.0"}}`,
		}, "3000"},
		{"unknown language", map[string]string{
			"Dockerfile": "FROM alpine\nCMD [\"/bin/app\"]\n",
		}, "3000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := run(t, "repo", tc.files)
			require.Len(t, res.Draft.Services, 1)
			q := questionNamed(t, res, "service."+res.Draft.Services[0].Name+".port")
			require.Equal(t, tc.port, q.Default, "the question carries no default, so an unattended run refuses")
			require.Contains(t, q.Why, "No port was found")
			require.Contains(t, q.Why, tc.port, "the reason has to say where the number came from")
		})
	}
}

func TestDefaults_AStartCommandNothingNamesIsTheConventionalOne(t *testing.T) {
	t.Parallel()
	// An express dependency without a start script: a web service the node
	// analyzer declares with no command.
	res := run(t, "repo", map[string]string{
		"package.json": `{"name":"web","dependencies":{"express":"4.19.0"}}`,
	})
	q := questionNamed(t, res, "service.web.command")
	require.Equal(t, "npm start", q.Default)
	require.Contains(t, q.Why, "npm start")
}

// The production variable, when the repository already names it, becomes
// database.source_url_env. That is what lets af init write the masking rules
// from the real schema in the same run, and what the report's empty database
// sentence tells people to add when it is absent.
func TestDefaults_AProductionVariableTheRepositoryNamesBecomesTheSource(t *testing.T) {
	t.Parallel()
	res := run(t, "repo", map[string]string{
		"package.json": `{"name":"web","scripts":{"start":"node server.js"},"dependencies":{"pg":"8.11.0"}}`,
		".env.example": "DATABASE_URL=\nPRODUCTION_DATABASE_URL=\n",
	})
	require.NotNil(t, res.Draft.Database)
	require.Equal(t, "DATABASE_URL", res.Draft.Database.URLEnv)
	require.Equal(t, "PRODUCTION_DATABASE_URL", res.Draft.Database.SourceURLEnv)

	plain := run(t, "repo", map[string]string{
		"package.json": `{"name":"web","scripts":{"start":"node server.js"},"dependencies":{"pg":"8.11.0"}}`,
		".env.example": "DATABASE_URL=\n",
	})
	require.Empty(t, plain.Draft.Database.SourceURLEnv,
		"the application's own variable is not production's, and naming it as the source would branch the wrong database")
}
