package verify

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Verification reads back every column of every table and looks for anything
// that still parses as something real.
//
// It exists because masking that is not checked is masking somebody believes
// in. A rule that did not match a column, a transform that failed on a null, a
// table added last week: each produces a golden that looks masked and is not,
// and none of them announces itself. So the golden is read back with the same
// detectors that would find the data if it leaked, and a golden that fails
// cannot be branched. That is enforced in code rather than in a checklist.
//
// It samples rather than reading everything, and says so. A full read of a
// large database on every refresh would make refreshes rare, and rare
// refreshes are how a golden drifts from production. The sample is per column
// and the attestation records its size, so the reader knows exactly what was
// checked.

// Finding is one value that still looks real.
type Finding struct {
	Schema string `json:"schema"`
	Table  string `json:"table"`
	Column string `json:"column"`
	// Detector is what recognised it.
	Detector string `json:"detector"`
	// Example is a redacted excerpt, enough to recognise the shape and not
	// enough to be the value. A report that quoted the data would be a report
	// that leaks it.
	Example string `json:"example"`
	// Rows is how many sampled rows matched.
	Rows int `json:"rows"`
}

// String renders a finding for a person.
func (f Finding) String() string {
	return fmt.Sprintf("%s.%s.%s holds %s (%d of the sampled rows, for example %s)",
		f.Schema, f.Table, f.Column, f.Detector, f.Rows, f.Example)
}

// Report is the result of a scan.
type Report struct {
	// Scanner names what produced this, so an attestation can be read by
	// something that did not produce it.
	Scanner string `json:"scanner"`
	// StartedAt and FinishedAt bound the scan.
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	// Tables and Columns are how much was looked at.
	Tables  int `json:"tables"`
	Columns int `json:"columns"`
	// RowsSampled is how many rows were read in total.
	RowsSampled int64 `json:"rows_sampled"`
	// SampleSize is the per column limit, recorded so that a reader knows
	// what "clean" covered.
	SampleSize int `json:"sample_size"`
	// Findings are what was found. Empty means the golden may be branched.
	Findings []Finding `json:"findings"`
	// Skipped names columns that could not be read, with the reason. A column
	// nobody could read is not a column that passed.
	Skipped []string `json:"skipped,omitempty"`
}

// Clean reports whether the golden may be branched.
//
// Skipped counts, and it did not. The sentence above the field says a column
// nobody could read is not a column that passed, and the comment above the
// append that fills it says ignoring one would let an unreadable column count
// as a clean one. Both were true statements about a rule nothing enforced:
// this read only Findings, so a scan that failed on a column returned clean and
// env/golden.go published the golden on the strength of it.
//
// Which is the one way a golden could pass verification without having been
// verified, and it is worse than a finding, because a finding is a column the
// scan READ and disliked while a skip is a column it never saw at all.
//
// Reading it as unclean is deliberately the strict direction. A scan that
// cannot read a column is a scan whose answer is "I do not know", and the whole
// design of this package is that not knowing fails rather than passes.
func (r Report) Clean() bool { return len(r.Findings) == 0 && len(r.Skipped) == 0 }

// DefaultSampleSize is how many rows per column are read.
//
// Large enough that a column of real data is found with near certainty, and
// small enough that a scan of a wide schema is seconds rather than minutes. A
// column where one row in ten thousand is real is not a column that was
// masked; the failure mode being guarded against is a rule that missed
// entirely, and that shows up in the first hundred rows.
const DefaultSampleSize = 2000

// Options configure a scan.
type Options struct {
	// SampleSize is rows per column. Zero uses the default.
	SampleSize int
	// Progress receives a line per table, and may be nil.
	Progress func(string)
	// Now is the time source.
	Now func() time.Time
}

// Scan reads back a database and reports what still looks real.
func Scan(ctx context.Context, conn *pgx.Conn, opts Options) (Report, error) {
	if opts.SampleSize <= 0 {
		opts.SampleSize = DefaultSampleSize
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	report := Report{
		Scanner: "antifailure/verify/1", StartedAt: opts.Now().UTC(),
		SampleSize: opts.SampleSize,
	}

	columns, err := textColumns(ctx, conn)
	if err != nil {
		return report, err
	}

	seenTables := map[string]bool{}
	for _, c := range columns {
		seenTables[c.schema+"."+c.table] = true
		report.Columns++

		rows, sampled, scanErr := scanColumn(ctx, conn, c, opts.SampleSize)
		if scanErr != nil {
			// A column that could not be read is recorded rather than ignored.
			// Ignoring it would let an unreadable column count as a clean one.
			report.Skipped = append(report.Skipped,
				fmt.Sprintf("%s.%s.%s: %v", c.schema, c.table, c.column, scanErr))
			continue
		}
		report.RowsSampled += int64(sampled)
		report.Findings = append(report.Findings, rows...)

		if opts.Progress != nil && len(rows) > 0 {
			opts.Progress(rows[0].String())
		}
	}
	report.Tables = len(seenTables)
	report.FinishedAt = opts.Now().UTC()

	sort.Slice(report.Findings, func(i, j int) bool {
		a, b := report.Findings[i], report.Findings[j]
		if a.Table != b.Table {
			return a.Table < b.Table
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		return a.Detector < b.Detector
	})
	return report, nil
}

type columnRef struct{ schema, table, column string }

// textColumns lists the columns worth reading.
//
// Only the ones that can hold a string. A bigint cannot hold an email address,
// and reading every numeric column of every table to prove it would multiply
// the scan for nothing.
func textColumns(ctx context.Context, conn *pgx.Conn) ([]columnRef, error) {
	const query = `
SELECT c.table_schema, c.table_name, c.column_name
FROM information_schema.columns c
JOIN information_schema.tables t
  ON t.table_schema = c.table_schema AND t.table_name = c.table_name
WHERE t.table_type = 'BASE TABLE'
  AND c.table_schema NOT IN ('pg_catalog', 'information_schema')
  AND c.data_type IN ('text', 'character varying', 'character', 'json', 'jsonb', 'xml')
ORDER BY c.table_schema, c.table_name, c.ordinal_position`

	rows, err := conn.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("verify: listing columns: %w", err)
	}
	defer rows.Close()

	var out []columnRef
	for rows.Next() {
		var c columnRef
		if err := rows.Scan(&c.schema, &c.table, &c.column); err != nil {
			return nil, fmt.Errorf("verify: listing columns: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// scanColumn reads a sample of one column and runs the detectors over it.
func scanColumn(ctx context.Context, conn *pgx.Conn, c columnRef, limit int) ([]Finding, int, error) {
	sql := fmt.Sprintf(
		`SELECT %s::text FROM %s.%s WHERE %s IS NOT NULL LIMIT %d`,
		quoteIdent(c.column), quoteIdent(c.schema), quoteIdent(c.table),
		quoteIdent(c.column), limit)

	rows, err := conn.Query(ctx, sql)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	counts := map[string]int{}
	examples := map[string]string{}
	sampled := 0
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, sampled, err
		}
		sampled++
		for _, d := range Detectors() {
			if d.Match(value) {
				counts[d.Name]++
				if examples[d.Name] == "" {
					examples[d.Name] = excerpt(value)
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, sampled, err
	}

	var out []Finding
	for name, n := range counts {
		out = append(out, Finding{
			Schema: c.schema, Table: c.table, Column: c.column,
			Detector: name, Example: examples[name], Rows: n,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Detector < out[j].Detector })
	return out, sampled, nil
}

// excerpt renders enough of a value to recognise its shape and not enough to
// be the value.
//
// A verification report that quoted the data would be a report that leaks it,
// and these reports are attached to pull requests.
func excerpt(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 4 {
		return strings.Repeat("*", len(s))
	}
	keep := 2
	if len(s) > 12 {
		keep = 3
	}
	return s[:keep] + strings.Repeat("*", min(len(s)-keep*2, 8)) + s[len(s)-keep:]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// Attestation is a signed statement that a golden was verified.
//
// Signed so that the claim can be checked by something that did not produce
// it: a CI job, a reviewer, a control plane. An unsigned report is a claim the
// thing making it can also forge, which is exactly the property that matters
// least when it comes from the same process that did the masking.
type Attestation struct {
	// Report is what was found.
	Report Report `json:"report"`
	// Golden identifies what was verified.
	Golden string `json:"golden"`
	// RulesHash identifies the masking configuration used, so a golden
	// verified under one set of rules is not mistaken for one verified under
	// another.
	RulesHash string `json:"rules_hash"`
	// Provenance identifies the project the golden was made for and the inputs
	// that produced it, so that a machine pulling a published golden can tell
	// whether it is looking at its own project's work or somebody else's.
	//
	// Signed along with everything else, which is the point of putting it
	// here rather than only in a provider annotation: a store is shared, and a
	// claim about whose data this is has to be one the reader can check.
	// Omitted when empty so that an attestation written before this field
	// existed still verifies against its own signature.
	Provenance string `json:"provenance,omitempty"`
	// PublicKey is the verifying key, base64.
	PublicKey string `json:"public_key"`
	// Signature covers the canonical form of everything above.
	Signature string `json:"signature"`
}

// Sign produces a signed attestation.
func Sign(report Report, golden, rulesHash, provenance string, key ed25519.PrivateKey) (Attestation, error) {
	a := Attestation{
		Report: report, Golden: golden, RulesHash: rulesHash, Provenance: provenance,
		PublicKey: base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)),
	}
	payload, err := a.payload()
	if err != nil {
		return a, err
	}
	a.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload))
	return a, nil
}

// Verify checks an attestation against its own public key.
//
// The caller still has to decide whether it trusts that key. This answers only
// whether the document was changed after it was signed, which is the question
// a reviewer looking at a pull request comment is actually asking.
func (a Attestation) Verify() bool {
	pub, err := base64.StdEncoding.DecodeString(a.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(a.Signature)
	if err != nil {
		return false
	}
	payload, err := a.payload()
	if err != nil {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(pub), payload, sig)
}

// payload is the canonical bytes that get signed.
//
// The signature field is excluded, and the encoding is Go's own sorted key
// JSON, so the same document produces the same bytes on any machine. A
// signature over a rendering that varied would verify on the machine that
// produced it and nowhere else.
func (a Attestation) payload() ([]byte, error) {
	a.Signature = ""
	body, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("verify: encoding the attestation: %w", err)
	}
	sum := sha256.Sum256(body)
	return []byte(hex.EncodeToString(sum[:])), nil
}

// GenerateKey returns a new signing key.
func GenerateKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}
