package env

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/testutil/fakes"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// A refresh whose named source holds nothing refuses rather than publishes.
//
// The defect: `af golden refresh` with database.source_url_env set and the
// variable unset copied nothing, masked nothing, verified nothing, and
// published a golden carrying this project's own provenance. It exited 0 and
// told the reader to bring an environment up from it. The `af up` guard that
// refuses an empty database for a manifest naming a source, AF-DB-012, then
// passed, because by then a golden for this project existed. The environment
// came up holding none of production's shape or volume and looking correct.
//
// The orderings are what these cover rather than the states. The variable
// absent, the variable exported and empty, the variable set, and no source
// configured at all. The last two are what keep the guard from becoming a
// refusal for everybody and for a project that has no production yet.

// countingDatabase records whether a refresh reached the provider at all.
//
// The assertion that matters is not only which error came back. It is that
// nothing was built: a guard that returns the right code after a candidate
// database has already been made and committed would still have published one.
type countingDatabase struct {
	*fakes.InMemoryDatabase
	refreshes int
}

func (d *countingDatabase) RefreshGolden(
	context.Context, provider.GoldenSpec,
) (provider.GoldenVersion, error) {
	d.refreshes++
	// Refused rather than returned, so the paths after it, which need a state
	// database this test has not opened, are never reached. What is under test
	// is whether the call happens.
	return provider.GoldenVersion{}, errors.New("the provider was asked to build a golden")
}

func refreshFixture(
	t *testing.T, db *schema.Database, environ map[string]string,
) (*Orchestrator, *session, *countingDatabase) {
	t.Helper()
	o, err := New(Options{
		Root:     t.TempDir(),
		Manifest: &schema.Manifest{Name: "app", Database: db},
		Branch:   "main",
		Clock:    clock.New(),
		Redactor: redact.New(),
		Getenv: func(k string) string {
			if k == MaskingKeyEnv {
				return "a-project-key-long-enough-to-be-accepted"
			}
			return environ[k]
		},
	})
	require.NoError(t, err)
	prov := &countingDatabase{InMemoryDatabase: fakes.NewInMemoryDatabase()}
	return o, &session{dbProv: prov}, prov
}

func TestRefresh_ANamedSourceThatIsUnsetBuildsNothing(t *testing.T) {
	o, s, prov := refreshFixture(t,
		&schema.Database{SourceURLEnv: "PRODUCTION_DATABASE_URL"}, nil)

	res, err := o.refreshWithin(t.Context(), s)
	require.Error(t, err, "an unset source published a golden of nothing")
	require.Equal(t, aferrors.AFDB016, codeOf(err))
	require.Contains(t, err.Error(), "PRODUCTION_DATABASE_URL",
		"the reader has to be told which variable to set")
	require.Empty(t, res.Version, "nothing may be published from a source that was never read")
	require.Zero(t, prov.refreshes,
		"the refusal has to come before a candidate database is built")
}

func TestRefresh_ANamedSourceExportedEmptyBuildsNothing(t *testing.T) {
	// The shape a missing CI secret takes. The step sets the variable from a
	// secret that does not exist, so it is exported and holds "". A pull
	// request from a fork gets no secrets at all, which is the run nobody
	// watches.
	o, s, prov := refreshFixture(t,
		&schema.Database{SourceURLEnv: "PRODUCTION_DATABASE_URL"},
		map[string]string{"PRODUCTION_DATABASE_URL": ""})

	_, err := o.refreshWithin(t.Context(), s)
	require.Equal(t, aferrors.AFDB016, codeOf(err))
	require.Zero(t, prov.refreshes)
}

func TestRefresh_ANamedSourceThatIsSetStillRefreshes(t *testing.T) {
	o, s, prov := refreshFixture(t,
		&schema.Database{SourceURLEnv: "PRODUCTION_DATABASE_URL"},
		map[string]string{"PRODUCTION_DATABASE_URL": "postgres://someone:secret@127.0.0.1:1/prod"})

	_, err := o.refreshWithin(t.Context(), s)
	require.NotEqual(t, aferrors.AFDB016, codeOf(err),
		"a source that is set must not be refused as missing")
	require.Equal(t, 1, prov.refreshes, "the ordinary case has to still reach the provider")
}

func TestRefresh_NoSourceConfiguredStillRefreshes(t *testing.T) {
	// A project with no production behind it is a supported configuration, and
	// the empty golden it gets is the point rather than a mistake.
	o, s, prov := refreshFixture(t, &schema.Database{Seed: "true"}, nil)

	_, err := o.refreshWithin(t.Context(), s)
	require.NotEqual(t, aferrors.AFDB016, codeOf(err))
	require.Equal(t, 1, prov.refreshes, "a seed only project has to still get its golden")
}
