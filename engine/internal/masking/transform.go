package masking

import (
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Transform rewrites one value.
//
// The contract every transform holds to:
//
//   - It is a pure function of the subkey, the column identity, and the input.
//     Two runs, two tables, and two refreshes all produce the same output for
//     the same input, which is what keeps foreign keys joinable.
//   - It preserves null. A null column stays null, because a not null
//     constraint that was satisfied must stay satisfied and an optional field
//     that was empty carries information the application may rely on.
//   - It preserves shape closely enough that the value still satisfies the
//     constraints the original did: length, format, and uniqueness.
type Transform interface {
	// Name is what the manifest calls it.
	Name() string
	// Apply rewrites a value. A nil input is null and must return nil.
	Apply(k *Key, c Column, in *string) (*string, error)
	// Describe is one sentence for the generated transform reference and for
	// af mask plan, so that a rule a reviewer does not recognise can be
	// understood without reading the code.
	Describe() string
	// PreservesUniqueness reports whether distinct inputs are guaranteed
	// distinct outputs. The planner refuses to put a transform without it on a
	// column carrying a unique constraint, which is a failure that would
	// otherwise surface as a constraint violation halfway through a masking
	// run over a large table.
	PreservesUniqueness() bool
}

// Registry holds the transforms a rules file may name.
var Registry = map[string]Transform{}

func register(t Transform) {
	if _, dup := Registry[t.Name()]; dup {
		panic("masking: two transforms are registered as " + t.Name())
	}
	Registry[t.Name()] = t
}

// Names returns every registered transform name, sorted. The reference
// generator and the rules validator both use it.
func Names() []string {
	out := make([]string, 0, len(Registry))
	for n := range Registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Lookup returns a transform by name.
func Lookup(name string) (Transform, bool) {
	t, ok := Registry[name]
	return t, ok
}

func init() {
	register(emailTransform{})
	register(nameTransform{})
	register(firstNameTransform{})
	register(lastNameTransform{})
	register(phoneTransform{})
	register(addressTransform{})
	register(cityTransform{})
	register(regionTransform{})
	register(postcodeTransform{})
	register(companyTransform{})
	register(uuidRemapTransform{})
	register(intFPETransform{})
	register(stringFPETransform{})
	register(dateShiftTransform{})
	register(numericNoiseTransform{})
	register(nullifyTransform{})
	register(hashHexTransform{})
	register(freeTextTransform{})
	register(usernameTransform{})
	register(ipTransform{})
	register(urlTransform{})
	register(creditCardTransform{})
	register(preserveTransform{})
}

// pass returns the input unchanged, and is the shared null handling every
// transform starts with.
func nullOK(in *string) bool { return in == nil }

func str(s string) *string { return &s }

// emailTransform replaces an address while keeping it unique and routable to
// nowhere.
//
// Uniqueness is by construction rather than by luck: the local part is a
// 64 bit keyed hash of the input rendered in base32, so two distinct inputs
// collide only if the hash does. That matters because an email column almost
// always carries a unique index, and a transform that collides fails the
// masking run partway through a table rather than at planning time.
type emailTransform struct{}

func (emailTransform) Name() string { return "email" }

func (emailTransform) Describe() string {
	return "Replaces an address with a unique synthetic one at example.test, which is reserved and can never receive mail."
}

func (emailTransform) PreservesUniqueness() bool { return true }

func (emailTransform) Apply(k *Key, c Column, in *string) (*string, error) {
	if nullOK(in) {
		return nil, nil
	}
	if *in == "" {
		// An empty string is not an address, and turning it into one would
		// create a value the application never had.
		return in, nil
	}
	sub := k.Sub(c)
	// The local part is derived from the address lowercased, so that two rows
	// differing only in case map to the same masked address, which is how the
	// real system treats them.
	h := prf(sub, strings.ToLower(strings.TrimSpace(*in)))
	local := base32Encode(h, 13)

	// A plus tag in the original is kept as a distinct tag rather than
	// dropped, because plus addressing is often load bearing in a sign up
	// flow and losing it changes what the application does.
	if plus := strings.IndexByte(*in, '+'); plus >= 0 && plus < strings.LastIndexByte(*in, '@') {
		tag := base32Encode(prf(sub, "tag:"+*in), 4)
		local += "+" + tag
	}
	return str(local + "@" + syntheticDomain), nil
}

func base32Encode(v uint64, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte(base32Alphabet[v&31])
		v >>= 5
		if v == 0 && i < n-1 {
			// Refill from a rotation so that short values still fill the
			// width, keeping every masked local part the same length.
			v = (uint64(i) + 1) * 0x9E3779B97F4A7C15
		}
	}
	return b.String()
}

// nameTransform replaces a full name.
type nameTransform struct{}

func (nameTransform) Name() string { return "name" }

func (nameTransform) Describe() string {
	return "Replaces a person's name with a synthetic one of a similar shape, keeping the number of parts."
}

func (nameTransform) PreservesUniqueness() bool { return false }

func (nameTransform) Apply(k *Key, c Column, in *string) (*string, error) {
	if nullOK(in) {
		return nil, nil
	}
	if strings.TrimSpace(*in) == "" {
		return in, nil
	}
	s := newPRFStream(k.Sub(c), *in)
	// The number of parts is preserved, because a form that renders a first
	// and last name separately behaves differently for a one word name.
	parts := len(strings.Fields(*in))
	switch {
	case parts <= 1:
		return str(givenNames[s.intn(len(givenNames))]), nil
	case parts == 2:
		return str(givenNames[s.intn(len(givenNames))] + " " + familyNames[s.intn(len(familyNames))]), nil
	default:
		middle := string(rune('A' + s.intn(26)))
		return str(givenNames[s.intn(len(givenNames))] + " " + middle + ". " +
			familyNames[s.intn(len(familyNames))]), nil
	}
}

type firstNameTransform struct{}

func (firstNameTransform) Name() string { return "first_name" }
func (firstNameTransform) Describe() string {
	return "Replaces a given name with a synthetic one."
}
func (firstNameTransform) PreservesUniqueness() bool { return false }
func (firstNameTransform) Apply(k *Key, c Column, in *string) (*string, error) {
	if nullOK(in) {
		return nil, nil
	}
	if strings.TrimSpace(*in) == "" {
		return in, nil
	}
	s := newPRFStream(k.Sub(c), *in)
	return str(givenNames[s.intn(len(givenNames))]), nil
}

type lastNameTransform struct{}

func (lastNameTransform) Name() string { return "last_name" }
func (lastNameTransform) Describe() string {
	return "Replaces a family name with a synthetic one."
}
func (lastNameTransform) PreservesUniqueness() bool { return false }
func (lastNameTransform) Apply(k *Key, c Column, in *string) (*string, error) {
	if nullOK(in) {
		return nil, nil
	}
	if strings.TrimSpace(*in) == "" {
		return in, nil
	}
	s := newPRFStream(k.Sub(c), *in)
	return str(familyNames[s.intn(len(familyNames))]), nil
}

// phoneTransform replaces a number while keeping its shape.
//
// Shape preservation is the whole difficulty. A column may hold E.164, a
// national format with spaces and dashes, or a bare run of digits, and a check
// constraint or a client side validator often depends on which. Rewriting only
// the digits, in place, keeps every separator and the length exactly as they
// were.
type phoneTransform struct{}

func (phoneTransform) Name() string { return "phone" }

func (phoneTransform) Describe() string {
	return "Replaces the digits of a phone number in place, keeping its length, punctuation, and country prefix so that format checks still pass."
}

func (phoneTransform) PreservesUniqueness() bool { return false }

func (phoneTransform) Apply(k *Key, c Column, in *string) (*string, error) {
	if nullOK(in) {
		return nil, nil
	}
	if *in == "" {
		return in, nil
	}
	s := newPRFStream(k.Sub(c), *in)
	runes := []rune(*in)
	digitsSeen := 0
	for i, r := range runes {
		if !unicode.IsDigit(r) {
			continue
		}
		digitsSeen++
		// The first three digits after a plus are the country calling code.
		// Changing them turns a valid number into one no validator accepts, so
		// they are kept.
		if runes[0] == '+' && digitsSeen <= 2 {
			continue
		}
		// A leading zero in a national format is significant, so it stays.
		if i == 0 && r == '0' {
			continue
		}
		runes[i] = rune('0' + s.intn(10))
	}
	return str(string(runes)), nil
}

// addressTransform replaces a street address.
type addressTransform struct{}

func (addressTransform) Name() string { return "address" }

func (addressTransform) Describe() string {
	return "Replaces a street address with a synthetic one of a similar shape."
}

func (addressTransform) PreservesUniqueness() bool { return false }

func (addressTransform) Apply(k *Key, c Column, in *string) (*string, error) {
	if nullOK(in) {
		return nil, nil
	}
	if strings.TrimSpace(*in) == "" {
		return in, nil
	}
	s := newPRFStream(k.Sub(c), *in)
	return str(fmt.Sprintf("%d %s %s",
		1+s.intn(999),
		streetNames[s.intn(len(streetNames))],
		streetTypes[s.intn(len(streetTypes))])), nil
}

type cityTransform struct{}

func (cityTransform) Name() string { return "city" }
func (cityTransform) Describe() string {
	return "Replaces a city with a synthetic one."
}
func (cityTransform) PreservesUniqueness() bool { return false }
func (cityTransform) Apply(k *Key, c Column, in *string) (*string, error) {
	if nullOK(in) {
		return nil, nil
	}
	if strings.TrimSpace(*in) == "" {
		return in, nil
	}
	s := newPRFStream(k.Sub(c), *in)
	return str(cityNames[s.intn(len(cityNames))]), nil
}

// regionTransform replaces a subdivision code.
//
// Two letters out, whatever went in, because that is the shape an address form
// validates and the shape the lexicon holds. A column that stores the full
// name of a state is a different column and wants a different rule; the
// description says so, so nobody finds out from the data.
type regionTransform struct{}

func (regionTransform) Name() string { return "region" }
func (regionTransform) Describe() string {
	return "Replaces a state or subdivision code with a synthetic two letter one. " +
		"For a column holding the full name of a region, use city or nullify instead."
}
func (regionTransform) PreservesUniqueness() bool { return false }
func (regionTransform) Apply(k *Key, c Column, in *string) (*string, error) {
	if nullOK(in) {
		return nil, nil
	}
	if strings.TrimSpace(*in) == "" {
		return in, nil
	}
	s := newPRFStream(k.Sub(c), *in)
	return str(regionCodes[s.intn(len(regionCodes))]), nil
}

// postcodeTransform rewrites a postal code in place, keeping letters as
// letters and digits as digits so that the format still validates in every
// country's shape.
type postcodeTransform struct{}

func (postcodeTransform) Name() string { return "postcode" }

func (postcodeTransform) Describe() string {
	return "Rewrites a postal code in place, keeping letters as letters and digits as digits so the country's format still validates."
}

func (postcodeTransform) PreservesUniqueness() bool { return false }

func (postcodeTransform) Apply(k *Key, c Column, in *string) (*string, error) {
	if nullOK(in) {
		return nil, nil
	}
	if *in == "" {
		return in, nil
	}
	s := newPRFStream(k.Sub(c), *in)
	runes := []rune(*in)
	for i, r := range runes {
		switch {
		case unicode.IsDigit(r):
			runes[i] = rune('0' + s.intn(10))
		case r >= 'A' && r <= 'Z':
			runes[i] = rune('A' + s.intn(26))
		case r >= 'a' && r <= 'z':
			runes[i] = rune('a' + s.intn(26))
		}
	}
	return str(string(runes)), nil
}

type companyTransform struct{}

func (companyTransform) Name() string { return "company" }
func (companyTransform) Describe() string {
	return "Replaces a company name with a synthetic one that reads as a company."
}
func (companyTransform) PreservesUniqueness() bool { return false }
func (companyTransform) Apply(k *Key, c Column, in *string) (*string, error) {
	if nullOK(in) {
		return nil, nil
	}
	if strings.TrimSpace(*in) == "" {
		return in, nil
	}
	s := newPRFStream(k.Sub(c), *in)
	return str(companyPrefixes[s.intn(len(companyPrefixes))] + " " +
		companySuffixes[s.intn(len(companySuffixes))]), nil
}

// uuidRemapTransform maps a UUID to another valid UUID, deterministically.
//
// This is the transform that keeps a schema joinable. A UUID primary key and
// every foreign key pointing at it are given the same Link, so they map
// identically and the relationship survives. Without it, masking a key column
// breaks every join in the database.
type uuidRemapTransform struct{}

func (uuidRemapTransform) Name() string { return "uuid_remap" }

func (uuidRemapTransform) Describe() string {
	return "Maps a UUID to a different valid UUID. Columns that share a link map identically, so foreign keys still join."
}

func (uuidRemapTransform) PreservesUniqueness() bool { return true }

func (uuidRemapTransform) Apply(k *Key, c Column, in *string) (*string, error) {
	if nullOK(in) {
		return nil, nil
	}
	s := strings.TrimSpace(*in)
	if s == "" {
		return in, nil
	}
	if !looksLikeUUID(s) {
		return nil, fmt.Errorf(
			"masking: uuid_remap on %s received a value that is not a UUID; use hash_hex or string_fpe for that column",
			c)
	}
	b := prfBytes(k.Sub(c), strings.ToLower(s), 16)
	// Version 4 and the RFC 4122 variant, so that a library which validates
	// the version still accepts the result.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	out := h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
	// The original's case is preserved so that a column comparing text rather
	// than the uuid type behaves the same.
	if s == strings.ToUpper(s) && strings.ContainsAny(s, "ABCDEF") {
		out = strings.ToUpper(out)
	}
	return str(out), nil
}

func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}

// intFPETransform replaces an integer with another of the same digit count.
//
// Preserving the digit count is what keeps a check constraint on magnitude
// satisfied, and what keeps a column that a user interface right aligns from
// suddenly rendering at a different width.
type intFPETransform struct{}

func (intFPETransform) Name() string { return "int_fpe" }

func (intFPETransform) Describe() string {
	return "Replaces an integer with a different one of the same digit count and sign, so range checks and column widths still hold."
}

func (intFPETransform) PreservesUniqueness() bool { return false }

func (intFPETransform) Apply(k *Key, c Column, in *string) (*string, error) {
	if nullOK(in) {
		return nil, nil
	}
	s := strings.TrimSpace(*in)
	if s == "" {
		return in, nil
	}
	neg := strings.HasPrefix(s, "-")
	digits := strings.TrimPrefix(s, "-")
	n, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("masking: int_fpe on %s received %q, which is not an integer", c, *in)
	}
	width := len(digits)
	// Zero maps to zero. A count of zero, a balance of zero, and an identifier
	// of zero all mean something specific, and inventing a value for them
	// changes what the data says.
	if n == 0 {
		return in, nil
	}
	lo := uint64(1)
	for i := 1; i < width; i++ {
		lo *= 10
	}
	hi := lo * 10
	if width == 1 {
		lo = 1
		hi = 10
	}
	span := hi - lo
	out := lo + (prf(k.Sub(c), s) % span)
	text := strconv.FormatUint(out, 10)
	if neg {
		text = "-" + text
	}
	return str(text), nil
}

// stringFPETransform replaces a string with one of the same length and
// character classes.
//
// This is the general purpose transform for an identifier with a format:
// an order reference, a licence key, an account code. Class preservation means
// a regular expression check constraint on the column still matches.
type stringFPETransform struct{}

func (stringFPETransform) Name() string { return "string_fpe" }

func (stringFPETransform) Describe() string {
	return "Replaces a string with one of the same length, keeping digits as digits and letters as letters so a format check still matches."
}

func (stringFPETransform) PreservesUniqueness() bool { return false }

func (stringFPETransform) Apply(k *Key, c Column, in *string) (*string, error) {
	if nullOK(in) {
		return nil, nil
	}
	if *in == "" {
		return in, nil
	}
	s := newPRFStream(k.Sub(c), *in)
	runes := []rune(*in)
	for i, r := range runes {
		switch {
		case unicode.IsDigit(r):
			runes[i] = rune('0' + s.intn(10))
		case r >= 'a' && r <= 'z':
			runes[i] = rune('a' + s.intn(26))
		case r >= 'A' && r <= 'Z':
			runes[i] = rune('A' + s.intn(26))
		}
		// Everything else is punctuation or structure and is left alone, which
		// is what preserves the format.
	}
	return str(string(runes)), nil
}

// dateShiftTransform moves a timestamp by a per record offset.
//
// The offset is derived from the value itself, so the same instant always
// moves to the same instant, and ordering between two rows is preserved when
// they carry the same value. What it deliberately does not preserve is the
// exact interval between two different timestamps, since that interval is
// often the identifying detail: a signup and a first purchase four minutes
// apart is a fingerprint.
type dateShiftTransform struct{}

func (dateShiftTransform) Name() string { return "date_shift" }

func (dateShiftTransform) Describe() string {
	return "Moves a date or timestamp by a deterministic offset of up to a year, keeping its format and its time of day."
}

func (dateShiftTransform) PreservesUniqueness() bool { return false }

// dateLayouts are the spellings Postgres hands back for date and timestamp
// columns. The output keeps whichever layout the input used, because a text
// column holding a date is compared as text.
var dateLayouts = []string{
	"2006-01-02 15:04:05.999999-07",
	"2006-01-02 15:04:05.999999-07:00",
	"2006-01-02T15:04:05.999999Z07:00",
	"2006-01-02 15:04:05.999999",
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02",
	"15:04:05",
}

func (dateShiftTransform) Apply(k *Key, c Column, in *string) (*string, error) {
	if nullOK(in) {
		return nil, nil
	}
	s := strings.TrimSpace(*in)
	if s == "" {
		return in, nil
	}
	for _, layout := range dateLayouts {
		t, err := time.Parse(layout, s)
		if err != nil {
			continue
		}
		// Up to a year in either direction, in whole days, so that a date only
		// column stays a whole date and a weekday sensitive report still has
		// weekdays.
		days := int(prf(k.Sub(c), s)%730) - 365
		shifted := t.AddDate(0, 0, days)
		if layout == "15:04:05" {
			// A bare time has no date to shift, so the seconds move instead.
			shifted = t.Add(time.Duration(prf(k.Sub(c), s)%3600) * time.Second)
		}
		return str(shifted.Format(layout)), nil
	}
	return nil, fmt.Errorf("masking: date_shift on %s could not parse %q as a date or timestamp", c, s)
}

// numericNoiseTransform perturbs a number by a bounded percentage.
//
// It exists for columns where the distribution matters and the exact value
// does not: an amount, a score, a duration. The perturbation is bounded so
// that a sum over the table stays the right order of magnitude, which keeps a
// dashboard built on that sum looking plausible.
type numericNoiseTransform struct{}

func (numericNoiseTransform) Name() string { return "numeric_noise" }

func (numericNoiseTransform) Describe() string {
	return "Moves a number by up to ten percent, keeping its sign, scale, and decimal places, so totals stay the right order of magnitude."
}

func (numericNoiseTransform) PreservesUniqueness() bool { return false }

func (numericNoiseTransform) Apply(k *Key, c Column, in *string) (*string, error) {
	if nullOK(in) {
		return nil, nil
	}
	s := strings.TrimSpace(*in)
	if s == "" {
		return in, nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, fmt.Errorf("masking: numeric_noise on %s received %q, which is not a number", c, *in)
	}
	if v == 0 {
		// Zero is a statement, not a magnitude. Perturbing it would turn "no
		// balance" into "a small balance", which is a different fact.
		return in, nil
	}
	// The scale is preserved so that a numeric(10,2) column still holds two
	// decimal places and does not fail its own type.
	places := 0
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		places = len(s) - dot - 1
	}
	factor := 0.9 + float64(prf(k.Sub(c), s)%2001)/10000.0 // 0.9 to 1.1
	out := v * factor
	if math.IsInf(out, 0) || math.IsNaN(out) {
		return in, nil
	}
	return str(strconv.FormatFloat(out, 'f', places, 64)), nil
}

// nullifyTransform empties a column.
//
// It is the default for a text column with no rule, because a column nobody
// classified is a column nobody has confirmed is safe, and the safe answer for
// free text is to remove it. The planner lists every column it nullified so
// that a user can allowlist the ones that were fine.
type nullifyTransform struct{}

func (nullifyTransform) Name() string { return "nullify" }

func (nullifyTransform) Describe() string {
	return "Sets the column to null. This is the default for unclassified free text, because a column nobody has confirmed is safe is not safe."
}

func (nullifyTransform) PreservesUniqueness() bool { return false }

func (nullifyTransform) Apply(_ *Key, _ Column, _ *string) (*string, error) {
	return nil, nil
}

// hashHexTransform replaces a value with a keyed hash.
//
// It is the transform for a value whose only job is to be compared for
// equality: an external identifier, a device token, an opaque key. It
// preserves uniqueness and destroys everything else.
type hashHexTransform struct{}

func (hashHexTransform) Name() string { return "hash_hex" }

func (hashHexTransform) Describe() string {
	return "Replaces a value with a keyed hash of the same length. Equality is preserved and nothing else is."
}

func (hashHexTransform) PreservesUniqueness() bool { return true }

func (hashHexTransform) Apply(k *Key, c Column, in *string) (*string, error) {
	if nullOK(in) {
		return nil, nil
	}
	if *in == "" {
		return in, nil
	}
	// The output is the same length as the input, so a column with a length
	// constraint or a fixed width type still fits.
	width := len([]rune(*in))
	if width > 128 {
		width = 128
	}
	b := prfBytes(k.Sub(c), *in, (width+1)/2)
	return str(hex.EncodeToString(b)[:width]), nil
}

// usernameTransform replaces a handle while keeping it unique.
type usernameTransform struct{}

func (usernameTransform) Name() string { return "username" }

func (usernameTransform) Describe() string {
	return "Replaces a handle with a unique synthetic one made of a word and a number."
}

func (usernameTransform) PreservesUniqueness() bool { return true }

func (usernameTransform) Apply(k *Key, c Column, in *string) (*string, error) {
	if nullOK(in) {
		return nil, nil
	}
	if *in == "" {
		return in, nil
	}
	sub := k.Sub(c)
	h := prf(sub, strings.ToLower(*in))
	word := strings.ToLower(givenNames[h%uint64(len(givenNames))])
	return str(fmt.Sprintf("%s-%s", word, base32Encode(h, 8))), nil
}

// ipTransform replaces an address while keeping it in a documentation range.
type ipTransform struct{}

func (ipTransform) Name() string { return "ip" }

func (ipTransform) Describe() string {
	return "Replaces an IP address with one from a documentation range reserved by RFC 5737, which can never route anywhere."
}

func (ipTransform) PreservesUniqueness() bool { return false }

func (ipTransform) Apply(k *Key, c Column, in *string) (*string, error) {
	if nullOK(in) {
		return nil, nil
	}
	s := strings.TrimSpace(*in)
	if s == "" {
		return in, nil
	}
	if strings.Contains(s, ":") {
		// IPv6 documentation prefix from RFC 3849. The first two groups are
		// fixed by the reservation; the remaining six are derived.
		b := prfBytes(k.Sub(c), s, 12)
		return str(fmt.Sprintf("2001:db8:%02x%02x:%02x%02x:%02x%02x:%02x%02x:%02x%02x:%02x%02x",
			b[0], b[1], b[2], b[3], b[4], b[5], b[6], b[7], b[8], b[9], b[10], b[11])), nil
	}
	b := prfBytes(k.Sub(c), s, 2)
	// 192.0.2.0/24 is reserved for documentation and is not routable.
	return str(fmt.Sprintf("192.0.2.%d", b[0])), nil
}

// urlTransform keeps a URL's structure and replaces its identifying parts.
type urlTransform struct{}

func (urlTransform) Name() string { return "url" }

func (urlTransform) Describe() string {
	return "Keeps a URL's scheme and path shape, replacing its host with a synthetic one at example.test."
}

func (urlTransform) PreservesUniqueness() bool { return false }

func (urlTransform) Apply(k *Key, c Column, in *string) (*string, error) {
	if nullOK(in) {
		return nil, nil
	}
	s := strings.TrimSpace(*in)
	if s == "" {
		return in, nil
	}
	scheme := "https://"
	rest := s
	if i := strings.Index(s, "://"); i >= 0 {
		scheme, rest = s[:i+3], s[i+3:]
	}
	pathPart := ""
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		pathPart = rest[i:]
	}
	stream := newPRFStream(k.Sub(c), s)
	host := strings.ToLower(companyPrefixes[stream.intn(len(companyPrefixes))]) + "." + syntheticDomain
	// The path is rewritten too, since a path routinely carries an identifier.
	if pathPart != "" {
		segments := strings.Split(pathPart, "/")
		for i, seg := range segments {
			if seg == "" {
				continue
			}
			b := prfBytes(k.Sub(c), s+"/"+strconv.Itoa(i), 4)
			segments[i] = hex.EncodeToString(b)[:min(len(seg), 8)]
		}
		pathPart = strings.Join(segments, "/")
	}
	return str(scheme + host + pathPart), nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// creditCardTransform replaces a card number with a Luhn valid test number.
//
// Luhn validity matters: a payment form validates the check digit client side,
// so a masked number that fails it makes a checkout flow untestable. The
// prefixes used are the ones every payment processor documents as test cards,
// so a number that escapes into a real system is rejected as a test card
// rather than charged.
type creditCardTransform struct{}

func (creditCardTransform) Name() string { return "credit_card" }

func (creditCardTransform) Describe() string {
	return "Replaces a card number with a Luhn valid test number, so a payment form still validates it and no real card is ever present."
}

func (creditCardTransform) PreservesUniqueness() bool { return false }

func (creditCardTransform) Apply(k *Key, c Column, in *string) (*string, error) {
	if nullOK(in) {
		return nil, nil
	}
	s := strings.TrimSpace(*in)
	if s == "" {
		return in, nil
	}
	digits := 0
	for _, r := range s {
		if unicode.IsDigit(r) {
			digits++
		}
	}
	if digits < 12 || digits > 19 {
		return nil, fmt.Errorf(
			"masking: credit_card on %s received a value with %d digits, which is not a card number", c, digits)
	}
	// 4242... is the test prefix every processor documents.
	body := "4242"
	stream := newPRFStream(k.Sub(c), s)
	for len(body) < digits-1 {
		body += strconv.Itoa(stream.intn(10))
	}
	body += strconv.Itoa(luhnCheckDigit(body))

	// The separators of the original are restored, so a column that stored a
	// spaced number still holds a spaced number.
	var out strings.Builder
	pos := 0
	for _, r := range s {
		if unicode.IsDigit(r) {
			out.WriteByte(body[pos])
			pos++
			continue
		}
		out.WriteRune(r)
	}
	return str(out.String()), nil
}

// luhnCheckDigit returns the digit that makes a number pass the Luhn check.
func luhnCheckDigit(body string) int {
	sum, double := 0, true
	for i := len(body) - 1; i >= 0; i-- {
		d := int(body[i] - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		double = !double
		sum += d
	}
	return (10 - sum%10) % 10
}

// freeTextTransform replaces prose with prose of a similar length.
//
// Length similarity is the point. A column holding a review or a support note
// is rendered somewhere, and replacing three paragraphs with the word "text"
// means the layout that has to hold three paragraphs is never exercised.
type freeTextTransform struct{}

func (freeTextTransform) Name() string { return "free_text" }

func (freeTextTransform) Describe() string {
	return "Replaces prose with synthetic prose of a similar length, so a layout built for three paragraphs still gets three paragraphs."
}

func (freeTextTransform) PreservesUniqueness() bool { return false }

func (freeTextTransform) Apply(k *Key, c Column, in *string) (*string, error) {
	if nullOK(in) {
		return nil, nil
	}
	if *in == "" {
		return in, nil
	}
	target := len([]rune(*in))
	s := newPRFStream(k.Sub(c), *in)
	var b strings.Builder
	b.Grow(target + 16)
	for b.Len() < target {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(sentenceWords[s.intn(len(sentenceWords))])
	}
	out := []rune(b.String())
	if len(out) > target {
		out = out[:target]
	}
	// A truncated last word is trimmed rather than left as a fragment, unless
	// trimming would empty the value.
	text := strings.TrimRight(string(out), " ")
	if i := strings.LastIndexByte(text, ' '); i > 0 && len(text) == target {
		text = text[:i] + strings.Repeat(".", target-i)
	}
	if text == "" {
		text = strings.Repeat(".", target)
	}
	return str(text), nil
}

// preserveTransform leaves a value alone.
//
// It exists so that "this column is fine" is something a rules file states
// explicitly rather than omits. An omission is indistinguishable from an
// oversight; a preserve rule is a decision someone made and a reviewer can
// question.
type preserveTransform struct{}

func (preserveTransform) Name() string { return "preserve" }

func (preserveTransform) Describe() string {
	return "Leaves the value unchanged. Use it to record that a column was reviewed and found safe, rather than leaving it out."
}

func (preserveTransform) PreservesUniqueness() bool { return true }

func (preserveTransform) Apply(_ *Key, _ Column, in *string) (*string, error) {
	return in, nil
}
