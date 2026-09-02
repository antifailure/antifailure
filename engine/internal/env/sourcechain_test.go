package env

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The variable naming production is looked up where the documentation says it
// is looked up.
//
// The secrets guide opens with source_url_env as its example and then lists
// four places a value is found: this shell, .env, the encrypted local store,
// and the system keyring. This one variable read the first and no others, so a
// project that put the production URL in .env, beside the STRIPE_SECRET_KEY
// that `af up` finds there, was told the variable held nothing. The two places
// a production credential actually belongs, the encrypted store and the
// keyring, were unreachable for it.
//
// The orderings are what these cover: the shell alone, .env alone, both at once
// where the shell has to win, and neither.

func chainOrchestrator(t *testing.T, root string, environ map[string]string) *Orchestrator {
	t.Helper()
	o, err := New(Options{
		Root: root,
		Manifest: &schema.Manifest{
			Name:     "app",
			Database: &schema.Database{SourceURLEnv: "PRODUCTION_DATABASE_URL"},
		},
		Branch:   "main",
		Clock:    clock.New(),
		Redactor: redact.New(),
		Getenv:   func(k string) string { return environ[k] },
	})
	require.NoError(t, err)
	return o
}

func writeDotEnv(t *testing.T, root, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, ".env"), []byte(body), 0o600))
}

func TestSourceURL_ComesFromDotEnvWhenTheShellHasNothing(t *testing.T) {
	root := t.TempDir()
	writeDotEnv(t, root, "PRODUCTION_DATABASE_URL=postgres://reader:secret@db.internal:5432/app\n")
	o := chainOrchestrator(t, root, nil)

	value, err := o.sourceURL(t.Context())
	require.NoError(t, err)
	require.False(t, value.IsZero(), "the value was on disk and the refusal said it was nowhere")
	require.Equal(t, "postgres://reader:secret@db.internal:5432/app", value.Reveal())
}

func TestSourceURL_TheShellBeatsDotEnv(t *testing.T) {
	// The documented order, and the reason it is that way round: somebody who
	// typed an export meant it and is usually pointing this at a different
	// database on purpose.
	root := t.TempDir()
	writeDotEnv(t, root, "PRODUCTION_DATABASE_URL=postgres://reader:secret@db.internal:5432/from-file\n")
	o := chainOrchestrator(t, root,
		map[string]string{"PRODUCTION_DATABASE_URL": "postgres://reader:secret@db.internal:5432/from-shell"})

	value, err := o.sourceURL(t.Context())
	require.NoError(t, err)
	require.Contains(t, value.Reveal(), "from-shell",
		"an export has to beat a file, or a temporary override is not one")
}

func TestSourceURL_NowhereAtAllIsStillNowhere(t *testing.T) {
	o := chainOrchestrator(t, t.TempDir(), nil)

	value, err := o.sourceURL(t.Context())
	require.NoError(t, err)
	require.True(t, value.IsZero())
}

func TestSourceURL_AValueThatIsOnlyWhitespaceIsNotAValue(t *testing.T) {
	// A CI step that sets the variable from a secret that does not exist
	// exports an empty one, and an empty connection string is not a source.
	root := t.TempDir()
	o := chainOrchestrator(t, root, map[string]string{"PRODUCTION_DATABASE_URL": "   "})

	value, err := o.sourceURL(t.Context())
	require.NoError(t, err)
	require.True(t, value.IsZero())
}

// The refusal has to hold once the chain is consulted, and it has to name the
// places somebody could put the value rather than only the shell.
func TestRefresh_TheRefusalNamesEverySourceThatWasSearched(t *testing.T) {
	o, s, prov := refreshFixture(t,
		&schema.Database{SourceURLEnv: "PRODUCTION_DATABASE_URL"}, nil)

	_, err := o.refreshWithin(t.Context(), s)
	require.Equal(t, aferrors.AFDB016, codeOf(err))
	require.Zero(t, prov.refreshes)

	var coded *aferrors.Error
	require.True(t, aferrors.As(err, &coded))
	require.Contains(t, coded.NextStep(), ".env",
		"telling somebody to export it when .env is searched too is half the answer")
	require.Contains(t, coded.NextStep(), "af secret set",
		"and the encrypted store is where a production credential belongs")
}

// A refresh whose source is in .env goes ahead. Without this the guard above
// could be satisfied by refusing everything.
func TestRefresh_ASourceInDotEnvReachesTheProvider(t *testing.T) {
	o, s, prov := refreshFixture(t,
		&schema.Database{SourceURLEnv: "PRODUCTION_DATABASE_URL"}, nil)
	writeDotEnv(t, o.opts.Root,
		"PRODUCTION_DATABASE_URL=postgres://reader:secret@db.internal:5432/app\n")

	_, err := o.refreshWithin(t.Context(), s)
	require.NotEqual(t, aferrors.AFDB016, codeOf(err))
	require.Equal(t, 1, prov.refreshes)
}
