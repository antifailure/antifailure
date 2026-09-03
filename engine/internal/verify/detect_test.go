package verify_test

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/antifailure/antifailure/engine/internal/verify"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func detector(t *testing.T, name string) verify.Detector {
	t.Helper()
	for _, d := range verify.Detectors() {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("no detector named %q", name)
	return verify.Detector{}
}

func TestDetectors_AreDescribedAndRanked(t *testing.T) {
	t.Parallel()
	ds := verify.Detectors()
	require.NotEmpty(t, ds)
	seen := map[string]bool{}
	for _, d := range ds {
		require.False(t, seen[d.Name], "duplicate detector %q", d.Name)
		seen[d.Name] = true
		require.NotEmpty(t, d.Describe, "%s has no description", d.Name)
		require.True(t, strings.HasSuffix(d.Describe, "."), "%s description is not a sentence", d.Name)
		require.NotNil(t, d.Match)
		// A card number is worse than a name, and a report that treats them
		// alike buries the thing that matters.
		require.GreaterOrEqual(t, d.Severity, verify.SeverityLow)
		require.LessOrEqual(t, d.Severity, verify.SeverityHigh)
	}
}

// The true positive corpus. A miss here is data reaching a preview
// environment, which is the failure the whole product exists to prevent.
func TestDetectors_FindRealData(t *testing.T) {
	t.Parallel()
	cases := []struct{ detector, value string }{
		{"email", "sarah.chen@acmecorp.com"},
		{"email", "Contact us at billing@northwind-logistics.co.uk for invoices"},
		{"email", `{"user":{"email":"j.doe@realcompany.io"}}`},
		{"phone", "+14155550132"},
		{"phone", "+442079460958"},
		{"phone", "phone: (415) 555-0132"},
		{"phone", "mobile 415-555-0132 is the contact"},
		{"payment-card", "4111111111111111"},
		{"payment-card", "4111 1111 1111 1111"},
		{"payment-card", "card on file: 5500-0000-0000-0004"},
		{"payment-card", "378282246310005"},
		{"national-id", "123-45-6789"},
		{"national-id", "SSN 078-05-1120 on record"},
		{"iban", "GB82WEST12345698765432"},
		{"iban", "DE89370400440532013000"},
		{"jwt", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"},
		{"private-key", "-----BEGIN RSA PRIVATE KEY-----\nMIIEow==\n-----END RSA PRIVATE KEY-----"},
		{"credential", "sk" + "_live_" + "51H8fjKlmNopQrStUvWxYz0123456789"},
		{"credential", "gh" + "p_" + "abcdefghijklmnopqrstuvwxyz0123456789"},
		{"credential", "AK" + "IA" + "IOSFODNN7EXAMPLE"},
		{"ip-address", "203.0.42.17"},
		{"ip-address", "last seen from 8.8.8.8"},
	}
	for _, c := range cases {
		d := detector(t, c.detector)
		require.True(t, d.Match(c.value),
			"%s missed %q, which is exactly the data that must never reach an environment",
			c.detector, c.value)
	}
}

// The false positive corpus. Every entry here is something a masked database
// is full of. A detector that fires on them makes an operator turn
// verification off, and a verification nobody runs protects nothing.
func TestDetectors_DoNotFireOnMaskedOrOrdinaryData(t *testing.T) {
	t.Parallel()
	values := []string{
		// What masking produces.
		"kx7m2p9qr4n8t@example.test",
		"owner@example.test",
		"3f2504e0-4f89-41d3-9a0c-0305e82c3301",
		"4242424242424242",
		"4242 4242 4242 4242",
		"192.0.2.44",
		"203.0.113.9",
		"2001:db8:85a3::8a2e:370:7334",
		"Gwen Larkspur",
		"Northwind Systems",
		"482 Hazel Terrace",
		// Ordinary database content.
		"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		"sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		"01HQ8XZ4K9M2N5P7R3T6V8W0Y2",
		"2024-03-15T09:31:00Z",
		"v1.24.7-rc.3",
		"1234567890123456789",
		"400 800 1200 1600",
		"SELECT id FROM users WHERE tenant_id = $1",
		"/var/lib/postgresql/data/base/16384/2601",
		"The build took 1234 seconds and used 5678 MB",
		"Order 300-402-1911 shipped",
		"GB12ABCD00000000000000",
		"AAAA11BBBBCCCCDDDDEEEE",
		"127.0.0.1",
		"10.0.4.19",
		"172.16.9.3",
		"192.168.1.1",
		"0.0.0.0",
		"255.255.255.0",
		"999.999.999.999",
		"eyJhbGciOiJIUzI1NiJ9",
		"-----BEGIN CERTIFICATE-----",
		"sk" + "_live_",
		"",
		"null",
		"{}",
	}
	for _, d := range verify.Detectors() {
		for _, v := range values {
			require.False(t, d.Match(v),
				"%s fired on %q, which is ordinary or already masked content", d.Name, v)
		}
	}
}

// Luhn passes on roughly one in ten random digit sequences. Without an issuer
// prefix check, a column of long numeric identifiers produces a finding on
// every tenth row.
func TestPaymentCard_DoesNotFireOnRandomLuhnValidNumbers(t *testing.T) {
	t.Parallel()
	d := detector(t, "payment-card")
	rnd := rand.New(rand.NewSource(20260825))
	falsePositives := 0
	const trials = 20000
	for i := 0; i < trials; i++ {
		// Identifiers that begin with a digit no issuer uses.
		n := fmt.Sprintf("%d%015d", 7+rnd.Intn(3), rnd.Int63n(1e15))
		if d.Match(n) {
			falsePositives++
		}
	}
	require.Zero(t, falsePositives,
		"%d of %d non-issuer identifiers were reported as cards", falsePositives, trials)
}

func TestIBAN_RequiresTheChecksum(t *testing.T) {
	t.Parallel()
	d := detector(t, "iban")
	// The pattern alone matches every uppercase alphanumeric run, which a
	// masked database is full of.
	require.True(t, d.Match("GB82WEST12345698765432"))
	require.False(t, d.Match("GB99WEST12345698765432"), "a wrong checksum is not an account number")
	require.False(t, d.Match("XX00ABCDEFGHIJKLMNOP"))
}

func TestEmail_TreatsReservedDomainsAsSynthetic(t *testing.T) {
	t.Parallel()
	d := detector(t, "email")
	// Matching these would guarantee a finding on every masked row, which
	// would make verification useless rather than strict.
	for _, s := range []string{
		"a@example.test", "b@example.com", "c@example.org", "d@sub.example.invalid",
		"e@host.localhost",
	} {
		require.False(t, d.Match(s), "%q is synthetic by construction", s)
	}
	require.True(t, d.Match("real.person@actualcompany.dev"))
}

// A national format matches a date range, a part number, and a version string.
// Requiring a nearby word that means "phone" cuts the false positive rate to
// near zero.
func TestPhone_NeedsCorroborationForANationalFormat(t *testing.T) {
	t.Parallel()
	d := detector(t, "phone")
	require.False(t, d.Match("Invoice 415 555 0132"))
	require.True(t, d.Match("Phone: 415 555 0132"))
	// E.164 needs no corroboration, because nothing else is written that way.
	require.True(t, d.Match("+14155550132"))
}

func TestCredential_NeedsEntropyAfterThePrefix(t *testing.T) {
	t.Parallel()
	d := detector(t, "credential")
	// A prefix alone is a word, often in documentation or a placeholder.
	require.False(t, d.Match("set your sk"+"_live_ key here"))
	require.True(t, d.Match("sk"+"_live_"+"aBcDeFgHiJkLmNoPqRsTuVwXyZ01"))
}

// A digest is not a payment card, however the digits fall.
//
// The case that found this: `af golden refresh` on a schema with a sha256
// column refused to publish, because one digest in eleven hundred contained a
// thirteen digit run that started with a Visa prefix and passed the Luhn check.
// The finding read `artifacts.sha256 holds payment-card`, which is both wrong
// and unactionable, and a scanner that produces those is a scanner somebody
// turns off.
func TestPaymentCard_DoesNotFireOnDigitsInsideALongerToken(t *testing.T) {
	card := detector(t, "payment-card")

	// A real number, and the same digits with a letter against them. Every one
	// of the second group is part of a token rather than a number anybody
	// wrote, which is the distinction being drawn.
	require.True(t, card.Match("4111111111111111"), "a card on its own must still be found")

	for _, glued := range []string{
		"b4111111111111111",
		"4111111111111111b",
		"a1b4111111111111111cd",
		// A whole digest, which is the shape that actually caused this.
		"9f4111111111111111e3a7c2",
	} {
		require.False(t, card.Match(glued),
			"%q is a token with digits in it, not a payment card", glued)
	}
}

// The other half: narrowing the match must not lose the ways a card is really
// written.
func TestPaymentCard_StillFindsOneWrittenTheWaysPeopleWriteThem(t *testing.T) {
	card := detector(t, "payment-card")

	for _, real := range []string{
		"4111111111111111",
		"4111 1111 1111 1111",
		"4111-1111-1111-1111",
		`{"card":"4111111111111111"}`,
		"card: 4111111111111111",
		"4111 1111 1111 1111 visa",
		"paid with 4111111111111111.",
		"(4111111111111111)",
	} {
		require.True(t, card.Match(real), "%q is a payment card and was not found", real)
	}
}

func TestDetectors_HandlePathologicalInputWithoutHanging(t *testing.T) {
	t.Parallel()
	// A masked database contains long text columns, and a detector that is
	// quadratic on one turns verification into a denial of service against the
	// operator running it.
	inputs := []string{
		strings.Repeat("a", 100000),
		strings.Repeat("1", 100000),
		strings.Repeat("a@b.c ", 20000),
		strings.Repeat("4111111111111111 ", 5000),
		strings.Repeat("-", 100000),
	}
	for _, d := range verify.Detectors() {
		for _, in := range inputs {
			_ = d.Match(in)
		}
	}
}

func BenchmarkDetectors_OnOrdinaryValue(b *testing.B) {
	// Verification reads every column of every table, so the cost per value is
	// what decides whether a 20 gigabyte scan finishes in minutes or hours.
	ds := verify.Detectors()
	const value = "Gwen Larkspur, 482 Hazel Terrace, Ashford, order 8814-2291-0034"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, d := range ds {
			if d.Match(value) {
				b.Fatal("unexpected match")
			}
		}
	}
}

// A column nobody could read is not a column that passed.
//
// That sentence was written twice in scan.go, once above the Skipped field and
// once above the append that fills it, and it was implemented nowhere: Clean()
// read only Findings, so a scan that failed to read a column reported itself
// clean and the golden was published. It is the same shape as a function with
// no callers. The guarantee was stated, believed, and absent.
//
// The consequence is specific rather than theoretical. env/golden.go refuses to
// publish on !report.Clean(), so an unreadable column was the one way a golden
// could pass verification without anybody having verified it, and the
// attestation it carries says how many columns were read.
func TestReport_AColumnThatCouldNotBeReadIsNotClean(t *testing.T) {
	t.Parallel()

	// No findings at all. Under the old Clean() this was indistinguishable
	// from a scan that read everything and found nothing.
	report := verify.Report{
		Columns: 2,
		Skipped: []string{"public.customers.notes: permission denied"},
	}
	require.False(t, report.Clean(),
		"a scan that could not read a column reported itself clean, so a golden holding "+
			"that column would be published as verified")
}

func TestReport_CleanStillMeansCleanWhenNothingWasSkipped(t *testing.T) {
	t.Parallel()

	// The other half, so the fix cannot be "always return false".
	require.True(t, verify.Report{Columns: 2, RowsSampled: 40}.Clean())
	require.False(t, verify.Report{
		Findings: []verify.Finding{{Detector: "email", Table: "customers", Column: "email"}},
	}.Clean())
}
