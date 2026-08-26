package masking_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"pgregory.net/rapid"

	"github.com/antifailure/antifailure/engine/internal/masking"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func testKey(t require.TestingT) *masking.Key {
	k, err := masking.NewKeyFromBytes([]byte("a-test-key-that-is-long-enough-32"))
	require.NoError(t, err)
	return k
}

func col(table, name string) masking.Column {
	return masking.Column{Schema: "public", Table: table, Name: name}
}

func apply(t *testing.T, name string, c masking.Column, in string) string {
	t.Helper()
	tr, ok := masking.Lookup(name)
	require.True(t, ok, "transform %q is not registered", name)
	out, err := tr.Apply(testKey(t), c, &in)
	require.NoError(t, err)
	require.NotNil(t, out)
	return *out
}

func TestRegistry_EveryTransformIsDescribed(t *testing.T) {
	t.Parallel()
	names := masking.Names()
	require.NotEmpty(t, names)
	for _, n := range names {
		tr, ok := masking.Lookup(n)
		require.True(t, ok)
		require.Equal(t, n, tr.Name())
		d := tr.Describe()
		require.NotEmpty(t, d, "%s has no description", n)
		require.True(t, strings.HasSuffix(d, "."), "%s description is not a sentence", n)
		require.NotContains(t, d, "—", "%s description uses an em dash", n)
	}
}

// The property the whole product rests on. Without it, the same customer
// becomes two different fake customers in two tables and every join breaks.
func TestTransforms_AreDeterministic(t *testing.T) {
	t.Parallel()
	for _, name := range masking.Names() {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tr, _ := masking.Lookup(name)
			rapid.Check(t, func(rt *rapid.T) {
				in := genValueFor(rt, name)
				c := col("users", "value")

				first, err1 := tr.Apply(testKey(rt), c, &in)
				second, err2 := tr.Apply(testKey(rt), c, &in)
				if (err1 == nil) != (err2 == nil) {
					rt.Fatalf("the same input produced an error only once")
				}
				if err1 != nil {
					return
				}
				if !eqPtr(first, second) {
					rt.Fatalf("%s is not deterministic for %q: %v then %v",
						name, in, deref(first), deref(second))
				}
			})
		})
	}
}

// Null carries information: a not null constraint that was satisfied has to
// stay satisfied, and an optional field that was empty means something.
func TestTransforms_PreserveNull(t *testing.T) {
	t.Parallel()
	for _, name := range masking.Names() {
		tr, _ := masking.Lookup(name)
		out, err := tr.Apply(testKey(t), col("users", "value"), nil)
		require.NoError(t, err, "%s errored on null", name)
		require.Nil(t, out, "%s turned null into a value", name)
	}
}

// A column masked in two different tables with the same link must produce the
// same output, or a foreign key stops resolving.
func TestTransforms_LinkedColumnsAgree(t *testing.T) {
	t.Parallel()
	const id = "3f2504e0-4f89-41d3-9a0c-0305e82c3301"
	tr, _ := masking.Lookup("uuid_remap")
	k := testKey(t)

	left := masking.Column{Schema: "public", Table: "users", Name: "id", Link: "user-id"}
	right := masking.Column{Schema: "public", Table: "orders", Name: "user_id", Link: "user-id"}
	unlinked := masking.Column{Schema: "public", Table: "orders", Name: "user_id"}

	in := id
	a, err := tr.Apply(k, left, &in)
	require.NoError(t, err)
	b, err := tr.Apply(k, right, &in)
	require.NoError(t, err)
	require.Equal(t, *a, *b, "linked columns must map identically or joins break")

	c, err := tr.Apply(k, unlinked, &in)
	require.NoError(t, err)
	require.NotEqual(t, *a, *c,
		"an unlinked column must get its own key, or knowing one column reveals another")
}

// A different key must produce a different result, or the key is decorative.
func TestTransforms_DependOnTheKey(t *testing.T) {
	t.Parallel()
	k1, err := masking.NewKeyFromBytes([]byte("first-key-long-enough-to-pass!!"))
	require.NoError(t, err)
	k2, err := masking.NewKeyFromBytes([]byte("second-key-long-enough-to-pass!"))
	require.NoError(t, err)

	in := "customer@example.com"
	tr, _ := masking.Lookup("email")
	a, err := tr.Apply(k1, col("users", "email"), &in)
	require.NoError(t, err)
	b, err := tr.Apply(k2, col("users", "email"), &in)
	require.NoError(t, err)
	require.NotEqual(t, *a, *b)
}

func TestKey_RefusesAShortSecret(t *testing.T) {
	t.Parallel()
	// A guessable masking key means the masking is reversible, which is the
	// one thing it must never be.
	_, err := masking.NewKeyFromBytes([]byte("short"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least 16")
}

func TestKey_FingerprintIsStableAndShort(t *testing.T) {
	t.Parallel()
	k := testKey(t)
	fp := k.Fingerprint()
	require.Len(t, fp, 16)
	require.Equal(t, fp, testKey(t).Fingerprint())
	// An attestation records which key produced a golden without recording the
	// key, so the fingerprint must not be derivable back to the material.
	require.NotContains(t, fp, "a-test-key")
}

// An email column almost always carries a unique index. A transform that
// collides fails the masking run partway through a large table rather than at
// planning time, which is the worst moment to find out.
func TestEmail_IsUniqueAcrossManyInputs(t *testing.T) {
	t.Parallel()
	tr, _ := masking.Lookup("email")
	k := testKey(t)
	seen := make(map[string]string, 50000)
	for i := 0; i < 50000; i++ {
		in := fmt.Sprintf("user%d@customer-%d.example.com", i, i%97)
		out, err := tr.Apply(k, col("users", "email"), &in)
		require.NoError(t, err)
		if prev, dup := seen[*out]; dup {
			t.Fatalf("collision: %q and %q both map to %q", prev, in, *out)
		}
		seen[*out] = in
	}
	require.True(t, tr.PreservesUniqueness())
}

func TestEmail_LandsInAReservedDomain(t *testing.T) {
	t.Parallel()
	// example.test is reserved by RFC 6761. It can never be registered, never
	// resolves, and can never receive mail, which is the last line of defence
	// if an application under test somehow reaches a real mail provider.
	for _, in := range []string{"a@b.com", "Person.Name@Corp.example.org", "x@y"} {
		got := apply(t, "email", col("users", "email"), in)
		require.True(t, strings.HasSuffix(got, "@example.test"), "%q became %q", in, got)
		require.NotContains(t, got, "@b.com")
	}
}

func TestEmail_IsCaseInsensitiveLikeARealMailbox(t *testing.T) {
	t.Parallel()
	lower := apply(t, "email", col("users", "email"), "person@example.com")
	upper := apply(t, "email", col("users", "email"), "Person@Example.COM")
	require.Equal(t, lower, upper,
		"two rows differing only in case are the same mailbox and must map together")
}

func TestEmail_KeepsPlusAddressing(t *testing.T) {
	t.Parallel()
	// Plus addressing is often load bearing in a sign up flow, and losing it
	// changes what the application does.
	got := apply(t, "email", col("users", "email"), "person+newsletter@example.com")
	require.Contains(t, got, "+")
	plain := apply(t, "email", col("users", "email"), "person@example.com")
	require.NotEqual(t, plain, got)
}

func TestEmail_LeavesAnEmptyStringAlone(t *testing.T) {
	t.Parallel()
	require.Equal(t, "", apply(t, "email", col("users", "email"), ""))
}

// A check constraint or a client side validator often depends on a phone
// number's exact shape.
func TestPhone_PreservesShapeAndCountryCode(t *testing.T) {
	t.Parallel()
	cases := []string{
		"+1 415 555 0132",
		"+44 20 7946 0958",
		"(415) 555-0132",
		"07700 900123",
		"4155550132",
		"+61-2-5550-1234",
	}
	for _, in := range cases {
		got := apply(t, "phone", col("users", "phone"), in)
		require.Equal(t, len(in), len(got), "%q changed length", in)
		require.NotEqual(t, in, got, "%q was not masked", in)
		for i := range in {
			inDigit := unicode.IsDigit(rune(in[i]))
			outDigit := unicode.IsDigit(rune(got[i]))
			require.Equal(t, inDigit, outDigit,
				"%q: position %d changed character class in %q", in, i, got)
		}
		if strings.HasPrefix(in, "+") {
			// The country calling code is what makes a number valid at all.
			require.Equal(t, in[:3], got[:3], "the country code of %q changed", in)
		}
		if strings.HasPrefix(in, "0") {
			require.True(t, strings.HasPrefix(got, "0"),
				"a leading zero is significant in a national format")
		}
	}
}

func TestUUIDRemap_ProducesAValidVersionFourUUID(t *testing.T) {
	t.Parallel()
	got := apply(t, "uuid_remap", col("users", "id"), "3f2504e0-4f89-41d3-9a0c-0305e82c3301")
	require.Len(t, got, 36)
	require.Equal(t, byte('4'), got[14], "a library that validates the version must still accept it")
	require.Contains(t, "89ab", string(got[19]), "the RFC 4122 variant bits must be set")
}

func TestUUIDRemap_IsUniqueAcrossManyInputs(t *testing.T) {
	t.Parallel()
	tr, _ := masking.Lookup("uuid_remap")
	k := testKey(t)
	seen := make(map[string]bool, 20000)
	for i := 0; i < 20000; i++ {
		in := fmt.Sprintf("%08x-0000-4000-8000-%012x", i, i)
		out, err := tr.Apply(k, col("users", "id"), &in)
		require.NoError(t, err)
		require.False(t, seen[*out], "collision on %q", in)
		seen[*out] = true
	}
}

// A rule pointed at the wrong column has to fail loudly at the value rather
// than write something the column's type will not hold.
func TestUUIDRemap_RefusesAValueThatIsNotAUUID(t *testing.T) {
	t.Parallel()
	tr, _ := masking.Lookup("uuid_remap")
	in := "not-a-uuid"
	_, err := tr.Apply(testKey(t), col("users", "id"), &in)
	require.Error(t, err)
	require.Contains(t, err.Error(), "hash_hex")
}

func TestIntFPE_KeepsDigitCountAndSign(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"7", "42", "1000", "999999", "-350", "-1"} {
		got := apply(t, "int_fpe", col("orders", "quantity"), in)
		require.Equal(t, len(in), len(got), "%q became %q", in, got)
		require.Equal(t, strings.HasPrefix(in, "-"), strings.HasPrefix(got, "-"))
		_, err := strconv.Atoi(got)
		require.NoError(t, err)
	}
}

func TestIntFPE_LeavesZeroAlone(t *testing.T) {
	t.Parallel()
	// A count of zero, a balance of zero, and an identifier of zero all mean
	// something specific. Inventing a value changes what the data says.
	require.Equal(t, "0", apply(t, "int_fpe", col("orders", "quantity"), "0"))
}

func TestStringFPE_PreservesLengthAndCharacterClasses(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"ORD-2024-00193", "ab12CD34", "SKU_991-X", "12345"} {
		got := apply(t, "string_fpe", col("orders", "reference"), in)
		require.Equal(t, len(in), len(got))
		for i := range in {
			a, b := rune(in[i]), rune(got[i])
			require.Equal(t, unicode.IsDigit(a), unicode.IsDigit(b), "%q at %d", in, i)
			require.Equal(t, unicode.IsUpper(a), unicode.IsUpper(b), "%q at %d", in, i)
			if !unicode.IsLetter(a) && !unicode.IsDigit(a) {
				require.Equal(t, a, b, "%q: punctuation at %d must survive", in, i)
			}
		}
	}
}

func TestDateShift_KeepsTheLayoutAndMovesTheDate(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"2024-03-15":           "2006-01-02",
		"2024-03-15 09:31:00":  "2006-01-02 15:04:05",
		"2024-03-15T09:31:00Z": "2006-01-02T15:04:05Z07:00",
	}
	for in, layout := range cases {
		got := apply(t, "date_shift", col("users", "created_at"), in)
		parsed, err := time.Parse(layout, got)
		require.NoError(t, err, "%q became %q, which does not parse as %s", in, got, layout)
		require.NotEqual(t, in, got, "%q was not shifted", in)

		orig, err := time.Parse(layout, in)
		require.NoError(t, err)
		delta := parsed.Sub(orig)
		require.LessOrEqual(t, delta.Abs(), 366*24*time.Hour,
			"the shift must stay within a year so the data stays plausible")
		require.Equal(t, orig.Hour(), parsed.Hour(), "the time of day must survive a date shift")
	}
}

func TestDateShift_RefusesSomethingThatIsNotADate(t *testing.T) {
	t.Parallel()
	tr, _ := masking.Lookup("date_shift")
	in := "yesterday"
	_, err := tr.Apply(testKey(t), col("users", "created_at"), &in)
	require.Error(t, err)
}

func TestNumericNoise_KeepsScaleAndOrderOfMagnitude(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"1250.00", "0.05", "-99.99", "42"} {
		got := apply(t, "numeric_noise", col("orders", "total"), in)
		wantPlaces := 0
		if dot := strings.IndexByte(in, '.'); dot >= 0 {
			wantPlaces = len(in) - dot - 1
		}
		gotPlaces := 0
		if dot := strings.IndexByte(got, '.'); dot >= 0 {
			gotPlaces = len(got) - dot - 1
		}
		// A numeric(10,2) column still has to hold two decimal places, or the
		// masked value fails its own type.
		require.Equal(t, wantPlaces, gotPlaces, "%q became %q", in, got)

		orig, err := strconv.ParseFloat(in, 64)
		require.NoError(t, err)
		out, err := strconv.ParseFloat(got, 64)
		require.NoError(t, err)
		require.Equal(t, orig < 0, out < 0, "the sign of %q changed", in)
		require.InEpsilon(t, orig, out, 0.11, "%q moved more than ten percent to %q", in, got)
	}
}

func TestNumericNoise_LeavesZeroAlone(t *testing.T) {
	t.Parallel()
	// Zero is a statement, not a magnitude. Perturbing it turns "no balance"
	// into "a small balance", which is a different fact.
	require.Equal(t, "0.00", apply(t, "numeric_noise", col("accounts", "balance"), "0.00"))
}

func TestCreditCard_ProducesALuhnValidTestNumber(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"4111111111111111", "4111 1111 1111 1111", "5500-0000-0000-0004"} {
		got := apply(t, "credit_card", col("payments", "card"), in)
		require.Equal(t, len(in), len(got))
		digits := stripNonDigits(got)
		// A payment form validates the check digit client side, so a masked
		// number that fails Luhn makes a checkout flow untestable.
		require.True(t, luhnValid(digits), "%q became %q, which fails the Luhn check", in, got)
		// The documented test prefix means a number that escapes into a real
		// system is rejected as a test card rather than charged.
		require.True(t, strings.HasPrefix(digits, "4242"), "%q became %q", in, got)
	}
}

func TestCreditCard_RefusesSomethingThatIsNotACard(t *testing.T) {
	t.Parallel()
	tr, _ := masking.Lookup("credit_card")
	in := "1234"
	_, err := tr.Apply(testKey(t), col("payments", "card"), &in)
	require.Error(t, err)
}

func TestFreeText_KeepsLengthSoLayoutIsStillExercised(t *testing.T) {
	t.Parallel()
	for _, n := range []int{1, 12, 140, 2000} {
		in := strings.Repeat("real customer feedback ", n/23+1)[:n]
		got := apply(t, "free_text", col("reviews", "body"), in)
		require.Len(t, []rune(got), n, "a %d character review became %d characters", n, len([]rune(got)))
		require.NotEqual(t, in, got)
	}
}

func TestHashHex_KeepsLengthAndUniqueness(t *testing.T) {
	t.Parallel()
	tr, _ := masking.Lookup("hash_hex")
	k := testKey(t)
	seen := map[string]bool{}
	for i := 0; i < 5000; i++ {
		in := fmt.Sprintf("external-identifier-%d", i)
		out, err := tr.Apply(k, col("users", "external_id"), &in)
		require.NoError(t, err)
		require.Len(t, *out, len(in), "a column with a length constraint must still fit")
		require.False(t, seen[*out], "collision on %q", in)
		seen[*out] = true
	}
}

func TestNullify_EmptiesEverything(t *testing.T) {
	t.Parallel()
	tr, _ := masking.Lookup("nullify")
	in := "anything at all"
	out, err := tr.Apply(testKey(t), col("notes", "body"), &in)
	require.NoError(t, err)
	require.Nil(t, out)
}

func TestPreserve_LeavesTheValueAlone(t *testing.T) {
	t.Parallel()
	// preserve exists so that "this column is fine" is stated rather than
	// omitted: an omission is indistinguishable from an oversight.
	require.Equal(t, "keep me", apply(t, "preserve", col("settings", "theme"), "keep me"))
}

func TestIP_LandsInADocumentationRange(t *testing.T) {
	t.Parallel()
	// RFC 5737 and RFC 3849 ranges can never route anywhere, so a masked
	// address cannot reach a real host even if something tries.
	require.True(t, strings.HasPrefix(apply(t, "ip", col("events", "ip"), "203.0.42.17"), "192.0.2."))
	require.True(t, strings.HasPrefix(apply(t, "ip", col("events", "ip"), "2a00:1450:4009::200e"), "2001:db8:"))
}

func TestURL_KeepsTheSchemeAndReplacesTheHost(t *testing.T) {
	t.Parallel()
	got := apply(t, "url", col("users", "website"), "https://acme-corp.com/customers/9182/invoice")
	require.True(t, strings.HasPrefix(got, "https://"))
	require.Contains(t, got, ".example.test")
	require.NotContains(t, got, "acme-corp")
	require.NotContains(t, got, "9182", "an identifier in a path is still an identifier")
}

func TestName_KeepsThePartCount(t *testing.T) {
	t.Parallel()
	// A form that renders a first and last name separately behaves differently
	// for a one word name.
	require.Len(t, strings.Fields(apply(t, "name", col("users", "name"), "Prince")), 1)
	require.Len(t, strings.Fields(apply(t, "name", col("users", "name"), "Ada Lovelace")), 2)
	require.Len(t, strings.Fields(apply(t, "name", col("users", "name"), "Ada M Lovelace")), 3)
}

func TestPostcode_KeepsTheFormat(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"SW1A 1AA", "94103", "K1A 0B1", "75008"} {
		got := apply(t, "postcode", col("addresses", "postcode"), in)
		require.Equal(t, len(in), len(got))
		for i := range in {
			a, b := rune(in[i]), rune(got[i])
			require.Equal(t, unicode.IsDigit(a), unicode.IsDigit(b), "%q at %d", in, i)
			require.Equal(t, unicode.IsLetter(a), unicode.IsLetter(b), "%q at %d", in, i)
		}
	}
}

// A transform without this property must never be placed on a unique column,
// and the planner relies on the flag being honest.
func TestPreservesUniqueness_IsHonest(t *testing.T) {
	t.Parallel()
	k := testKey(t)
	for _, name := range masking.Names() {
		tr, _ := masking.Lookup(name)
		if !tr.PreservesUniqueness() || name == "preserve" {
			continue
		}
		seen := map[string]string{}
		for i := 0; i < 20000; i++ {
			in := genUniqueInputFor(name, i)
			out, err := tr.Apply(k, col("t", "c"), &in)
			if err != nil || out == nil {
				continue
			}
			if prev, dup := seen[*out]; dup && prev != in {
				t.Fatalf("%s claims to preserve uniqueness but %q and %q both map to %q",
					name, prev, in, *out)
			}
			seen[*out] = in
		}
	}
}

func genUniqueInputFor(name string, i int) string {
	switch name {
	case "uuid_remap":
		return fmt.Sprintf("%08x-0000-4000-8000-%012x", i, i)
	case "email":
		return fmt.Sprintf("person%d@example.com", i)
	default:
		return fmt.Sprintf("value-%d", i)
	}
}

// genValueFor draws an input the transform can accept, so that the determinism
// property is tested with real shapes rather than only with rejections.
func genValueFor(rt *rapid.T, name string) string {
	switch name {
	case "uuid_remap":
		return rapid.SampledFrom([]string{
			"3f2504e0-4f89-41d3-9a0c-0305e82c3301",
			"00000000-0000-4000-8000-000000000000",
			"FFFFFFFF-FFFF-4FFF-BFFF-FFFFFFFFFFFF",
		}).Draw(rt, "uuid")
	case "int_fpe":
		return strconv.Itoa(rapid.IntRange(-999999, 999999).Draw(rt, "n"))
	case "numeric_noise":
		return fmt.Sprintf("%.2f", rapid.Float64Range(-1e6, 1e6).Draw(rt, "f"))
	case "date_shift":
		return rapid.SampledFrom([]string{
			"2024-03-15", "2024-03-15 09:31:00", "2001-01-01T00:00:00Z",
		}).Draw(rt, "date")
	case "credit_card":
		return rapid.SampledFrom([]string{
			"4111111111111111", "5500 0000 0000 0004", "378282246310005",
		}).Draw(rt, "card")
	case "email":
		return rapid.SampledFrom([]string{
			"a@b.com", "person.name+tag@corp.example.org", "x@y", "",
		}).Draw(rt, "email")
	default:
		return rapid.StringN(0, 64, -1).Draw(rt, "s")
	}
}

func eqPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func deref(s *string) string {
	if s == nil {
		return "<null>"
	}
	return *s
}

func stripNonDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func luhnValid(digits string) bool {
	sum, double := 0, false
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i] - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		double = !double
		sum += d
	}
	return sum%10 == 0
}
