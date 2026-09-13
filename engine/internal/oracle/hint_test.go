package oracle_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/oracle"
)

// digestOf is a fixed high entropy value, the way a hash column holds one.
func digestOf(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

// oneAccount is a table holding one row, the same key on both sides, with the
// columns a test wants.
func oneAccount(row map[string]any) *oracle.Snapshot {
	cols := []oracle.Column{}
	for name := range row {
		cols = append(cols, oracle.Column{Name: name, Type: "text"})
	}
	row["id"] = "1"
	cols = append(cols, oracle.Column{Name: "id", Type: "text"})
	return &oracle.Snapshot{Tables: []oracle.Table{{
		Schema: "public", Name: "accounts", Key: []string{"id"}, Columns: cols,
		Rows: map[string]map[string]any{`"1"`: row}, RowCount: 1,
	}}}
}

func onlyFinding(t *testing.T, base, cand map[string]any) oracle.Finding {
	t.Helper()
	res := oracle.Compare(oracle.Input{BaselineAfter: oneAccount(base), CandidateAfter: oneAccount(cand)})
	// Never suppressed: the difference is reported whether or not it carries a
	// hint, which is the half of this a hint must not take away.
	require.Lenf(t, res.Findings, 1, "%+v", res.Findings)
	require.Equal(t, oracle.KindRowChanged, res.Findings[0].Kind)
	return res.Findings[0]
}

// A password hashed under each side's own salt differs on every build. The
// finding stays, and it names the exact entries that would quiet it.
func TestASaltedSecretThatDiffersIsReportedWithTheIgnoreEntryToAdd(t *testing.T) {
	base := map[string]any{
		"password_hash": `\x` + digestOf("baseline hash") + digestOf("baseline hash 2"),
		"password_salt": `\x` + digestOf("baseline salt"),
	}
	cand := map[string]any{
		"password_hash": `\x` + digestOf("candidate hash") + digestOf("candidate hash 2"),
		"password_salt": `\x` + digestOf("candidate salt"),
	}
	f := onlyFinding(t, base, cand)
	require.Contains(t, f.Hint, "add `$.password_hash` and `$.password_salt` to oracle.ignore.fields")

	res := oracle.Compare(oracle.Input{BaselineAfter: oneAccount(base), CandidateAfter: oneAccount(cand)})
	require.Contains(t, res.Text(), "hint: password_hash and password_salt hold values")
	require.Contains(t, res.Markdown(), "`$.password_hash`")
}

// A chain digest in a camel case column, in base64 rather than hexadecimal.
func TestAChainDigestInBase64IsReportedWithTheIgnoreEntryToAdd(t *testing.T) {
	f := onlyFinding(t,
		map[string]any{"entryHash": "q83vEjRWeJqrze8SNFZ4mqvN7xI0VniaE8hPq2Lq1zY="},
		map[string]any{"entryHash": "Zk9pXw2Rb7tLmQeN4vC1sY8uJ3hA6dG0kP5xW9zT2oI="})
	require.Contains(t, f.Hint, "add `$.entryHash` to oracle.ignore.fields")
}

// The other half. Each of these is a real difference that must be reported
// with no hint, because a hint on it would tell somebody to ignore a column
// that is not a digest or a value that is not a random one.
func TestAnOrdinaryChangedColumnCarriesNoHint(t *testing.T) {
	for _, tc := range []struct {
		name       string
		base, cand map[string]any
	}{
		{"a name that changed", map[string]any{"name": "Preview Owner"}, map[string]any{"name": "Preview Owner Renamed"}},
		{"random values in a column not named like a digest",
			map[string]any{"external_ref": digestOf("a")}, map[string]any{"external_ref": digestOf("b")}},
		{"a digest column holding the same character repeated",
			map[string]any{"password_hash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, map[string]any{"password_hash": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},
		{"a digest column whose two values differ in length",
			map[string]any{"password_hash": digestOf("a")}, map[string]any{"password_hash": digestOf("b")[:40]}},
		{"a word that merely contains hash", map[string]any{"hashtag": digestOf("a")}, map[string]any{"hashtag": digestOf("b")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := onlyFinding(t, tc.base, tc.cand)
			require.Empty(t, f.Hint)
		})
	}
}
