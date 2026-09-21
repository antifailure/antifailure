package sqlload

import (
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// Generating a value for a parameter whose value was normalised away.
//
// The list below is short on purpose. Every type in it is one where a value
// drawn at random is a legal value of that type and means nothing more; every
// type NOT in it is refused by name, so the report says "no value of type
// jsonb can be generated" rather than binding an empty object and letting
// somebody read the result as a measurement of their document workload.
//
// The ranges are stated here and in the documentation because they are the
// whole limitation of the derived path. An integer parameter is drawn from one
// to a million, so a predicate on a primary key will usually match a row in a
// table of a million and usually match nothing in a table of a hundred. That
// is why every derived run reports the rows its statements actually returned:
// a reader who sees zero rows across the whole run knows the values matched
// nothing and that the latencies are the cost of finding that out, which is a
// real measurement of the index and not a measurement of the result set.
//
// The declared path exists for when that is not good enough. A query parameter
// there draws its values from the table itself, so the ids are ids that exist.

// The bounds a generated value is drawn from.
const (
	generatedIntMin   = 1
	generatedIntMax   = 1_000_000
	generatedSmallMax = 32_000
	generatedTextLen  = 12
)

// generatedEpoch is the instant a generated date or timestamp is offset from.
// A constant rather than the current time, so the same seed produces the same
// timestamp on every machine on every day, which is what reproducibility
// means.
var generatedEpoch = time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)

// generatedWindow is how far after the epoch a generated timestamp can fall.
const generatedWindow = 5 * 365 * 24 * time.Hour

// canGenerate reports whether a value of this Postgres type can be produced.
//
// Asked before a statement is taken into the mix rather than at execution
// time. A statement discovered to be unusable halfway through a run is a run
// whose measurements are of a mix nobody declared.
func canGenerate(pgType string) bool {
	_, ok := generators[baseType(pgType)]
	return ok
}

// generate produces one value of a type.
func generate(pgType string, rng *rand.Rand) (any, error) {
	g, ok := generators[baseType(pgType)]
	if !ok {
		return nil, fmt.Errorf("no value of type %s can be generated", pgType)
	}
	return g(rng), nil
}

// baseType strips the modifier a type name can carry, so that character
// varying(255) and character varying are one type.
func baseType(pgType string) string {
	t := strings.ToLower(strings.TrimSpace(pgType))
	if i := strings.IndexByte(t, '('); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	return t
}

// generators is the closed set.
//
// The keys are the names REGTYPE PRINTS, and only those. That is what
// pg_prepared_statements.parameter_types is cast to, and it always answers with
// the canonical spelling: "integer" and never "int4", "character varying" and
// never "varchar", "timestamp with time zone" and never "timestamptz". This map
// carried both spellings of nine types for a while, which looked like
// defensiveness and was dead weight: no server has ever produced the second
// spelling, so nine of the twenty four entries could never be reached and the
// published list they were compared against could never match.
var generators = map[string]func(*rand.Rand) any{
	"smallint": func(r *rand.Rand) any { return int16(generatedIntMin + r.Int63n(generatedSmallMax)) },
	"integer":  func(r *rand.Rand) any { return int32(generatedIntMin + r.Int63n(generatedIntMax)) },
	"bigint":   func(r *rand.Rand) any { return generatedIntMin + r.Int63n(generatedIntMax) },

	"numeric":          func(r *rand.Rand) any { return roundedFloat(r) },
	"real":             func(r *rand.Rand) any { return float32(roundedFloat(r)) },
	"double precision": func(r *rand.Rand) any { return roundedFloat(r) },

	"text":              func(r *rand.Rand) any { return word(r) },
	"character varying": func(r *rand.Rand) any { return word(r) },
	"name":              func(r *rand.Rand) any { return word(r) },

	"boolean": func(r *rand.Rand) any { return r.Intn(2) == 0 },

	"uuid": func(r *rand.Rand) any { return randomUUID(r) },

	"date":                        func(r *rand.Rand) any { return randomTime(r).Truncate(24 * time.Hour) },
	"timestamp without time zone": func(r *rand.Rand) any { return randomTime(r) },
	"timestamp with time zone":    func(r *rand.Rand) any { return randomTime(r) },
}

func roundedFloat(r *rand.Rand) float64 {
	// Two decimal places, because an amount column is the commonest numeric
	// and a value with fifteen of them is one no application would send.
	return float64(r.Int63n(100_000_00)) / 100
}

// word builds a short lowercase string.
//
// Letters only. A generated value reaches a LIKE pattern, a regular expression
// and a full text query in somebody's workload, and a value carrying a percent
// sign or a backslash would measure the escaping rather than the query.
func word(r *rand.Rand) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, generatedTextLen)
	for i := range b {
		b[i] = alphabet[r.Intn(len(alphabet))]
	}
	return string(b)
}

func randomTime(r *rand.Rand) time.Time {
	return generatedEpoch.Add(time.Duration(r.Int63n(int64(generatedWindow)))).UTC()
}

// randomUUID builds a version 4 uuid from the run's own generator.
//
// From the seeded generator rather than from crypto/rand, because the whole
// promise of the seed is that two runs execute the same sequence, and a
// cryptographically random id would break it for the one type most likely to
// be a primary key.
func randomUUID(r *rand.Rand) string {
	var b [16]byte
	for i := range b {
		b[i] = byte(r.Intn(256))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
