package verify_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/verify"
)

// What the attestation carries beyond the findings: the columns the scan
// could not read and the columns masking copied unchanged. Both are what
// "verified" did not cover, and both have to survive the signature and the
// round trip through a Docker label so that af golden list can print them.
func coverageReport() verify.Report {
	r := sampleReport()
	r.Unread = []verify.UnreadColumn{{
		Schema: "public", Table: "provider_keys", Column: "ciphertext", Type: "bytea",
		Reason: "4 of 4 sampled values are binary rather than text and could not be read",
		Ruled:  true,
	}}
	r.Unruled = []string{"public.billing_customers.stripe_customer_id", "public.runs.kind"}
	return r
}

func TestAttestation_CarriesTheUnreadAndUnruledColumns(t *testing.T) {
	t.Parallel()
	_, priv, err := verify.GenerateKey()
	require.NoError(t, err)
	a, err := verify.Sign(coverageReport(), "gv_1", "rules-abc", "gp1-abc", priv)
	require.NoError(t, err)

	body, err := json.Marshal(a)
	require.NoError(t, err)
	parsed, ok := verify.ParseAttestation(string(body))
	require.True(t, ok)
	require.True(t, parsed.Verify(), "the parsed attestation must still verify")
	require.Equal(t, []string{"public.billing_customers.stripe_customer_id", "public.runs.kind"},
		parsed.Report.Unruled)
	require.Len(t, parsed.Report.Unread, 1)
	require.Equal(t, "bytea", parsed.Report.Unread[0].Type)
	require.True(t, parsed.Report.Unread[0].Ruled)
}

func TestAttestation_AChangedUnruledListDoesNotVerify(t *testing.T) {
	t.Parallel()
	// The count is a claim about the golden, so it is signed with the rest.
	// An attestation whose unruled list could be edited down after the fact
	// would be one that says 0 about a golden made with 145.
	_, priv, err := verify.GenerateKey()
	require.NoError(t, err)
	a, err := verify.Sign(coverageReport(), "gv_1", "rules-abc", "gp1-abc", priv)
	require.NoError(t, err)

	tampered := a
	tampered.Report.Unruled = nil
	require.False(t, tampered.Verify())

	tampered = a
	tampered.Report.Unread = nil
	require.False(t, tampered.Verify())
}

func TestParseAttestation_RefusesWhatIsNotOne(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", "{}", `{"rows":0}`, "not json"} {
		_, ok := verify.ParseAttestation(raw)
		require.False(t, ok, "%q is not an attestation", raw)
	}
}

func TestKindOf_SaysHowEachTypeIsRead(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"text": "text", "character varying": "text", "jsonb": "text",
		"ARRAY": "text", "USER-DEFINED": "text", "inet": "text", "citext": "text",
		"bytea":  "bytea",
		"bigint": "structural", "uuid": "structural", "timestamp with time zone": "structural",
		"point": "unread", "int4range": "unread", "tsrange": "unread",
	}
	for typ, want := range cases {
		require.Equal(t, want, verify.KindOf(typ), typ)
	}
}

func TestSensitiveName_MatchesTheWordsTheRulesAlreadyActOn(t *testing.T) {
	t.Parallel()
	require.True(t, verify.SensitiveName("sso_connection_secrets", "sp_private_key"))
	require.True(t, verify.SensitiveName("provider_keys", "ciphertext"))
	require.True(t, verify.SensitiveName("admin_users", "password_salt"))
	require.True(t, verify.SensitiveName("admin_sessions", "token_hash"))
	require.True(t, verify.SensitiveName("vault", "Credential_Blob"))
	require.False(t, verify.SensitiveName("attachments", "body"))
	require.False(t, verify.SensitiveName("environments", "state"))
}
