package verify_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The provider identifier detector. A Stripe customer id is not a secret and
// it is exactly what the scan exists to catch: a value that says which real
// customer a row belongs to, the same in every environment. The credential
// detector had Stripe's secret key prefixes and not these, so a column of
// customer ids passed the scan with no findings.
func TestProviderIdentifier_FindsStripeObjectIds(t *testing.T) {
	t.Parallel()
	d := detector(t, "provider-identifier")
	for _, v := range []string{
		"cus_NffrFeUfNV2Hib",
		"sub_1MowQVLkdIwHu7ixeRlqHVzs",
		"in_1MtHbELkdIwHu7ixl4OzzPMv",
		"pm_1MqLiJLkdIwHu7ixUEgbFdYF",
		"price_1MoBy5LkdIwHu7ixZhnattbh",
		"evt_1NG8Du2eZvKYlo2CUI79vXWy",
		`{"customer":"cus_NffrFeUfNV2Hib","plan":"team"}`,
		"charged cs_test_a1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6 today",
	} {
		require.True(t, d.Match(v), "missed %q", v)
	}
}

func TestProviderIdentifier_LeavesWordsAndMaskedIdsAlone(t *testing.T) {
	t.Parallel()
	// The prefixes are ordinary syllables. in_progress and sub_category are
	// words, in_flightRequests is a word with a capital in it, and a body
	// the prefixed_id transform wrote is lowercase hex with no capital. None
	// of those is an identifier Stripe issued.
	d := detector(t, "provider-identifier")
	for _, v := range []string{
		"in_progress", "sub_category_default_value", "in_flightRequests",
		"status: in_review since yesterday", "pm_", "cus_",
		"cus_8f3a1c9e2b7d4a60",                 // what prefixed_id writes
		"sub_0123456789abcdef0123456789abcdef", // the same, at subscription length
		"login_cus_NffrFeUfNV2Hib",             // the prefix is the tail of a word
		"cus_1234567890123456",                 // digits only, no capital
	} {
		require.False(t, d.Match(v), "false positive on %q", v)
	}
}

func TestCredential_KnowsPostHogKeys(t *testing.T) {
	t.Parallel()
	d := detector(t, "credential")
	require.True(t, d.Match("phc_Uq7x9KzQ2mN4vB8cR1tY6wE3sA5dF0gH"))
	require.True(t, d.Match("phx_Uq7x9KzQ2mN4vB8cR1tY6wE3sA5dF0gH"))
}
