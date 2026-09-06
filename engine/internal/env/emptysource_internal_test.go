package env

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/journal"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/state"
	"github.com/antifailure/antifailure/engine/internal/testutil/fakes"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// An empty golden is loud.
//
// A golden built from nothing and a masked copy of production produce the
// same progress lines, the same events and the same green environment. The
// difference is every row, and until this nothing said so: a manifest with no
// database.source_url_env got an environment whose migrations built the
// schema, whose workflows ran against no data, and whose report read exactly
// like a run against production.

// emptySourceFixture is an orchestrator with a session complete enough to
// branch a golden out of the in memory provider, capturing every progress
// line and every event.
func emptySourceFixture(t *testing.T, db *schema.Database) (*Orchestrator, *session, *[]string, *events.MemorySink) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	st, err := state.Open(ctx, root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	c := clock.New()
	bus := events.NewBus(c)
	sink := events.NewMemorySink(64)
	bus.AddSink(sink)

	var progress []string
	o, err := New(Options{
		Root: root, Manifest: &schema.Manifest{Name: "app", Database: db}, Branch: "main",
		Clock: c, Redactor: redact.New(),
		Getenv: func(k string) string {
			if k == MaskingKeyEnv {
				return "a-project-key-long-enough-to-be-accepted"
			}
			return ""
		},
		Progress: func(line string) { progress = append(progress, line) },
	})
	require.NoError(t, err)
	s := &session{db: st, bus: bus, dbProv: fakes.NewInMemoryDatabase(), journal: journal.New(st, c, bus)}
	return o, s, &progress, sink
}

func countOf(lines []string, want string) int {
	n := 0
	for _, l := range lines {
		if l == want {
			n++
		}
	}
	return n
}

// The refresh itself needs a Postgres to migrate, which this fixture does
// not have, so the creation path is proved up to the event that announces it
// and the reuse path is proved end to end on a golden seeded with this
// project's identity.
func TestDatabase_CreatingAGoldenFromNothingAnnouncesItAsEmpty(t *testing.T) {
	o, s, _, sink := emptySourceFixture(t, &schema.Database{Provider: schema.DBDocker})

	_, _, _, _, _, err := o.database(t.Context(), s)
	require.Error(t, err, "there is no Postgres here for the migrations to build the schema in")

	refreshing := sink.OfType(events.GoldenRefreshing)
	require.Len(t, refreshing, 1)
	require.Equal(t, true, refreshing[0].Data["empty_source"],
		"the event a control plane reads has to carry the fact")
}

func TestDatabase_AReusedGoldenBuiltFromNothingIsSaidOnceAndReported(t *testing.T) {
	for _, tc := range []struct {
		name string
		db   *schema.Database
	}{
		{"no source named", &schema.Database{Provider: schema.DBDocker}},
		{"no database block at all", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, s, progress, _ := emptySourceFixture(t, tc.db)
			identity, err := o.GoldenIdentity()
			require.NoError(t, err)
			_, err = s.dbProv.RefreshGolden(t.Context(), provider.GoldenSpec{Provenance: identity})
			require.NoError(t, err)

			golden, _, _, _, empty, err := o.database(t.Context(), s)
			require.NoError(t, err)
			require.NotEmpty(t, golden)
			require.True(t, empty, "the caller has to be told, or the report cannot say it")
			require.Equal(t, 1, countOf(*progress, EmptySourceSentence),
				"the sentence is printed once per run, not zero times and not twice:\n%s",
				strings.Join(*progress, "\n"))
		})
	}
}

// The provenance decides. A named source or a seed command means the golden
// holds rows, and the sentence would be false.
func TestProvenance_EmptyMeansNoSourceAndNoSeed(t *testing.T) {
	require.True(t, provenance{}.empty())
	require.False(t, provenance{Source: "PRODUCTION_DATABASE_URL"}.empty())
	require.False(t, provenance{Seed: "npm run seed"}.empty())
}

func TestNoteEmptySource_SaysNothingForAGoldenWithRows(t *testing.T) {
	var progress []string
	o, err := New(Options{
		Root: t.TempDir(), Manifest: &schema.Manifest{Name: "app"}, Branch: "main",
		Clock: clock.New(), Redactor: redact.New(),
		Progress: func(line string) { progress = append(progress, line) },
	})
	require.NoError(t, err)
	o.noteEmptySource(provenance{Source: "PRODUCTION_DATABASE_URL"})
	require.Empty(t, progress)
	o.noteEmptySource(provenance{})
	require.Equal(t, []string{EmptySourceSentence}, progress)
}
