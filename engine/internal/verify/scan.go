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
	"unicode/utf8"

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
	if f.Detector == DetectorUnreadSensitive {
		// Not a value the scan read and disliked, a column it could not read
		// at all. Rows and Example would be a count of nothing and an
		// excerpt of nothing, so the sentence says what is actually known.
		return fmt.Sprintf("%s.%s.%s is not readable by the scanner (%s), has no masking "+
			"rule, and its name says it holds a secret",
			f.Schema, f.Table, f.Column, f.Example)
	}
	return fmt.Sprintf("%s.%s.%s holds %s (%d of the sampled rows, for example %s)",
		f.Schema, f.Table, f.Column, f.Detector, f.Rows, f.Example)
}

// DetectorUnreadSensitive is the finding raised for a column the scanner could
// not read, that no rule covers, and whose name says it holds a secret.
//
// It exists because the scan used to say clean about columns it never opened.
// verify read six text types and nothing else, so a bytea column called
// sp_private_key or ciphertext was invisible to it: not read, not skipped, not
// counted, and the report that came back said clean with zero findings while
// af mask plan, on the same database, listed the column as COPIED UNCHANGED.
// Two instruments, one database, opposite answers, and the one that said
// clean was the one that gated publication.
//
// A column the scanner cannot read is a column about which it knows nothing,
// and the strict reading of nothing is the one this package takes everywhere
// else. It cannot fail every unreadable column, because a preview environment
// with no enum columns is no preview environment. So it fails the narrow case
// where three facts line up: the scanner cannot read it, nobody wrote a rule
// for it, and the name is one the detectors would already treat as a secret.
const DetectorUnreadSensitive = "unread-sensitive-name"

// UnreadColumn is a column the scanner could not read as text.
//
// Listed rather than silently passed over, which is the whole point of the
// list. A column the scan never opened is not a column that passed, and until
// this existed nothing in the report said which columns those were. Distinct
// from Skipped, which is a column the scan tried to read and could not: this
// is a column the scan knows it cannot read from the type alone.
type UnreadColumn struct {
	Schema string `json:"schema"`
	Table  string `json:"table"`
	Column string `json:"column"`
	// Type is the Postgres type that made it unreadable.
	Type string `json:"type"`
	// Reason is one sentence for a person, in the form the CLI prints.
	Reason string `json:"reason"`
	// Ruled reports whether a masking rule covers the column. A column
	// nobody could read and nobody masked is copied as it was.
	Ruled bool `json:"ruled"`
}

// String renders the column the way the CLI prints it.
func (u UnreadColumn) String() string {
	return fmt.Sprintf("%s.%s.%s: %s", u.Schema, u.Table, u.Column, u.Reason)
}

// Report is the result of a scan.
type Report struct {
	// Scanner names what produced this, so an attestation can be read by
	// something that did not produce it.
	Scanner string `json:"scanner"`
	// Engine names the datastore this report is about.
	//
	// An environment holds more than one store, each with its own golden and
	// its own attestation, and two reports that do not say which store they
	// read are two reports nobody can tell apart. Omitted when empty so that
	// an attestation written before this field existed still verifies against
	// its own signature, the same reason Provenance is.
	Engine string `json:"engine,omitempty"`
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
	// Unread names the columns the scanner could not read as text, by type,
	// so that "clean" is a claim about the columns it opened and not about
	// the ones it never could. Recorded in the attestation for the same
	// reason SampleSize is: a reader deciding whether to trust the scan has
	// to be able to see what it did not cover.
	Unread []UnreadColumn `json:"unread,omitempty"`
	// Unruled names the columns masking copied unchanged because no rule
	// named them, as the caller reported them from the plan. The scan does
	// not know the rules; the caller does, and this is how the count travels
	// from the plan, where it was printed once at the bottom, into the
	// attestation, the golden listing and the JSON of every command that
	// reads back a golden.
	Unruled []string `json:"unruled,omitempty"`
}

// CoverageRecorded reports whether this report was made by a scanner that
// recorded what it could not read and what masking left alone.
//
// The first scanner recorded neither, and a listing that read its empty
// lists as zero would say "0 columns copied unchanged" about a golden made
// under rules that copied 145. Zero and unknown are different facts, and the
// scanner version is what tells them apart.
func (r Report) CoverageRecorded() bool {
	return r.Scanner != "" && r.Scanner != "antifailure/verify/1"
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
	// Unruled names the columns masking copied unchanged because no rule
	// covered them, as schema.table.column. It is what decides whether an
	// unreadable column with a secret's name is a finding or a note: with a
	// rule the column was rewritten, whatever the scanner can see of it, and
	// without one it holds exactly what production held.
	Unruled []string
}

// Scan reads back a Postgres database and reports what still looks real.
//
// The engine's own callers all hold a pgx connection, so this is the shape
// they keep. It is a thin wrapper over ScanSource, and going through the same
// generic path the second engine uses is deliberate: a boundary that the
// tested path routes around is a boundary that rots.
func Scan(ctx context.Context, conn *pgx.Conn, opts Options) (Report, error) {
	return ScanSource(ctx, PostgresSource(conn), opts)
}

// PostgresSource reads a Postgres through a pgx connection.
func PostgresSource(conn *pgx.Conn) Source {
	return NewSource(Postgres, func(ctx context.Context, sql string, yield func(Row) error) error {
		rows, err := conn.Query(ctx, sql)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			values, valErr := rows.Values()
			if valErr != nil {
				return valErr
			}
			row := make(Row, 0, len(values))
			for _, v := range values {
				row = append(row, asBytes(v))
			}
			if err := yield(row); err != nil {
				return err
			}
		}
		return rows.Err()
	})
}

// asBytes renders one scanned value as the bytes the detectors read.
//
// A text cast arrives as a string and a bytea as bytes, which is the whole
// distinction the scan makes. Anything else is rendered the way it prints,
// rather than dropped: a value nobody anticipated is still worth showing the
// detectors.
func asBytes(v any) []byte {
	switch value := v.(type) {
	case nil:
		return nil
	case []byte:
		return value
	case string:
		return []byte(value)
	default:
		return []byte(fmt.Sprint(value))
	}
}

// ScanSource reads back a datastore and reports what still looks real.
func ScanSource(ctx context.Context, src Source, opts Options) (Report, error) {
	if opts.SampleSize <= 0 {
		opts.SampleSize = DefaultSampleSize
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	report := Report{
		Scanner: "antifailure/verify/2", Engine: src.Engine(),
		StartedAt:  opts.Now().UTC(),
		SampleSize: opts.SampleSize,
	}

	columns, err := src.Columns(ctx)
	if err != nil {
		return report, err
	}
	unruled := map[string]bool{}
	for _, name := range opts.Unruled {
		unruled[name] = true
	}
	report.Unruled = append([]string(nil), opts.Unruled...)
	sort.Strings(report.Unruled)

	seenTables := map[string]bool{}
	for _, c := range columns {
		seenTables[c.Schema+"."+c.Table] = true
		ruled := !unruled[c.Schema+"."+c.Table+"."+c.Name]

		if c.kind == kindUnread {
			// Known from the type alone, before a row is read. Said in the
			// report rather than passed over, and turned into a finding when
			// the column is one that nothing masked and whose name says what
			// it holds.
			report.noteUnread(c, "not readable by the scanner: "+c.Type, ruled)
			continue
		}
		report.Columns++

		rows, sampled, opaque, scanErr := scanColumn(ctx, src, c, opts.SampleSize)
		if scanErr != nil {
			// A column that could not be read is recorded rather than ignored.
			// Ignoring it would let an unreadable column count as a clean one.
			report.Skipped = append(report.Skipped,
				fmt.Sprintf("%s.%s.%s: %v", c.Schema, c.Table, c.Name, scanErr))
			continue
		}
		report.RowsSampled += int64(sampled)
		report.Findings = append(report.Findings, rows...)
		if opaque > 0 {
			// A bytea column is read as UTF-8 where it decodes, and a value
			// that does not decode is binary the detectors cannot see into.
			// The column is then partly read at best, and the part that was
			// not read is said out loud, with the same consequence for a
			// secret's name as a type the scanner cannot read at all.
			report.noteUnread(c, fmt.Sprintf(
				"%d of %d sampled values are binary rather than text and could not be read",
				opaque, sampled), ruled)
		}

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

// columnKind is how the scanner reads a column, decided from its type.
type columnKind int

const (
	// kindText is read through a ::text cast and handed to the detectors.
	// Strings, JSON, XML, and everything whose text form is what a person
	// would see: arrays, enums, extension types, network addresses.
	kindText columnKind = iota
	// kindBytea is read as raw bytes and decoded as UTF-8 where it decodes.
	// A secret pasted into a bytea column is text in a binary coat, and the
	// coat is cheap to take off.
	kindBytea
	// kindUnread is a type the scanner has no way to read as text. It is
	// listed rather than passed over.
	kindUnread
)

// structuralTypes are the types whose text form cannot carry a sentence
// somebody typed: numbers, times, booleans, and identifiers the database
// generates. They are not read and not listed, because a listing of every
// bigint in the schema as "not readable" is a listing nobody reads, which is
// the same as no listing.
//
// The same list masking's classifier calls knownStructural, kept in step by a
// test in that package rather than by sharing the code, because the two
// packages are on opposite sides of a boundary this one is not allowed to
// cross: verify must not trust masking's opinion of anything.
var structuralTypes = map[string]bool{
	"smallint": true, "integer": true, "bigint": true, "decimal": true,
	"numeric": true, "real": true, "double precision": true, "money": true,
	"smallserial": true, "serial": true, "bigserial": true, "boolean": true,
	"uuid": true, "date": true, "time": true, "time without time zone": true,
	"time with time zone": true, "timestamp": true,
	"timestamp without time zone": true, "timestamp with time zone": true,
	"interval": true, "oid": true, "bit": true, "bit varying": true,
}

// KindOf classifies a Postgres type the way the scanner reads it: "text" for
// a type read through its text form, "bytea" for one decoded as UTF-8 where
// it decodes, "unread" for one the scanner cannot read, and "structural" for
// one it does not need to. Exported so the CLI's help and the tests can say
// which is which without a database.
func KindOf(dataType string) string {
	switch classify(dataType) {
	case kindText:
		return "text"
	case kindBytea:
		return "bytea"
	}
	if structuralTypes[strings.ToLower(dataType)] {
		return "structural"
	}
	return "unread"
}

func classify(dataType string) columnKind {
	switch strings.ToLower(dataType) {
	case "text", "character varying", "character", "json", "jsonb", "xml",
		"citext", "name",
		// information_schema reports every array as ARRAY and every enum,
		// domain and extension type as USER-DEFINED. Their text form is what
		// a person sees, and it is what the detectors run over.
		"array", "user-defined",
		// Network types locate somebody, and ::text renders them the way the
		// ip detector reads them.
		"inet", "cidr", "macaddr", "macaddr8",
		"tsvector", "tsquery":
		return kindText
	case "bytea":
		return kindBytea
	}
	return kindUnread
}

// sensitiveNameWords are the words in a table or column name that say the
// column holds something that grants access. The list is the one the built in
// masking rules and the credential detector already act on, written down once
// more here because this package must not import that one.
var sensitiveNameWords = []string{
	"secret", "key", "token", "private", "ciphertext", "password", "credential",
}

// SensitiveName reports whether a table or column name says the column holds
// a secret. Exported for the documentation test that keeps the CLI's own
// description of the rule honest.
func SensitiveName(table, column string) bool {
	lower := strings.ToLower(table + " " + column)
	for _, w := range sensitiveNameWords {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

// noteUnread records a column the scan could not read, and raises a finding
// when it is also unmasked and named like a secret.
func (r *Report) noteUnread(c Column, reason string, ruled bool) {
	r.Unread = append(r.Unread, UnreadColumn{
		Schema: c.Schema, Table: c.Table, Column: c.Name, Type: c.Type,
		Reason: reason, Ruled: ruled,
	})
	if ruled || !SensitiveName(c.Table, c.Name) {
		return
	}
	r.Findings = append(r.Findings, Finding{
		Schema: c.Schema, Table: c.Table, Column: c.Name,
		Detector: DetectorUnreadSensitive, Example: c.Type,
	})
}

// scanColumn reads a sample of one column and runs the detectors over it.
//
// It returns the findings, how many rows it sampled, and how many of those it
// could not turn into text, which is only ever non zero for a column read as
// bytes.
//
// The reading is here and the statement is in the dialect, which is the whole
// division: what counts as a leak is the same question in every store, and
// how to ask for the values is not.
func scanColumn(
	ctx context.Context, src Source, c Column, limit int,
) ([]Finding, int, int, error) {
	counts := map[string]int{}
	examples := map[string]string{}
	sampled, opaque := 0, 0
	err := src.Sample(ctx, c, limit, func(row Row) error {
		if len(row) == 0 {
			return nil
		}
		sampled++
		var value string
		if c.kind == kindBytea {
			decoded, ok := decodeText(row[0])
			if !ok {
				opaque++
				return nil
			}
			value = decoded
		} else {
			value = string(row[0])
		}
		for _, d := range Detectors() {
			if d.Match(value) {
				counts[d.Name]++
				if examples[d.Name] == "" {
					examples[d.Name] = excerpt(value)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, sampled, opaque, err
	}

	var out []Finding
	for name, n := range counts {
		out = append(out, Finding{
			Schema: c.Schema, Table: c.Table, Column: c.Name,
			Detector: name, Example: examples[name], Rows: n,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Detector < out[j].Detector })
	return out, sampled, opaque, nil
}

// decodeText reports whether raw bytes are text a detector can read.
//
// Valid UTF-8 with no control characters other than the whitespace a text
// file carries. A random salt or a ciphertext is valid UTF-8 with vanishing
// probability and full of control bytes when it is, so the test separates
// the two cleanly without pretending to know what the bytes mean.
func decodeText(raw []byte) (string, bool) {
	if len(raw) == 0 || !utf8.Valid(raw) {
		return "", false
	}
	for _, b := range raw {
		if b < 0x20 && b != '\t' && b != '\n' && b != '\r' {
			return "", false
		}
	}
	return string(raw), true
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

// ParseAttestation reads an attestation back out of its JSON, and reports
// whether it was one.
//
// For the commands that list goldens. The attestation is the durable record
// of what a version was verified as holding, and the counts it carries, of
// columns the scan could not read and of columns copied unchanged, are the
// counts af golden list and inspect_goldens print beside "verified". Reading
// them from the attestation rather than recomputing them is what makes the
// listing describe the golden that exists rather than the rules file that
// exists now.
func ParseAttestation(raw string) (Attestation, bool) {
	var a Attestation
	if raw == "" || json.Unmarshal([]byte(raw), &a) != nil {
		return Attestation{}, false
	}
	if a.Report.Scanner == "" {
		return Attestation{}, false
	}
	return a, true
}

// GenerateKey returns a new signing key.
func GenerateKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}
