package clickhouse

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// The pure parts, which are the parts a live test cannot show going wrong.
//
// A statement that is retargeted at the wrong database still runs, and a
// Replicated engine rewritten badly still parses, so the live tests would go
// green over both. These are the assertions that can say no about the exact
// text.

// realCreateStatements are what a server actually returns for
// create_table_query, copied from one rather than written from the
// documentation.
func TestRetarget_MovesTheStatementToAnotherDatabase(t *testing.T) {
	t.Parallel()
	const ddl = "CREATE TABLE af_l42.events (`uuid` UUID, `event` String, " +
		"`distinct_id` String, `email` Nullable(String)) ENGINE = MergeTree ORDER BY uuid " +
		"SETTINGS index_granularity = 8192"

	got, err := retarget(ddl, "af_l42", "af_cand_9f", "events")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(got, "CREATE TABLE `af_cand_9f`.`events` ("), got)
	// Everything after the name is carried through untouched, which is what
	// keeps the partitioning, the ordering, the codecs and the settings the
	// same as production's.
	require.Contains(t, got, "ENGINE = MergeTree ORDER BY uuid SETTINGS index_granularity = 8192")
	require.NotContains(t, got, "af_l42")
}

// TestRetarget_DoesNotRenameAColumnThatSharesTheTableName is the reason the
// name is replaced by span rather than by search and replace.
func TestRetarget_DoesNotRenameAColumnThatSharesTheTableName(t *testing.T) {
	t.Parallel()
	const ddl = "CREATE TABLE prod.events (`id` UUID, `events` String) ENGINE = MergeTree ORDER BY id"
	got, err := retarget(ddl, "prod", "cand", "events")
	require.NoError(t, err)
	require.Contains(t, got, "`events` String",
		"a column called events was renamed with the table, so the copy has a column "+
			"production does not")
	require.True(t, strings.HasPrefix(got, "CREATE TABLE `cand`.`events` ("), got)
}

func TestRetarget_RefusesAStatementItCannotRead(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, ddl, wants string
	}{
		{"not a create table", "CREATE VIEW prod.events AS SELECT 1", "does not begin with"},
		{"no column list", "CREATE TABLE prod.events", "no column list"},
		{"names something else", "CREATE TABLE prod.other (id UUID) ENGINE = MergeTree", "expected the table's own name"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := retarget(tc.ddl, "prod", "cand", "events")
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wants)
		})
	}
}

// TestSingleNode_RewritesAReplicatedEngine is the one transformation this
// package makes to somebody else's DDL.
//
// A golden is one database on one server. A ReplicatedMergeTree names a
// coordination path and a replica, both of which belong to the cluster it came
// from, and recreated verbatim it either collides with production's path or
// waits for a keeper that is not there. Every argument after those two is the
// engine's own and is kept: dropping the version column of a
// ReplacingMergeTree would change which row wins.
func TestSingleNode_RewritesAReplicatedEngine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, in, want string }{
		{
			"path and replica dropped",
			"(id UUID) ENGINE = ReplicatedMergeTree('/clickhouse/tables/{shard}/events', '{replica}') ORDER BY id",
			"(id UUID) ENGINE = MergeTree() ORDER BY id",
		},
		{
			"the engine's own arguments are kept",
			"(id UUID, ver UInt64) ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/e', '{replica}', ver) ORDER BY id",
			"(id UUID, ver UInt64) ENGINE = ReplacingMergeTree(ver) ORDER BY id",
		},
		{
			"the default path form has nothing to drop",
			"(id UUID) ENGINE = ReplicatedMergeTree ORDER BY id",
			"(id UUID) ENGINE = MergeTree ORDER BY id",
		},
		{
			"a plain engine is untouched",
			"(id UUID) ENGINE = MergeTree PARTITION BY toYYYYMM(ts) ORDER BY id",
			"(id UUID) ENGINE = MergeTree PARTITION BY toYYYYMM(ts) ORDER BY id",
		},
		{
			"a comma inside an argument is not an argument boundary",
			"(id UUID) ENGINE = ReplicatedSummingMergeTree('/x/{shard}', '{replica}', (a, b)) ORDER BY id",
			"(id UUID) ENGINE = SummingMergeTree((a, b)) ORDER BY id",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, singleNode(tc.in))
		})
	}
}

// TestDecodeRowBinary_ReadsTheTypesAScanReturns covers the wire format every
// read in this package goes through.
func TestDecodeRowBinary_ReadsTheTypesAScanReturns(t *testing.T) {
	t.Parallel()
	body := rowBinary(
		[]string{"a", "b", "c"},
		[]string{"String", "Nullable(String)", "LowCardinality(String)"},
		[][]any{
			{"one", "two", "three"},
			{"four", nil, "six"},
		})

	var rows [][]string
	require.NoError(t, decodeRowBinary(bufio.NewReader(bytes.NewReader(body)), func(r [][]byte) error {
		row := make([]string, 0, len(r))
		for _, v := range r {
			if v == nil {
				row = append(row, "<null>")
				continue
			}
			row = append(row, string(v))
		}
		rows = append(rows, row)
		return nil
	}))
	require.Equal(t, [][]string{
		{"one", "two", "three"},
		{"four", "<null>", "six"},
	}, rows)
}

// TestDecodeRowBinary_ReadsLowCardinality is the case that decides whether a
// real analytics schema can be scanned at all.
//
// LowCardinality(String) is what such a schema gives every event name, every
// property key and most of its identifiers, and RowBinary writes one as a
// plain length prefixed string. A decoder that refused the NAME would refuse
// the column, and the verification scan of the store this lane exists for
// would fail on the first column it read.
func TestDecodeRowBinary_ReadsLowCardinality(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{
		"String", "Nullable(String)", "LowCardinality(String)",
		"LowCardinality(Nullable(String))", "Nullable(LowCardinality(String))",
	} {
		typ := typ
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			base, nullable := unwrapType(typ)
			require.Equal(t, "String", base)
			require.Equal(t, strings.Contains(typ, "Nullable"), nullable)
		})
	}
}

// TestDecodeRowBinary_RefusesAWidthItCannotKnow is the refusal that keeps a
// misaligned scan from reporting the wrong column clean.
func TestDecodeRowBinary_RefusesAWidthItCannotKnow(t *testing.T) {
	t.Parallel()
	body := rowBinary([]string{"n"}, []string{"UInt64"}, nil)
	err := decodeRowBinary(bufio.NewReader(bytes.NewReader(body)), func([][]byte) error { return nil })
	require.Error(t, err)
	require.Contains(t, err.Error(), "UInt64")
	require.Contains(t, err.Error(), "misaligns every column after it")
}

// rowBinary builds a RowBinaryWithNamesAndTypes body, which is what a server
// sends. Written here rather than captured, so a case can be constructed that
// a server would not readily produce.
func rowBinary(names, types []string, rows [][]any) []byte {
	var buf bytes.Buffer
	writeUvarint(&buf, uint64(len(names)))
	for _, n := range names {
		writeBinaryString(&buf, n)
	}
	for _, ty := range types {
		writeBinaryString(&buf, ty)
	}
	for _, row := range rows {
		for i, v := range row {
			_, nullable := unwrapType(types[i])
			if nullable {
				if v == nil {
					buf.WriteByte(1)
					continue
				}
				buf.WriteByte(0)
			}
			writeBinaryString(&buf, v.(string))
		}
	}
	return buf.Bytes()
}

func writeUvarint(buf *bytes.Buffer, n uint64) {
	var b [binary.MaxVarintLen64]byte
	buf.Write(b[:binary.PutUvarint(b[:], n)])
}

// TestPlainSortingKeyColumns_DropsTheExpressions is what stops a statement
// naming an identifier that does not exist.
//
// A ClickHouse sorting key is a list of EXPRESSIONS and an analytics table's
// is mostly expressions. Only the plain names are columns.
func TestPlainSortingKeyColumns_DropsTheExpressions(t *testing.T) {
	t.Parallel()
	require.Equal(t,
		[]string{"team_id", "event"},
		plainSortingKeyColumns("team_id, toDate(timestamp), event, cityHash64(distinct_id, uuid)"))
	require.Empty(t, plainSortingKeyColumns(""))
	require.Empty(t, plainSortingKeyColumns("tuple()"))
	require.Equal(t, []string{"uuid"}, plainSortingKeyColumns("uuid"))
}

// TestShortHash_SeparatesNamesASanitisationWouldMerge is why an identifier
// this package generates is a hash rather than a cleaned up name.
//
// Two environments whose identifiers differ only in a character an identifier
// cannot hold would share one branch database, which is the worst failure this
// package could have: one environment's writes in another's twin.
func TestShortHash_SeparatesNamesASanitisationWouldMerge(t *testing.T) {
	t.Parallel()
	require.NotEqual(t, shortHash("feature/a"), shortHash("feature-a"))
	require.NotEqual(t, shortHash("feature.a"), shortHash("feature_a"))
	require.Equal(t, shortHash("stable"), shortHash("stable"))
	require.Len(t, shortHash("anything"), 12)
}

func TestParseURL_RefusesWhatItCannotSpeak(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, url, wants string }{
		{"empty", "", "is empty"},
		{"native protocol", "clickhouse://user@host:9000/db", "has to be http or https"},
		{"no host", "http:///db", "names no host"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseURL(secrets.New(tc.url))
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wants)
		})
	}
	// And the password never appears in a message, because every one of these
	// is written about a value that carries one.
	_, err := parseURL(secrets.New("clickhouse://user:hunter2@host:9000/db"))
	require.Error(t, err)
	require.NotContains(t, err.Error(), "hunter2")
}

func TestURLFor_NamesTheDatabaseAndStaysASecret(t *testing.T) {
	t.Parallel()
	c, err := parseURL(secrets.New("http://default:pw@127.0.0.1:8123/default"))
	require.NoError(t, err)
	got := c.urlFor("af_env_abc")
	require.Equal(t, "http://default:pw@127.0.0.1:8123/af_env_abc", got.Reveal())
	require.NotContains(t, got.String(), "pw")
}
