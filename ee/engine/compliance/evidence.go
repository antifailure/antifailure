package compliance

// What a pack reads, and how it is verified.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Evidence is a value rather than an interface on purpose. Every control's
// logic is then a pure function over data, which means every control can be
// tested without a database and the same evidence always produces the same
// report. Reading it out of a real control plane is a separate concern, in
// postgres.go, where it can be tested against a real Postgres.
//
// The two verifications in this file are the ones the packs cannot do
// themselves and must not take on trust.
//
// The audit chain, which has to be recomputed here rather than believed. The
// entries carry their own hashes, and an attacker who can write the table can
// write those too; what they cannot easily do is recompute every entry since.
// Recomputing is the check. Reading the stored hash and comparing it to itself
// would be a check that passes on a rewritten log, which is the only log it
// matters on.
//
// The masking attestations, which are signed. An attestation is a statement
// that a golden was scanned and found clean, and a compliance report that
// repeated that statement without checking the signature would launder an
// altered document into evidence.

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Evidence is everything the packs read.
type Evidence struct {
	// Org is the organization the evidence is about.
	Org string
	// From and To bound the period.
	From, To time.Time
	// GeneratedAt is when the evidence was gathered.
	GeneratedAt time.Time

	Audit        AuditEvidence
	Attestations []Attestation
	Goldens      GoldenEvidence
	Egress       EgressEvidence
	Policy       PolicyEvidence
	Access       AccessEvidence
	Environments EnvironmentEvidence
	Posture      Posture

	// Unread names checks whose evidence could not be gathered, with the
	// reason. Carried rather than dropped so a control reported as "not
	// evidenced" because a query failed is not mistaken for one where there was
	// genuinely nothing to show.
	Unread map[Check]string
	// Incomplete is the same information as a list, for the report header, so
	// that a reader sees at the top that the document is partial rather than
	// discovering it control by control.
	Incomplete []string
}

// AuditEvidence is the audit log over the period.
type AuditEvidence struct {
	Entries           int
	FirstSeq, LastSeq int64
	Head              string
	ByAction          map[string]int
	Breaks            []ChainBreak
}

// ChainBreak is one place the hash chain does not hold.
type ChainBreak struct {
	Seq    int64
	Kind   string
	Detail string
}

// GoldenEvidence is what was published.
type GoldenEvidence struct {
	Total         int
	Unverified    int
	UnverifiedIDs []string
}

// EgressEvidence is what environments were allowed to reach.
type EgressEvidence struct {
	Decisions     int
	Blocked       int
	BlockedByHost map[string]int
}

// PolicyEvidence is organization policy and what it refused.
type PolicyEvidence struct {
	Configured bool
	Refusals   int
	Rules      []string
}

// AccessEvidence is membership changes.
type AccessEvidence struct {
	Removals int
	// RemovalsWithoutSessionRevoke is the number that were not followed by a
	// session revocation, which is the specific failure every framework asks
	// about and almost nobody checks: a removed member whose session keeps
	// working until it expires.
	RemovalsWithoutSessionRevoke int
}

// EnvironmentEvidence is what was created and whether it was cleaned up.
type EnvironmentEvidence struct {
	Created   int
	Destroyed int
	Leaked    int
	LeakedIDs []string
}

// Posture is the configuration the checks read rather than the events.
type Posture struct {
	// AppRole is the role the application connects as.
	AppRole string
	// AppRoleBypassesRLS reports whether that role holds BYPASSRLS, which would
	// make every policy decorative.
	AppRoleBypassesRLS bool
	// AuditGrants are the privileges the application role holds on the audit
	// log. Only INSERT and SELECT should be there.
	AuditGrants []string
	// TenantTables are the tables holding tenant data.
	TenantTables []string
	// TablesWithoutRLS are those among them with row level security disabled.
	TablesWithoutRLS []string
	// AuditRetentionDays is how long entries are kept. Zero means for ever.
	AuditRetentionDays int
}

// note records that a check's evidence could not be read.
func (e *Evidence) note(check Check, reason string) {
	if e.Unread == nil {
		e.Unread = map[Check]string{}
	}
	e.Unread[check] = reason
	e.Incomplete = append(e.Incomplete, string(check)+": "+reason)
}

// ---------------------------------------------------------------------------
// The audit chain
// ---------------------------------------------------------------------------

// AuditEntry is one row of the audit log, as the chain sees it.
//
// The field names and their order are load bearing. The canonical form the
// control plane hashes is these eleven fields in this order, each prefixed with
// its own byte length, and a reordering here produces a hash that disagrees
// with every entry ever written. The length prefixes exist because without them
// an actor named "a" doing "b.c" hashes identically to one named "ab" doing
// ".c", and an attacker who chooses one field could then choose the hash.
type AuditEntry struct {
	Seq         int64
	OrgID       string
	ActorUserID string
	ActorLabel  string
	Action      string
	TargetType  string
	TargetID    string
	Origin      string
	// Detail is the raw JSON as the database returned it, canonicalised when
	// hashed rather than re-encoded by a Go marshaller, because two encoders
	// that disagree about key order disagree about the hash.
	Detail     json.RawMessage
	OccurredAt time.Time
	PrevHash   string
	EntryHash  string
}

// VerifyChain recomputes every entry's hash and reports every break.
//
// Every break rather than the first, because an investigation wants the extent
// of the tampering and the first one tells you only where it started.
//
// The anchor is the entry immediately before the window, or nil when the window
// starts at the beginning of the log. It exists because the first entry inside
// any period carries the hash of the entry before it, which is outside the
// period: verifying a window without one reports a broken link at the first row
// of every report ever produced. It is used for that link and is not counted,
// because the report describes the period it claims to.
//
// Entries must be in sequence order.
func VerifyChain(anchor *AuditEntry, entries []AuditEntry) AuditEvidence {
	out := AuditEvidence{ByAction: map[string]int{}}
	if len(entries) == 0 {
		return out
	}
	out.Entries = len(entries)
	out.FirstSeq = entries[0].Seq
	out.LastSeq = entries[len(entries)-1].Seq
	out.Head = entries[len(entries)-1].EntryHash

	previous := anchor
	for i := range entries {
		entry := entries[i]
		out.ByAction[entry.Action]++

		computed := entry.hash()
		if computed != entry.EntryHash {
			out.Breaks = append(out.Breaks, ChainBreak{
				Seq: entry.Seq, Kind: "altered",
				Detail: "the stored hash does not match the entry's contents, " +
					"so the entry was changed after it was written",
			})
		}
		if previous != nil {
			if entry.PrevHash != previous.EntryHash {
				out.Breaks = append(out.Breaks, ChainBreak{
					Seq: entry.Seq, Kind: "broken_link",
					Detail: "this entry does not point at the one before it, " +
						"so an entry between them was removed or replaced",
				})
			}
			if gap := entry.Seq - previous.Seq; gap > 1 {
				// A gap is not proof of tampering: the sequence is taken from a
				// database sequence and a rolled back transaction consumes a
				// number without writing a row. It is worth reporting because
				// it is also what a deletion looks like, and only somebody who
				// knows what the installation was doing can tell them apart.
				out.Breaks = append(out.Breaks, ChainBreak{
					Seq: entry.Seq, Kind: "missing",
					Detail: fmt.Sprintf(
						"%d sequence numbers between %d and %d have no row; a rolled back "+
							"transaction consumes a number without writing one, and so does a "+
							"deletion, and this check cannot tell them apart",
						gap-1, previous.Seq, entry.Seq),
				})
			}
		}
		previous = &entries[i]
	}
	return out
}

// hash recomputes the entry's canonical hash.
//
// This has to agree byte for byte with the control plane's own implementation
// in web/packages/db/src/audit.ts, and there is a test that runs a chain
// written by that implementation, against a real Postgres, through this one. A
// verifier that only agrees with itself would report a clean log as tampered or
// a tampered log as clean, and there is no way to tell which from the code.
func (a AuditEntry) hash() string {
	parts := []string{
		strconv.FormatInt(a.Seq, 10),
		a.OrgID,
		a.ActorUserID,
		a.ActorLabel,
		a.Action,
		a.TargetType,
		a.TargetID,
		a.Origin,
		canonicalJSON(a.Detail),
		// Exactly three fractional digits and a Z, which is what
		// JavaScript's toISOString produces and therefore what was hashed.
		// Go's RFC3339Nano would drop trailing zeroes and produce a different
		// string for the same instant, which is a hash that disagrees for half
		// the entries and agrees for the rest.
		a.OccurredAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		a.PrevHash,
	}
	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte(strconv.Itoa(len(part))))
		h.Write([]byte(":"))
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// canonicalJSON renders a document with object keys sorted.
//
// Sorted, so two encoders that disagree about key order do not disagree about
// the hash. Numbers and strings are rendered the way JSON.stringify renders
// them, because that is what produced the hashes being checked; this is not a
// place to have a better opinion than the thing already in the database.
func canonicalJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "null"
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "null"
	}
	var b strings.Builder
	writeCanonical(&b, value)
	return b.String()
}

func writeCanonical(b *strings.Builder, value any) {
	switch v := value.(type) {
	case nil:
		b.WriteString("null")
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			encoded, _ := json.Marshal(k)
			b.Write(encoded)
			b.WriteByte(':')
			writeCanonical(b, v[k])
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			writeCanonical(b, item)
		}
		b.WriteByte(']')
	case string:
		encoded, _ := json.Marshal(v)
		b.Write(encoded)
	case float64:
		// The shortest representation that round trips, which is what
		// JSON.stringify produces: 1 rather than 1.0, and no exponent until
		// JavaScript itself would use one.
		b.WriteString(formatJSNumber(v))
	case bool:
		if v {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	default:
		encoded, _ := json.Marshal(v)
		b.Write(encoded)
	}
}

// formatJSNumber renders a number the way JavaScript does.
func formatJSNumber(f float64) string {
	if f == float64(int64(f)) && f < 1e21 && f > -1e21 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// ---------------------------------------------------------------------------
// Masking attestations
// ---------------------------------------------------------------------------

// Attestation is a signed statement that a golden was scanned.
//
// Decoded here rather than imported, because the engine's own type is in
// engine/internal/verify and a separate module cannot reach it. The wire form
// is the contract: it is JSON, it is stored as JSON, and it is what a pull
// request comment already shows. Decoding it independently is also the honest
// thing for a verifier to do, since a verifier that shares a struct with the
// signer cannot detect a change in the signer.
type Attestation struct {
	Report    AttestationReport `json:"report"`
	Golden    string            `json:"golden"`
	RulesHash string            `json:"rules_hash"`
	PublicKey string            `json:"public_key"`
	Signature string            `json:"signature"`

	// SignatureValid is the result of checking the signature against the key
	// the document itself carries. Whether that key is trusted is a separate
	// question and not one this answers; what it answers is whether the
	// document was changed after it was signed, which is the question a report
	// repeating its contents has to ask first.
	SignatureValid bool `json:"-"`
	// Clean reports whether the scan found nothing.
	Clean bool `json:"-"`
	// Unverifiable records why a signature could not be checked at all, as
	// distinct from one that was checked and did not verify. A malformed key
	// and a forged document are different problems.
	Unverifiable string `json:"-"`
}

// AttestationReport is what the scan found.
type AttestationReport struct {
	Scanner     string               `json:"scanner"`
	StartedAt   time.Time            `json:"started_at"`
	FinishedAt  time.Time            `json:"finished_at"`
	Tables      int                  `json:"tables"`
	Columns     int                  `json:"columns"`
	RowsSampled int64                `json:"rows_sampled"`
	SampleSize  int                  `json:"sample_size"`
	Findings    []AttestationFinding `json:"findings"`
	Skipped     []string             `json:"skipped,omitempty"`
}

// AttestationFinding is one column the scan found real data in.
type AttestationFinding struct {
	Schema   string `json:"schema"`
	Table    string `json:"table"`
	Column   string `json:"column"`
	Detector string `json:"detector"`
	Example  string `json:"example"`
	Rows     int    `json:"rows"`
}

// ParseAttestation decodes and verifies one attestation.
//
// It returns the attestation even when the signature does not verify, because a
// report has to be able to say "this one did not verify" and naming which one
// needs the contents.
func ParseAttestation(raw []byte) (Attestation, error) {
	var a Attestation
	if err := json.Unmarshal(raw, &a); err != nil {
		return Attestation{}, fmt.Errorf("the attestation is not JSON")
	}
	a.Clean = len(a.Report.Findings) == 0
	a.SignatureValid, a.Unverifiable = a.verify()
	return a, nil
}

// verify checks the signature over the payload the engine signs.
//
// The payload is the hex SHA-256 of the document with the signature field
// emptied, and "the document" is the engine's own struct marshalled by
// encoding/json. That means field ORDER is part of the signature, so the type
// above mirrors engine/internal/verify.Attestation field for field and tag for
// tag, and reordering it silently breaks every verification.
//
// That is a fragile coupling and pretending otherwise would be worse than
// having it. Two things make it safe rather than hopeful. The types cannot be
// shared, because a separate module cannot import engine/internal. And there is
// a test that takes an attestation produced by the engine's own signer and runs
// it through this, so a change to either side is caught by a failing test
// rather than by a compliance report quietly reporting every attestation as
// forged.
func (a Attestation) verify() (bool, string) {
	pub, err := base64.StdEncoding.DecodeString(a.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return false, "the attestation carries no usable public key"
	}
	signature, err := base64.StdEncoding.DecodeString(a.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return false, "the attestation carries no usable signature"
	}

	unsigned := a
	unsigned.Signature = ""
	body, err := json.Marshal(struct {
		Report    AttestationReport `json:"report"`
		Golden    string            `json:"golden"`
		RulesHash string            `json:"rules_hash"`
		PublicKey string            `json:"public_key"`
		Signature string            `json:"signature"`
	}{
		Report: unsigned.Report, Golden: unsigned.Golden, RulesHash: unsigned.RulesHash,
		PublicKey: unsigned.PublicKey, Signature: "",
	})
	if err != nil {
		return false, "the attestation could not be re-encoded"
	}
	sum := sha256.Sum256(body)
	payload := []byte(hex.EncodeToString(sum[:]))
	if !ed25519.Verify(ed25519.PublicKey(pub), payload, signature) {
		return false, ""
	}
	return true, ""
}

// Summary is the one line a report shows for an attestation.
func (a Attestation) Summary() string {
	verdict := "clean"
	if !a.Clean {
		verdict = fmt.Sprintf("%d finding(s)", len(a.Report.Findings))
	}
	if !a.SignatureValid {
		verdict += ", SIGNATURE DOES NOT VERIFY"
	}
	return fmt.Sprintf("golden %s: %s, %d columns over %d tables, %d rows sampled, rules %s",
		a.Golden, verdict, a.Report.Columns, a.Report.Tables,
		a.Report.RowsSampled, shortHash(a.RulesHash))
}
