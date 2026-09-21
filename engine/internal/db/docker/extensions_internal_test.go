package docker

// THE THREE PIECES OF EXTENSION SUPPORT THAT ARE PURE FUNCTIONS.
//
// Everything about extensions that needs a container is proved against one in
// extensions_live_test.go, because a claim about what a Postgres carries can
// only be settled by asking a Postgres. These are the parts that decide what
// to ask for, and each of them has a failure that produces a WORKING container
// with the wrong contents:
//
//   - a command that REPLACES shared_preload_libraries rather than adding to
//     it starts perfectly and leaves the insights reading an empty table,
//   - an image chosen from the version rather than the manifest starts
//     perfectly and has none of the extensions the schema needs,
//   - a branch preload taken from the manifest rather than from the golden
//     starts perfectly right up until the manifest changes, and then never
//     starts again.
//
// So they are measured here as values, where each one can be broken on its
// own, and again through a real server there.

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
)

func TestTheStatisticsModuleIsAddedToRatherThanReplacedByADeclaredLibrary(t *testing.T) {
	t.Parallel()

	// The shape of the whole command, once, so that a change to the flags is
	// visible here rather than only in a substring match.
	require.Equal(t, []string{
		"postgres",
		"-c", "shared_preload_libraries=pg_stat_statements",
		"-c", "pg_stat_statements.track=all",
	}, serverCmd(nil), "with nothing declared this is the command the provider has always run")

	require.Equal(t, []string{
		"postgres",
		"-c", "shared_preload_libraries=timescaledb,pg_stat_statements",
		"-c", "pg_stat_statements.track=all",
	}, serverCmd([]string{"timescaledb"}),
		"a declared library is ADDED; replacing the list is how the insights lose statement timing")

	// The declared ones lead. citus refuses to load from anywhere but the
	// front, measured: with the statistics module first the postmaster exits
	// during initdb with "Citus has to be loaded first" and the container
	// never accepts a connection at all.
	require.Equal(t, "shared_preload_libraries=citus,pg_cron,pg_stat_statements",
		serverCmd([]string{"citus", "pg_cron"})[2],
		"the declared libraries must keep their order AND come first, or citus cannot be used")

	require.Equal(t, "shared_preload_libraries=pg_stat_statements",
		serverCmd([]string{"pg_stat_statements"})[2],
		"declaring the module the provider already preloads must not list it twice")

	require.Equal(t, "shared_preload_libraries=pg_stat_statements,citus",
		serverCmd([]string{"pg_stat_statements", "citus"})[2],
		"a manifest naming the statistics module keeps the position it chose for it")

	require.Equal(t, "shared_preload_libraries=citus,pg_stat_statements",
		serverCmd([]string{" citus ", "", "citus"})[2],
		"blank and repeated entries are dropped rather than reaching the command line")
}

func TestADeclaredImageIsUsedInsteadOfTheOneBuiltFromTheVersion(t *testing.T) {
	t.Parallel()

	stock := &Provider{version: 17}
	require.Equal(t, "postgres:17-alpine", stock.imageFor(0),
		"nothing declared is the stock image, which is what every manifest written before this key means")
	require.Equal(t, "postgres:16-alpine", stock.imageFor(16),
		"the spec's version still chooses the stock tag")

	named := &Provider{version: 17, image: "pgvector/pgvector:pg17"}
	require.Equal(t, "pgvector/pgvector:pg17", named.imageFor(0),
		"a declared image is what runs")
	require.Equal(t, "pgvector/pgvector:pg17", named.imageFor(16),
		"a declared image is not overridden by the spec's version; the two are compared instead, in versionMatches")
}

func TestABranchTakesTheLibrariesItsGoldenWasBuiltWith(t *testing.T) {
	t.Parallel()

	// The manifest has since stopped asking for the library. The golden still
	// holds an extension whose library the postmaster loads before any
	// database is opened, so the golden's record has to win or the branch is a
	// container that cannot start at all.
	forgetful := &Provider{preload: nil}
	require.Equal(t, []string{"timescaledb"},
		forgetful.branchPreload(map[string]string{dockerutil.LabelPreload: "timescaledb"}),
		"the golden records what it was built with and that is what a branch of it runs")

	// The manifest asks for something else entirely. Still the golden's.
	changed := &Provider{preload: []string{"pg_cron"}}
	require.Equal(t, []string{"citus"},
		changed.branchPreload(map[string]string{dockerutil.LabelPreload: "citus"}),
		"the golden's record wins over what the manifest now says, because only one of the two built this image")

	require.Equal(t, []string{"citus", "pg_cron"},
		changed.branchPreload(map[string]string{dockerutil.LabelPreload: " citus , pg_cron "}),
		"the label is comma separated and its spacing is not meaningful")

	// A golden built before the label existed, or with nothing extra. The
	// manifest is the only other source there is, and falling back to it can
	// only add libraries to a server.
	require.Equal(t, []string{"pg_cron"}, changed.branchPreload(nil),
		"an unlabelled golden falls back to the manifest rather than to nothing")
	require.Equal(t, []string{"pg_cron"},
		changed.branchPreload(map[string]string{dockerutil.LabelPreload: ""}),
		"an empty label is the same case as an absent one")
}
