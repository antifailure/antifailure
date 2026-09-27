package docker

// THE PARTS OF OPENING ANOTHER BUILD'S DATA DIRECTORY THAT ARE PURE FUNCTIONS.
//
// The classification is the one worth testing without a daemon, because the
// inputs that matter are the ones that are hard to produce on demand. A data
// directory written by a build compiled with a different block size, or carrying
// a catalog version the other build does not know, are exactly the cases a
// storage engine developer meets and exactly the ones no pair of published
// images reproduces. The strings below are the postmaster's own, from
// PostgreSQL's src/backend/access/transam/xlog.c and src/backend/utils/init,
// so the classifier is aimed at what a server really prints rather than at what
// this repository imagines it prints.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// A server that refused the data directory says so in a FATAL or a PANIC, and
// only those lines are the finding. A log full of LOG and WARNING lines is a
// server that opened the directory perfectly well, and reading one as a refusal
// would let a container that died for an unrelated reason wear this finding's
// words.
func TestOnlyTheServersOwnRefusalsAreRead(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		log  string
		want string
	}{
		{
			name: "a catalog version the other build does not know",
			log: strings.Join([]string{
				"The files belonging to this database system will be owned by user \"postgres\".",
				"2026-09-24 10:00:00.000 UTC [1] FATAL:  database files are incompatible with server",
				"2026-09-24 10:00:00.000 UTC [1] DETAIL:  The data directory was initialized by " +
					"PostgreSQL version 17, which is not compatible with this version 16.10.",
			}, "\n"),
			want: "FATAL:  database files are incompatible with server",
		},
		{
			name: "a different block size, which is the storage engine case",
			log: "2026-09-24 10:00:00.000 UTC [1] FATAL:  database files are incompatible with server\n" +
				"2026-09-24 10:00:00.000 UTC [1] DETAIL:  The database cluster was initialized with " +
				"BLCKSZ 8192, but the server was compiled with BLCKSZ 16384.",
			want: "BLCKSZ",
		},
		{
			name: "a write ahead log the other build cannot replay",
			log:  "2026-09-24 10:00:00.000 UTC [1] PANIC:  could not locate a valid checkpoint record",
			want: "PANIC:  could not locate a valid checkpoint record",
		},
		{
			name: "a data directory the server may not read",
			log: "2026-09-24 10:00:00.000 UTC [1] FATAL:  data directory " +
				"\"/var/lib/antifailure/pgdata\" has invalid permissions",
			want: "has invalid permissions",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			said, ok := refusalLines(c.log)
			require.True(t, ok, "this is the server refusing, and it has to be read as one")
			require.Contains(t, said, c.want)
		})
	}
}

// A healthy start, a server that says nothing, and an empty log are all "not
// this finding". Each one is a real case: a container killed for memory leaves a
// log that ends mid work, and a daemon that would not answer leaves none at all.
func TestALogWithNoRefusalIsNotTheFinding(t *testing.T) {
	t.Parallel()
	healthy := strings.Join([]string{
		"2026-09-24 10:00:00.000 UTC [1] LOG:  starting PostgreSQL 17.6",
		"2026-09-24 10:00:00.000 UTC [1] LOG:  database system was shut down at 09:59:59 UTC",
		"2026-09-24 10:00:00.000 UTC [1] WARNING:  database \"antifailure\" has a collation " +
			"version mismatch",
		"2026-09-24 10:00:00.000 UTC [1] LOG:  database system is ready to accept connections",
	}, "\n")
	for _, log := range []string{healthy, "", "\n\n", "some output with no verdict in it at all"} {
		said, ok := refusalLines(log)
		require.False(t, ok, "must not read %q as a refusal", log)
		require.Empty(t, said)
	}
}

// Repeated lines are collapsed and the list is capped, because a message is an
// explanation and not a wall. A restarting server prints the same FATAL once per
// attempt.
func TestRefusalLinesCollapseRepeatsAndAreCapped(t *testing.T) {
	t.Parallel()
	repeated := strings.Repeat("FATAL:  database files are incompatible with server\n", 9)
	said, ok := refusalLines(repeated)
	require.True(t, ok)
	require.Equal(t, 1, strings.Count(said, "FATAL"), "one line, six times, is one line")

	var distinct strings.Builder
	for i := 0; i < 9; i++ {
		distinct.WriteString("FATAL:  refusal number ")
		distinct.WriteByte(byte('0' + i))
		distinct.WriteString("\n")
	}
	said, ok = refusalLines(distinct.String())
	require.True(t, ok)
	require.Equal(t, maxRefusalLines, strings.Count(said, "FATAL"), "capped")
}

// One golden and one image is one derived image, whatever the order of events,
// so a second branch of the same pairing finds the copy the first one made. Two
// images are two, because they are two different data directories once opened.
// And the tag is a legal Docker tag, which is the whole reason the image
// reference is hashed rather than embedded.
func TestTheDerivedTagIsDeterministicAndPerImage(t *testing.T) {
	t.Parallel()
	const id1 = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	const id2 = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	a := rebaseTag("gv_20260924_abcd", "pgvector/pgvector:pg17", id1)
	b := rebaseTag("gv_20260924_abcd", "pgvector/pgvector:pg17", id1)
	c := rebaseTag("gv_20260924_abcd", "postgres:17-alpine", id1)
	d := rebaseTag("gv_20260924_efgh", "pgvector/pgvector:pg17", id1)
	require.Equal(t, a, b, "the same pairing must find the same copy")
	require.NotEqual(t, a, c, "two builds are two copies")
	require.NotEqual(t, a, d, "two goldens are two copies")

	// THE ASSERTION THE FIRST VERSION OF THIS KEY DID NOT SATISFY, and the one
	// the people this feature is for meet on every iteration: the same reference
	// rebuilt in place is a different build, so it must be a different copy.
	// Keyed on the reference alone, a and e were equal and the branch ran the
	// image the previous build had left behind.
	e := rebaseTag("gv_20260924_abcd", "pgvector/pgvector:pg17", id2)
	require.NotEqual(t, a, e,
		"a rebuilt image under the same name is a different build and must not reuse its copy")

	require.True(t, strings.HasPrefix(a, RebaseRepo+":"),
		"a copy must not live under the golden repository, where ListGoldens would find it")
	tag := strings.TrimPrefix(a, RebaseRepo+":")
	require.LessOrEqual(t, len(tag), 128)
	for _, r := range tag {
		require.True(t,
			r == '_' || r == '.' || r == '-' ||
				(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'),
			"a Docker tag may not contain %q, and the image reference is hashed for exactly that reason", r)
	}
}

// The major comparison, which two callers make and which therefore lives in one
// place. The zero cases are the ones worth pinning: a version nobody could read
// must not refuse a build, and a manifest that declares nothing has nothing to
// disagree with.
func TestTheMajorComparisonRefusesADisagreementAndNotAnUnknown(t *testing.T) {
	t.Parallel()
	require.NoError(t, majorMatches(0, 17, "pg:x"), "could not ask is not a mismatch")
	require.NoError(t, majorMatches(16, 0, "pg:x"), "nothing declared is nothing to disagree with")
	require.NoError(t, majorMatches(17, 17, "pg:x"))

	err := majorMatches(16, 17, "pgvector/pgvector:pg16")
	require.Error(t, err)
	var coded *aferrors.Error
	require.ErrorAs(t, err, &coded)
	require.Equal(t, aferrors.AFDB039, coded.Code())
	require.Contains(t, err.Error(), "pgvector/pgvector:pg16")
	require.Contains(t, err.Error(), "16")
	require.Contains(t, err.Error(), "17")
}

// A build stream carries its own failure, and the reader has to find it: the
// daemon returns a reader and a nil error for a build that goes wrong part way
// through, so a reader that only checked the error would report a copy that
// never happened as a success.
func TestTheCopysStreamCarriesItsOwnFailure(t *testing.T) {
	t.Parallel()
	require.NoError(t, readBuildStream(strings.NewReader(
		`{"stream":"Step 1/2 : FROM postgres:17-alpine\n"}`+"\n"+
			`{"stream":"Successfully tagged antifailure/rebased:gv_x-on-abc\n"}`+"\n")))

	err := readBuildStream(strings.NewReader(
		`{"stream":"Step 2/2 : COPY --from=antifailure/golden:gv_x /d /d\n"}` + "\n" +
			`{"error":"failed to compute cache key: not found"}` + "\n"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to compute cache key")
	require.Contains(t, err.Error(), "COPY", "the last output is what says where it got to")

	// A stream cut off part way is a build that was killed rather than one that
	// finished, which is the case a process killed for memory produces and the
	// one that has no verdict in its log at all.
	truncated := readBuildStream(strings.NewReader(`{"stream":"Step 1/2 : FROM postgr`))
	require.Error(t, truncated)
	require.Contains(t, truncated.Error(), "reading the copy's progress")
}

// The image a message about a branch names is the one somebody typed, never the
// derived copy, whose tag carries a digest nobody chose and which nothing they
// could do acts on.
func TestAMessageNamesTheImageSomebodyTyped(t *testing.T) {
	t.Parallel()
	p := &Provider{version: 17}
	require.Equal(t, "postgres:17-alpine", p.branchImageOrDeclared())

	p.image = "pgvector/pgvector:pg17"
	require.Equal(t, "pgvector/pgvector:pg17", p.branchImageOrDeclared())

	p.branchImage = "timescale/timescaledb:2.17.2-pg17"
	require.Equal(t, "timescale/timescaledb:2.17.2-pg17", p.branchImageOrDeclared())
}
