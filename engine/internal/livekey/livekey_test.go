package livekey_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/livekey"
)

// The credentials below are assembled at runtime rather than written out, so
// that no string in this repository looks like a key to a scanner, a push
// protection rule, or a person skimming the file.
func fake(prefix string, n int, alphabet string) string {
	var b strings.Builder
	b.WriteString(prefix)
	for i := 0; i < n; i++ {
		b.WriteByte(alphabet[i%len(alphabet)])
	}
	return b.String()
}

const (
	base62 = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	hexes  = "0123456789abcdef"
	caps   = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

func TestScan_FindsLiveCredentials(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"Stripe secret key":         fake("sk"+"_"+"live"+"_", 24, base62),
		"Stripe restricted key":     fake("rk"+"_"+"live"+"_", 24, base62),
		"GitHub personal token":     fake("ghp"+"_", 36, base62),
		"AWS access key":            fake("AKIA", 16, caps),
		"Slack bot token":           fake("xoxb"+"-", 30, base62),
		"SendGrid key":              fake("SG"+".", 40, base62),
		"Anthropic key":             fake("sk-ant-api", 40, base62),
		"Supabase service key":      fake("sbp"+"_", 40, hexes),
		"npm token":                 fake("npm"+"_", 36, base62),
		"GitHub fine grained token": fake("github"+"_"+"pat"+"_", 40, base62),
	}
	for provider, credential := range cases {
		t.Run(provider, func(t *testing.T) {
			found := livekey.Scan("authorization: Bearer "+credential, "the body")
			require.Len(t, found, 1, "%s was not recognised", provider)
			require.Equal(t, provider, found[0].Provider)
			require.Equal(t, "the body", found[0].Where)
		})
	}
}

func TestScan_LeavesTestCredentialsAlone(t *testing.T) {
	t.Parallel()
	// The distinction the whole thing rests on. A detector that refused test
	// keys would refuse every sandbox request and be turned off within a day.
	for _, credential := range []string{
		fake("sk"+"_"+"test"+"_", 24, base62),
		fake("pk"+"_"+"test"+"_", 24, base62),
		fake("rk"+"_"+"test"+"_", 24, base62),
		"POSTMARK_API_TEST",
	} {
		require.Empty(t, livekey.Scan("key="+credential, "the body"),
			"a test credential is exactly what an environment is supposed to carry: %s", credential[:10])
	}
}

func TestScan_DoesNotMatchProse(t *testing.T) {
	t.Parallel()
	// Refusing a request because somebody wrote the word "akia" in a comment
	// would make this the first thing a user turns off.
	for _, text := range []string{
		"we rotated the akia keys last week",
		"see the ghp_ prefix in the docs",
		"AC is the account prefix",
		"sk_live_ is what production uses",
		"xoxb- tokens are Slack's",
	} {
		require.Empty(t, livekey.Scan(text, "the body"), "matched prose: %q", text)
	}
}

func TestScan_ReportsEachPrefixOnce(t *testing.T) {
	t.Parallel()
	// A body carrying the same kind of key twice is one problem, not two, and
	// a message listing it twice reads like a bug in the detector.
	a := fake("sk"+"_"+"live"+"_", 24, base62)
	b := fake("sk"+"_"+"live"+"_", 30, base62)
	require.Len(t, livekey.Scan(a+" "+b, "the body"), 1)
}

func TestScan_NeverEchoesTheCredential(t *testing.T) {
	t.Parallel()
	// A refusal that quoted the key back would write it into the logs of the
	// thing refusing it, which is the one place it definitely should not be.
	credential := fake("sk"+"_"+"live"+"_", 24, base62)
	found := livekey.Scan("authorization: Bearer "+credential, "the body")
	require.Len(t, found, 1)
	rendered := found[0].String() + " " + livekey.Describe(found)
	require.NotContains(t, rendered, credential)
	require.NotContains(t, rendered, credential[len("sk_live_"):])
}

func TestScanHeaders_NamesTheHeader(t *testing.T) {
	t.Parallel()
	// Naming the header is the difference between fixing it in a minute and
	// hunting for it.
	found := livekey.ScanHeaders(map[string][]string{
		"Authorization": {"Bearer " + fake("sk"+"_"+"live"+"_", 24, base62)},
		"Content-Type":  {"application/json"},
	})
	require.Len(t, found, 1)
	require.Contains(t, found[0].Where, "Authorization")
	require.Empty(t, livekey.ScanHeaders(nil))
}

func TestScan_IsCaseSensitive(t *testing.T) {
	t.Parallel()
	// Every prefix is emitted in a fixed case by the provider that issues it,
	// and folding case turns "AC" into a match for "ac" in any URL.
	require.Empty(t, livekey.Scan("SK"+"_"+"LIVE"+"_"+strings.Repeat("A", 24), "the body"))
	require.Empty(t, livekey.Scan("akia"+strings.Repeat("B", 16), "the body"))
}

func TestDescribe_ReadsAsASentenceFragment(t *testing.T) {
	t.Parallel()
	found := livekey.Scan("Bearer "+fake("sk"+"_"+"live"+"_", 24, base62), "the Authorization header")
	require.Equal(t, "Stripe secret key (sk_live_) in the Authorization header", livekey.Describe(found))
	require.Empty(t, livekey.Describe(nil))
}
