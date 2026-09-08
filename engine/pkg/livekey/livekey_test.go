package livekey_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/livekey"
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

// The alphabets the two new clouds need. Azure emits plain base64 and so does
// the body of a PEM block, and Microsoft documents a wider set again for an
// Entra client secret. They are cycled rather than random so that a failing
// case is the same case tomorrow.
const (
	base64s = base62 + "+/"
	entra   = base62 + "-._~"
)

// pemHeader and pemFooter are assembled rather than written out, for the same
// reason the fake credentials above are: this file is scanned by the check that
// imports this package, and a header sitting next to a long base64 body and the
// word gserviceaccount is precisely what that check exists to refuse.
const (
	pemHeader = "-----BEGIN " + "PRIVATE KEY-----"
	pemFooter = "-----END " + "PRIVATE KEY-----"
)

// serviceAccountFile is the credential Google issues, in the form it is written
// to disk: JSON, with the PEM line breaks escaped rather than literal. The
// escaped break is the reason the pattern needed a skip at all, so the on disk
// form is the one most of these vectors use.
func serviceAccountFile(body string) string {
	return `{"type":"service_account","project_id":"af-example",` +
		`"private_key_id":"` + fake("", 40, hexes) + `",` +
		`"private_key":"` + pemHeader + `\n` + body + `\n` + pemFooter + `\n",` +
		`"client_email":"af-example@af-example.iam.gserviceaccount.com",` +
		`"token_uri":"https://oauth2.googleapis.com/token"}`
}

// serviceAccountParsed is the same credential after a JSON decoder has been
// over it, where the line breaks are real. Both forms reach a proxy: one as an
// uploaded file, one as a field a program already decoded.
func serviceAccountParsed(body string) string {
	return "{\"private_key\": \"" + pemHeader + "\n" + body + "\n" + pemFooter + "\n\", " +
		"\"client_email\": \"af-example@af-example.iam.gserviceaccount.com\"}"
}

// storageConnection is the connection string the Azure storage SDKs read. The
// account key is 86 characters of base64 and two of padding.
func storageConnection(account, key string) string {
	return "DefaultEndpointsProtocol=https;AccountName=" + account +
		";AccountKey=" + key + "==;EndpointSuffix=core.windows.net"
}

// busConnection is the shape Service Bus, Event Hubs, IoT Hub and Relay all
// share. SharedAccessKeyName sits immediately before SharedAccessKey and is a
// deliberate part of the vector: a detector that matched the name field would
// report the word RootManageSharedAccessKey as a credential.
func busConnection(key string) string {
	return "Endpoint=sb://af-example.servicebus.windows.net/;" +
		"SharedAccessKeyName=RootManageSharedAccessKey;SharedAccessKey=" + key + "="
}

// liveVector is one credential that must be refused, and who it belongs to.
type liveVector struct {
	name     string
	provider string
	text     string
}

// liveCloudVectors are the credential forms this lane exists to recognise.
//
// Every one of them is synthetic and assembled at run time, and every one is
// shaped the way the provider actually emits it: the right length, the right
// alphabet, and carried in the field or the header that really carries it. A
// detector proved only against a string no real credential resembles has not
// been proved at all.
func liveCloudVectors() []liveVector {
	pemBody := fake("", 64, base64s)
	return []liveVector{
		{"google api key, bare", "Google API key", fake("AI"+"za", 35, base62)},
		{"google api key, in a url", "Google API key",
			"https://maps.googleapis.com/maps/api/geocode/json?key=" + fake("AI"+"za", 35, base62)},
		{"google access token, bare", "Google OAuth access token", fake("ya"+"29"+".", 60, base62)},
		{"google access token, in a header", "Google OAuth access token",
			"authorization: Bearer " + fake("ya"+"29"+".", 120, base62)},
		{"gcp service account key, on disk", "GCP service account key", serviceAccountFile(pemBody)},
		{"gcp service account key, decoded", "GCP service account key", serviceAccountParsed(pemBody)},
		{"gcp service account key, wrapped over lines", "GCP service account key",
			serviceAccountParsed(fake("", 64, base64s) + "\n" + fake("", 64, base64s))},
		// The marker ahead of the key rather than behind it. A JSON object has
		// no guaranteed field order once a program has been over it, so a
		// lookaround that only searched forward would miss this one.
		{"gcp service account key, marker first", "GCP service account key",
			"{\"client_email\": \"af@af.iam.gserviceaccount.com\", \"private_key\": \"" +
				pemHeader + "\n" + pemBody + "\n" + pemFooter + "\"}"},

		{"azure storage key, connection string", "Azure storage account key",
			storageConnection("afexample", fake("", 86, base64s))},
		{"azure storage key, bare assignment", "Azure storage account key",
			"AccountKey=" + fake("", 86, base64s) + "=="},
		{"azure shared access key, service bus", "Azure shared access key",
			busConnection(fake("", 43, base64s))},
		{"azure shared access key, event hubs", "Azure shared access key",
			"Endpoint=sb://af-example.servicebus.windows.net/;EntityPath=events;" +
				"SharedAccessKeyName=send;SharedAccessKey=" + fake("", 43, base64s) + "="},
		{"azure client secret, connection string", "Azure client secret",
			"Authority=https://login.microsoftonline.com/af;AppSecret=" + fake("", 40, entra)},
		{"azure client secret, environment", "Azure client secret",
			"AZURE_CLIENT_SECRET=" + fake("", 40, entra)},
	}
}

// benignVectors must every one of them produce nothing.
//
// This half is the harder half and it is why the lane's number has two sides. A
// detector that refuses everything is indistinguishable from a detector that
// works until the day somebody counts, and it is switched off the first time it
// blocks a deploy over a sentence in a README.
func benignVectors() []struct{ name, text string } {
	type v = struct{ name, text string }
	return []v{
		// Prose. Somebody writing documentation about the credential.
		{"prose, google api key prefix", "every Google API key starts with AIza and runs to 39 characters"},
		{"prose, google token prefix", "a ya29. token expires after an hour and is refreshed, not stored"},
		{"prose, account key field", "set AccountKey= to the value the portal shows under Access keys"},
		{"prose, shared access key field", "SharedAccessKey= is the last field in a Service Bus connection string"},
		{"prose, client secret variable", "the SDKs read AZURE_CLIENT_SECRET= from the environment"},
		{"prose, pem header alone", "the file opens with " + pemHeader + " and we never log past it"},

		// Documentation that shows the shape with the value taken out. These
		// are the strings a repository actually contains, in a README, a
		// sample env file, or a terraform variable description.
		{"redacted, angle bracket placeholder", "AccountKey=<your-storage-account-key>;EndpointSuffix=core.windows.net"},
		{"redacted, the word redacted", "AccountKey=REDACTED;AccountName=afexample"},
		{"redacted, ellipsis", "SharedAccessKey=..." },
		{"redacted, env sample", "AZURE_CLIENT_SECRET=\nAZURE_TENANT_ID=\n"},
		{"redacted, google key placeholder", "key=AIza<YOUR_KEY_HERE>"},
		// The reason only LEADING characters are skipped. Left to run, the gap
		// would let the words of a sentence accumulate into sixty characters
		// of key material that is not there.
		{"redacted, body replaced by prose",
			`{"private_key": "` + pemHeader +
				` the body of this key was removed before the file was committed to the repository",` +
				` "client_email": "af@af.iam.gserviceaccount.com"}`},
		{"redacted, service account body removed",
			`{"private_key":"` + pemHeader + `\nREDACTED\n` + pemFooter +
				`\n","client_email":"af@af.iam.gserviceaccount.com"}`},

		// One character short of every threshold. If the detector cannot say
		// no here it is counting nothing.
		{"one short, storage account key", "AccountKey=" + fake("", 85, base64s) + "=="},
		{"one short, shared access key", "SharedAccessKey=" + fake("", 39, base64s) + "="},
		{"one short, google api key", "key=" + fake("AI"+"za", 34, base62)},
		{"one short, client secret", "AZURE_CLIENT_SECRET=" + fake("", 29, entra)},
		{"one short, service account body",
			"{\"private_key\": \"" + pemHeader + "\n" + fake("", 59, base64s) + "\n" + pemFooter +
				"\", \"client_email\": \"af@af.iam.gserviceaccount.com\"}"},

		// The published emulator credential. Azurite's development key is live
		// SHAPED and is exactly what a sandbox is supposed to hold.
		{"azurite, by account name", storageConnection("devstoreaccount1", fake("", 86, base64s))},
		{"azurite, by shorthand", "UseDevelopmentStorage=true;AccountKey=" + fake("", 86, base64s) + "=="},
		// Field order in a connection string is not fixed either, so the
		// excusing marker has to be found on both sides of the key.
		{"azurite, account name after the key",
			"AccountKey=" + fake("", 86, base64s) + "==;AccountName=devstoreaccount1"},

		// A private key that is not Google's. Naming the wrong provider sends
		// somebody to rotate a credential that has nothing to do with the
		// finding, so a PEM with no marker beside it is not a GCP finding.
		{"tls private key, no marker", pemHeader + "\n" + fake("", 64, base64s) + "\n" + pemFooter},
		{"tls private key, in a kubernetes secret",
			"tls.key: |\n  " + pemHeader + "\n  " + fake("", 64, base64s) + "\n  " + pemFooter},
		{"gcp marker too far from the key", pemHeader + "\n" + fake("", 64, base64s) + "\n" + pemFooter +
			strings.Repeat(" ", 5000) + "af@af.iam.gserviceaccount.com"},

		// Another provider's secret under another provider's name. Every OAuth
		// service in existence spells it client_secret, and a finding that
		// said Azure about a GitHub app would be worse than no finding.
		{"generic oauth client secret", "client_secret=" + fake("", 40, entra)},
		{"github app secret", "GITHUB_CLIENT_SECRET=" + fake("", 40, hexes)},

		// Credential adjacent strings that are not credentials.
		{"jwt", "eyJhbGciOiJSUzI1NiJ9." + fake("", 60, base62) + "." + fake("", 43, base62)},
		{"base64 payload", "body=" + fake("", 200, base64s) + "=="},
		{"uuid", "tenant=" + fake("", 8, hexes) + "-" + fake("", 4, hexes) + "-4" + fake("", 3, hexes)},
		{"azure resource id",
			"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/af/providers/Microsoft.Storage/storageAccounts/afexample"},
		{"google endpoint variable", "STORAGE_EMULATOR_HOST=localhost:9199"},
	}
}

func TestScan_FindsGoogleAndAzureCredentials(t *testing.T) {
	t.Parallel()
	// The two largest clouds after AWS, and the two the detector could not see.
	// Each vector is the shape the provider really emits, so a pass here is a
	// statement about real credentials rather than about the test's own strings.
	for _, v := range liveCloudVectors() {
		t.Run(v.name, func(t *testing.T) {
			found := livekey.Scan(v.text, "the body")
			require.Len(t, found, 1, "%s was not recognised, or was recognised twice", v.name)
			require.Equal(t, v.provider, found[0].Provider)
			require.Equal(t, "the body", found[0].Where)
		})
	}
}

func TestScan_RefusesNothingItShouldNot(t *testing.T) {
	t.Parallel()
	// The other half of the number. Each of these is a string a repository or a
	// request really carries, and every one of them must produce nothing.
	for _, v := range benignVectors() {
		t.Run(v.name, func(t *testing.T) {
			require.Empty(t, livekey.Scan(v.text, "the body"),
				"%s was refused and is not a live credential", v.name)
		})
	}
}

func TestScan_TheAzuriteKeyIsWhatASandboxIsSupposedToHold(t *testing.T) {
	t.Parallel()
	// The live versus test rule applied to a provider that draws no distinction
	// in the credential itself. The same 86 characters are a finding under a
	// real account name and not one under Azurite's, and the account name is
	// the only thing that separates them.
	key := fake("", 86, base64s)
	require.Empty(t, livekey.Scan(storageConnection("devstoreaccount1", key), "the body"),
		"the emulator's own key is what an environment is for")
	found := livekey.Scan(storageConnection("afproduction", key), "the body")
	require.Len(t, found, 1, "the same key under a real account name is a finding")
	require.Equal(t, "Azure storage account key", found[0].Provider)
}

func TestScan_WillNotNameGoogleForSomebodyElsesPrivateKey(t *testing.T) {
	t.Parallel()
	// A bare PEM is a private key and it is not evidence of a cloud. Reporting
	// it as a GCP service account key would send somebody to rotate a
	// credential their Google project does not have.
	body := fake("", 64, base64s)
	bare := pemHeader + "\n" + body + "\n" + pemFooter
	require.Empty(t, livekey.Scan(bare, "the body"))
	found := livekey.Scan(bare+"\nclient_email: af@af.iam.gserviceaccount.com", "the body")
	require.Len(t, found, 1, "the same key with Google's own marker beside it is a finding")
	require.Equal(t, "GCP service account key", found[0].Provider)
}

func TestScan_MeasuresThePemBodyThroughTheLineBreak(t *testing.T) {
	t.Parallel()
	// The reason the pattern has a skip. Without it the tail after the header
	// is a line break, the count is zero, and the most valuable credential
	// Google issues is the one shape that never matches. Both spellings of the
	// break have to work, because both reach a proxy.
	body := fake("", 64, base64s)
	require.Len(t, livekey.Scan(serviceAccountFile(body), "the body"), 1, "escaped break")
	require.Len(t, livekey.Scan(serviceAccountParsed(body), "the body"), 1, "literal break")
}

func TestScan_NeverEchoesACloudCredential(t *testing.T) {
	t.Parallel()
	// The Azure shapes have no prefix of their own, so the finding reports the
	// field name that carried them. That is still a marker and it is still not
	// the value, and this is the assertion that keeps it that way.
	for _, v := range liveCloudVectors() {
		t.Run(v.name, func(t *testing.T) {
			found := livekey.Scan(v.text, "the body")
			require.Len(t, found, 1)
			rendered := found[0].String() + " " + livekey.Describe(found)
			// Every run of thirty characters in the vector, which is longer
			// than any field name and shorter than any of these credentials.
			for i := 0; i+30 <= len(v.text); i++ {
				require.NotContains(t, rendered, v.text[i:i+30],
					"the finding carried a piece of the credential")
			}
		})
	}
}

// TestScan_TheNumber is the lane's measurement, and it prints it.
//
// Two counts, because one of them alone is worthless. A detector that refuses
// everything scores perfectly on the first and is switched off within a day;
// one that refuses nothing scores perfectly on the second and protects nobody.
// Run it with -v and the last line is the number.
func TestScan_TheNumber(t *testing.T) {
	t.Parallel()
	live := liveCloudVectors()
	benign := benignVectors()

	detected := 0
	for _, v := range live {
		found := livekey.Scan(v.text, "the body")
		if len(found) == 1 && found[0].Provider == v.provider {
			detected++
		}
	}
	positives := 0
	for _, v := range benign {
		if len(livekey.Scan(v.text, "the body")) > 0 {
			positives++
		}
	}

	t.Logf("livekey: %d/%d live GCP and Azure credential forms refused, "+
		"%d false positives over %d synthetic non credentials",
		detected, len(live), positives, len(benign))
	require.Equal(t, len(live), detected)
	require.Zero(t, positives)
}
