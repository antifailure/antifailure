// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

// The shared licence corpus, emitted here and consumed by the control plane.
//
// WHY THIS FILE EXISTS, AND IT IS NOT "TO ADD TEST COVERAGE".
//
// There are two implementations of one decision. This package parses and
// evaluates licences in Go and runs in the engine. `ee/web/server/src/license.ts`
// parses and evaluates them in TypeScript and runs in the control plane, because
// single sign-on and provisioning are mounted there and the control plane has no
// engine in it. Two readers of one format drift, and the one that drifts is the
// one nobody is holding against the other: a customer's engine says a feature is
// not permitted while their control plane prints that it is, and both are
// confident.
//
// THE COMMENT AT THE TOP OF license.ts SAID THIS FILE ALREADY EXISTED. It named
// the hazard, then listed three things holding the two sides together, and the
// second was "ee/license-vectors.json is a corpus of tokens with the verdict
// each one must produce. test/license.test.ts here reads it and
// ee/engine/license/vectors_test.go reads the same file." None of that was in
// the tree: not the corpus, not this file, not a single occurrence of the string
// in either suite. That is the exact shape ee/README.md records three times over
// and warns about in its own words, which is that a claim resting on an invented
// mechanism reads identically to a true one until somebody goes looking.
//
// So: this is the emitter, `ee/license-vectors.json` is the corpus, and
// `ee/web/server/test/vectors.test.ts` is the consumer. It is the same shape
// the community tree already uses three times, for the policy engine, for
// webhooks and for mock packs, and the Go side emits in all of them because the
// Go side is the definition.
//
//	go test ./license -run TestVectors -update-vectors
//
// WHAT THE CORPUS DOES NOT COMPARE, said here rather than left to be assumed
// checked. It compares the DECISIONS: the refusal, the state, the days left,
// whether the licence is honoured, which features are permitted, and whether one
// more member passes the seat limit. It does NOT compare the warning sentences,
// and they are measurably different today: this side formats an expiry date as
// "5 September 2026" and the control plane uses `toUTCString`, which is
// "Sat, 05 Sep 2026 00:00:00 GMT". Both are shown to an operator, so that is
// worth fixing, and it is a difference in prose for two different surfaces
// rather than a difference in what either binary permits. Recording it beats
// quietly widening the corpus until it passes.

package license_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/license"
)

var updateVectors = flag.Bool("update-vectors", false,
	"rewrite ee/license-vectors.json from the current implementation")

// corpusPath is where both sides read it from.
const corpusPath = "../../license-vectors.json"

// corpus is the whole file.
type corpus struct {
	// Note is addressed to whoever opens the file wondering what it is.
	Note string `json:"note"`
	// Keys is the AF_LICENSE_PUBLIC_KEYS value that trusts the signer, in the
	// kid=base64 form an operator pastes into a deployment. It is a PUBLIC key.
	// There is deliberately no signing key in this repository, in this file or
	// anywhere near it, which is why the tokens below are fixtures rather than
	// something either side mints while it runs.
	Keys string `json:"keys"`
	// AllFeatures and NotShipped bind two lists that were duplicated across the
	// two languages with nothing checking them. license.ts declares its own
	// ALL_FEATURES and says it matches this one; nothing made that true.
	AllFeatures []string `json:"all_features"`
	NotShipped  []string `json:"not_shipped"`
	Cases       []vcase  `json:"cases"`
}

// vcase is one token, one moment, and the answer both implementations owe.
type vcase struct {
	Name string `json:"name"`
	// Why this case is in the corpus, so that deleting it is a decision rather
	// than a tidy-up.
	Why string `json:"why"`

	// The input.
	Token    string   `json:"token"`
	Org      string   `json:"org"`
	Now      string   `json:"now"`
	LastSeen string   `json:"last_seen,omitempty"`
	Revoked  []string `json:"revoked,omitempty"`

	// The answer. Refusal is empty when the token parses.
	Refusal  string      `json:"refusal"`
	State    string      `json:"state,omitempty"`
	DaysLeft int         `json:"days_left"`
	Honoured bool        `json:"honoured"`
	Enabled  []string    `json:"enabled"`
	Seats    []seatProbe `json:"seats,omitempty"`
}

// seatProbe is the seat limit asked as a number rather than through a
// provisioning flow, because that is where the two sides could differ by one
// and nothing would say so until a customer's twenty sixth member was refused
// or allowed.
type seatProbe struct {
	Current  int  `json:"current"`
	Exceeded bool `json:"exceeded"`
}

// spec is how a case is minted the first time. Once a case has a token in the
// file, the token is the fixture and this is not consulted again: regenerating
// recomputes the VERDICTS and leaves the tokens alone, so a semantic change
// produces a diff of verdicts rather than a diff of the whole file.
//
// Changing what a case's licence says therefore means deleting that case's
// token from the corpus, so that it is minted again. That is deliberate. A
// corpus whose fixtures move under the verdicts is a corpus that cannot show
// you what changed.
type spec struct {
	name     string
	why      string
	claims   license.Claims
	org      string
	now      time.Time
	lastSeen time.Time
	revoked  []string
	// mint builds the token from the signed one, for the cases whose whole
	// point is that the token is not what the signer produced. Nil means the
	// signed token is used as it is.
	mint  func(signed string) string
	seats []int
}

var vectorEpoch = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

func specs() []spec {
	base := func() license.Claims {
		return license.Claims{
			ID:        "lic-corpus",
			Org:       "acme",
			Plan:      "enterprise",
			Features:  []license.Feature{license.FeatureSSO, license.FeatureSCIM},
			Seats:     25,
			IssuedAt:  vectorEpoch,
			ExpiresAt: vectorEpoch.AddDate(1, 0, 0),
		}
	}
	with := func(f func(c *license.Claims)) license.Claims {
		c := base()
		f(&c)
		return c
	}

	return []spec{
		{
			name:   "current license, two features, inside the term",
			why:    "The ordinary case. If the two sides disagree here nothing else in the corpus matters.",
			claims: base(),
			org:    "acme",
			now:    vectorEpoch.AddDate(0, 1, 0),
			seats:  []int{0, 24, 25, 26},
		},
		{
			name:   "the organization is compared without case or surrounding space",
			why:    "An operator pastes a slug with a trailing space or a capital. Refusing that is a support ticket, and one side trimming while the other did not would refuse a licence that is fine.",
			claims: with(func(c *license.Claims) { c.Org = "  ACME " }),
			org:    "acme",
			now:    vectorEpoch.AddDate(0, 1, 0),
		},
		{
			name:   "a license issued to somebody else",
			why:    "This is what stops a key being passed around, so both binaries have to refuse it and neither may permit a feature while doing so.",
			claims: base(),
			org:    "globex",
			now:    vectorEpoch.AddDate(0, 1, 0),
		},
		{
			name:   "the instant of expiry is not still active",
			why:    "An off by one at the boundary is the most likely place two implementations differ, and it is invisible for a year.",
			claims: base(),
			org:    "acme",
			now:    vectorEpoch.AddDate(1, 0, 0),
		},
		{
			name:   "expired, inside the grace period",
			why:    "Expiry is an ordinary commercial event and the software keeps working. A side that fell straight to expired would switch a paying customer off two weeks early.",
			claims: base(),
			org:    "acme",
			now:    vectorEpoch.AddDate(1, 0, 3),
		},
		{
			name:   "the instant the grace period ends is expired",
			why:    "The other boundary, for the same reason as the first.",
			claims: base(),
			org:    "acme",
			now:    vectorEpoch.AddDate(1, 0, license.DefaultGraceDays),
		},
		{
			name:   "expired, past the grace period, permits nothing",
			why:    "The state a lapsed customer is actually in. Settings are preserved and features are off, and a side that still permitted them would be giving the product away.",
			claims: base(),
			org:    "acme",
			now:    vectorEpoch.AddDate(1, 1, 0),
			seats:  []int{0, 26},
		},
		{
			name:   "a license that carries its own grace period",
			why:    "grace_days is optional and zero means the default. A side reading zero as no grace would cut a customer off on the day.",
			claims: with(func(c *license.Claims) { c.GraceDays = 45 }),
			org:    "acme",
			now:    vectorEpoch.AddDate(1, 0, 30),
		},
		{
			name:    "revoked",
			why:     "Revocation is consulted from a list the operator supplies and there is no fetch. It has to beat everything else, including a licence that is otherwise current.",
			claims:  base(),
			org:     "acme",
			now:     vectorEpoch.AddDate(0, 1, 0),
			revoked: []string{"lic-corpus"},
		},
		{
			name:     "the clock was rolled back past the tolerance",
			why:      "A rolled back clock makes an expired licence look current, which is exactly what moving it is for. The check has to come before expiry on both sides.",
			claims:   base(),
			org:      "acme",
			now:      vectorEpoch.AddDate(0, 1, 0),
			lastSeen: vectorEpoch.AddDate(0, 2, 0),
		},
		{
			name:     "a clock inside the hour of tolerance is not a rollback",
			why:      "Ordinary time synchronisation moves a clock by seconds and a resumed virtual machine by more. A side with no tolerance would switch enterprise features off on an ntp step.",
			claims:   base(),
			org:      "acme",
			now:      vectorEpoch.AddDate(0, 1, 0),
			lastSeen: vectorEpoch.AddDate(0, 1, 0).Add(30 * time.Minute),
		},
		{
			name:   "a license naming a feature this build does not ship",
			why:    "THE DIVERGENCE THIS CORPUS WAS WRITTEN AFTER FINDING. The engine filters a feature it enforces nowhere and the control plane did not, so one binary said billing was permitted and the other said it was not, for the same key, in the same deployment.",
			claims: with(func(c *license.Claims) { c.Features = append(c.Features, license.FeatureBilling) }),
			org:    "acme",
			now:    vectorEpoch.AddDate(0, 1, 0),
		},
		{
			name:   "a license naming a feature from a later release",
			why:    "The feature set is closed at issue time, not at read time. Refusing the whole licence over one unknown name would turn every ordering of upgrade and renewal into an outage for the features the customer did buy.",
			claims: with(func(c *license.Claims) { c.Features = append(c.Features, "teleportation") }),
			org:    "acme",
			now:    vectorEpoch.AddDate(0, 1, 0),
		},
		{
			name:   "unlimited seats is zero, not nobody",
			why:    "Zero reading as a limit of none would lock every customer on an unmetered licence out of their own directory.",
			claims: with(func(c *license.Claims) { c.Seats = 0 }),
			org:    "acme",
			now:    vectorEpoch.AddDate(0, 1, 0),
			seats:  []int{0, 1000},
		},
		{
			name:   "a trial license is still a license",
			why:    "The trial flag changes what an operator is told and must not change what is permitted.",
			claims: with(func(c *license.Claims) { c.Trial = true }),
			org:    "acme",
			now:    vectorEpoch.AddDate(0, 1, 0),
		},
		{
			name:   "signed by a key this installation does not trust",
			why:    "Distinct from tampered, because the two send an operator to two different places: one to ask for a licence signed by a current key, the other to suspect the file.",
			claims: base(),
			org:    "acme",
			now:    vectorEpoch.AddDate(0, 1, 0),
			mint: func(signed string) string {
				// Signed by a key nobody trusts, minted here rather than
				// derived from the good token, so the signature is genuine and
				// only the key is unknown.
				return signWithAnUntrustedKey()
			},
		},
		{
			name:   "the payload was edited after signing",
			why:    "The one property the wire format exists to have.",
			claims: base(),
			org:    "acme",
			now:    vectorEpoch.AddDate(0, 1, 0),
			mint:   tamperWithThePayload,
		},
		{
			name:   "not a license at all",
			why:    "Somebody pastes the wrong thing into a deployment. Both sides refuse as malformed rather than as tampered, because tampered sends a reader looking for an attacker.",
			claims: base(),
			org:    "acme",
			now:    vectorEpoch.AddDate(0, 1, 0),
			mint:   func(string) string { return "not-a-license" },
		},
		{
			name:   "the prefix is right and the rest is not base64url",
			why:    "A truncated or re-encoded paste. Node's base64url decoder discards rubbish rather than refusing it, so this is the case where a round trip check on one side and none on the other would produce two different verdicts.",
			claims: base(),
			org:    "acme",
			now:    vectorEpoch.AddDate(0, 1, 0),
			mint:   func(string) string { return "aflic_not!base64.also!not" },
		},
		{
			name:   "one part, no dot",
			why:    "The shape of a licence cut at a line break in an email.",
			claims: base(),
			org:    "acme",
			now:    vectorEpoch.AddDate(0, 1, 0),
			mint: func(signed string) string {
				for i := 0; i < len(signed); i++ {
					if signed[i] == '.' {
						return signed[:i]
					}
				}
				return signed
			},
		},
		{
			name:   "whitespace and newlines survive the paste",
			why:    "A licence copied out of an email arrives wrapped. Refusing that is a support ticket rather than a security property, and both sides have to be equally forgiving.",
			claims: base(),
			org:    "acme",
			now:    vectorEpoch.AddDate(0, 1, 0),
			mint: func(signed string) string {
				mid := len(signed) / 2
				return "  " + signed[:mid] + "\n  " + signed[mid:] + "\n"
			},
		},
	}
}

// The signer for the corpus. Minted once, when a case has no token yet, and
// never written to disk in its private half.
var (
	corpusKeyID = "corpus-1"
	corpusPub   ed25519.PublicKey
	corpusPriv  ed25519.PrivateKey
	untrustedID = "not-trusted"
	untrustedPr ed25519.PrivateKey
)

func signWithAnUntrustedKey() string {
	claims := license.Claims{
		ID: "lic-corpus", Org: "acme", Plan: "enterprise",
		Features:  []license.Feature{license.FeatureSSO},
		IssuedAt:  vectorEpoch,
		ExpiresAt: vectorEpoch.AddDate(1, 0, 0),
		KeyID:     untrustedID,
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("aflic_%s.%s",
		base64.RawURLEncoding.EncodeToString(payload),
		base64.RawURLEncoding.EncodeToString(ed25519.Sign(untrustedPr, payload)))
}

// tamperWithThePayload flips one byte of the encoded payload, leaving the
// signature alone, which is the shape of an edited licence rather than of a
// corrupted one.
func tamperWithThePayload(signed string) string {
	body := signed[len("aflic_"):]
	dot := -1
	for i := 0; i < len(body); i++ {
		if body[i] == '.' {
			dot = i
			break
		}
	}
	payload, err := base64.RawURLEncoding.DecodeString(body[:dot])
	if err != nil {
		panic(err)
	}
	edited := make([]byte, len(payload))
	copy(edited, payload)
	// "acme" becomes "acmf" wherever it appears, which changes the organization
	// the licence names without changing its length.
	for i := 0; i+4 <= len(edited); i++ {
		if string(edited[i:i+4]) == "acme" {
			edited[i+3] = 'f'
			break
		}
	}
	return "aflic_" + base64.RawURLEncoding.EncodeToString(edited) + "." + body[dot+1:]
}

func mintSigner(t *testing.T) {
	t.Helper()
	if corpusPriv != nil {
		return
	}
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	corpusPub, corpusPriv = pub, priv
	_, upriv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	untrustedPr = upriv
}

func signClaims(t *testing.T, claims license.Claims) string {
	t.Helper()
	if claims.KeyID == "" {
		claims.KeyID = corpusKeyID
	}
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	return fmt.Sprintf("aflic_%s.%s",
		base64.RawURLEncoding.EncodeToString(payload),
		base64.RawURLEncoding.EncodeToString(ed25519.Sign(corpusPriv, payload)))
}

// verifierFor builds a verifier the way a deployment does, through
// LoadVerifier and the environment, rather than by handing NewVerifier a map.
// The corpus publishes its key in the AF_LICENSE_PUBLIC_KEYS form on purpose,
// so this exercises the reader an operator's paste actually goes through.
func verifierFor(t *testing.T, keys string, revoked []string) *license.Verifier {
	t.Helper()
	v, count, err := license.LoadVerifier(func(name string) string {
		if name == license.TrustedKeysEnv {
			return keys
		}
		return ""
	})
	require.NoError(t, err, "the corpus publishes a keys value this build cannot read")
	require.Positive(t, count, "the corpus published a key and this build trusts none")
	v.Revoke(revoked...)
	return v
}

// answer runs the current implementation and returns the verdict fields.
func answer(t *testing.T, c vcase, keys string) vcase {
	t.Helper()
	out := c
	v := verifierFor(t, keys, c.Revoked)

	now, err := time.Parse(time.RFC3339, c.Now)
	require.NoError(t, err)
	var lastSeen time.Time
	if c.LastSeen != "" {
		lastSeen, err = time.Parse(time.RFC3339, c.LastSeen)
		require.NoError(t, err)
	}

	claims, err := v.Parse(c.Token)
	if err != nil {
		out.Refusal = refusalOf(err)
		out.State = ""
		out.DaysLeft = 0
		out.Honoured = false
		out.Enabled = []string{}
		out.Seats = nil
		return out
	}

	status := v.Evaluate(claims, license.Evaluation{Org: c.Org, Now: now, LastSeen: lastSeen})
	out.Refusal = ""
	out.State = string(status.State)
	out.DaysLeft = status.DaysLeft
	out.Honoured = status.Honoured()
	out.Enabled = []string{}
	for _, f := range license.AllFeatures() {
		if status.Enabled(f) {
			out.Enabled = append(out.Enabled, string(f))
		}
	}
	sort.Strings(out.Enabled)

	// A NEW slice, and the reason is a defect this file had until the mutation
	// matrix found it. `out := c` copies the struct and copies the slice HEADER,
	// so out.Seats and c.Seats share one backing array. Writing the computed
	// answer into out.Seats[i] therefore overwrote the expected answer in
	// c.Seats[i], and the comparison in TestVectors was a slice against itself.
	// Breaking SeatsExceeded by one in the implementation left the suite green,
	// which is the whole definition of an assertion that cannot say no.
	out.Seats = nil
	for _, probe := range c.Seats {
		out.Seats = append(out.Seats, seatProbe{
			Current:  probe.Current,
			Exceeded: status.SeatsExceeded(probe.Current),
		})
	}
	return out
}

// refusalOf names which of the three refusals an error is, because the three
// send an operator to three different places and a corpus that recorded only
// "refused" would let the two sides disagree about which one.
func refusalOf(err error) string {
	switch {
	case errors.Is(err, license.ErrUnknownKey):
		return "unknown_key"
	case errors.Is(err, license.ErrTampered):
		return "tampered"
	case errors.Is(err, license.ErrMalformed):
		return "malformed"
	default:
		return "unclassified:" + err.Error()
	}
}

const corpusNote = "The licence corpus, shared by the Go reader in ee/engine/license " +
	"and the TypeScript reader in ee/web/server/src/license.ts. Both must produce these " +
	"verdicts. Emitted by ee/engine/license/vectors_test.go and read by " +
	"ee/web/server/test/vectors.test.ts. Regenerate with " +
	"'GOWORK=off go test ./license -run TestVectors -update-vectors' from ee/engine. " +
	"The tokens are fixtures: regenerating recomputes the verdicts and leaves them alone, " +
	"so a semantic change shows as a diff of verdicts. To change what a case's licence " +
	"says, delete that case's token and regenerate. There is no signing key in this " +
	"repository; 'keys' is the public half, in the form AF_LICENSE_PUBLIC_KEYS takes."

func TestVectors(t *testing.T) {
	stored := readCorpus(t)

	if *updateVectors {
		writeCorpus(t, buildCorpus(t, stored))
		return
	}

	require.NotEmpty(t, stored.Cases,
		"found no cases in %s, so this check is looking in the wrong place or the corpus was "+
			"emptied. That is a failure, not a pass.", corpusPath)
	require.NotEmpty(t, stored.Keys, "the corpus publishes no keys, so nothing in it can verify")

	require.Equal(t, featureNames(license.AllFeatures()), stored.AllFeatures,
		"the corpus and this build disagree about what features exist")
	require.Equal(t, featureNames(license.NotShippedFeatures()), stored.NotShipped,
		"the corpus and this build disagree about which features are not shipped")

	for _, c := range stored.Cases {
		t.Run(c.Name, func(t *testing.T) {
			got := answer(t, c, stored.Keys)
			require.Equal(t, c.Refusal, got.Refusal, "refusal, for: %s", c.Why)
			require.Equal(t, c.State, got.State, "state, for: %s", c.Why)
			require.Equal(t, c.DaysLeft, got.DaysLeft, "days left, for: %s", c.Why)
			require.Equal(t, c.Honoured, got.Honoured, "honoured, for: %s", c.Why)
			require.Equal(t, c.Enabled, got.Enabled, "permitted features, for: %s", c.Why)
			require.Equal(t, c.Seats, got.Seats, "the seat limit, for: %s", c.Why)
		})
	}
}

// TestVectorsCoverEveryRefusalAndEveryState is the check on the corpus rather
// than on the implementation.
//
// A corpus is only worth what it covers, and a corpus that quietly stopped
// carrying a state would pass for ever while the two sides drifted on exactly
// the case it dropped. So the states and refusals this package can produce are
// enumerated from the package's own constants, and every one has to appear.
func TestVectorsCoverEveryRefusalAndEveryState(t *testing.T) {
	stored := readCorpus(t)
	require.NotEmpty(t, stored.Cases, "no cases, so this check has nothing to look at")

	seenState := map[string]bool{}
	seenRefusal := map[string]bool{}
	for _, c := range stored.Cases {
		if c.Refusal != "" {
			seenRefusal[c.Refusal] = true
			continue
		}
		seenState[c.State] = true
	}

	// StateNone is deliberately absent: it is the answer to having no token at
	// all, and a corpus of tokens cannot carry the case of there being none.
	// Both sides test it directly, and saying so here is cheaper than a reader
	// wondering whether it was forgotten.
	for _, want := range []string{"active", "grace", "expired", "revoked", "wrong_org", "clock_rollback"} {
		require.True(t, seenState[want], "no case in the corpus produces the state %q", want)
	}
	for _, want := range []string{"malformed", "tampered", "unknown_key"} {
		require.True(t, seenRefusal[want], "no case in the corpus produces the refusal %q", want)
	}
}

func featureNames(fs []license.Feature) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, string(f))
	}
	sort.Strings(out)
	return out
}

func readCorpus(t *testing.T) corpus {
	t.Helper()
	body, err := os.ReadFile(filepath.Clean(corpusPath))
	if os.IsNotExist(err) {
		if *updateVectors {
			return corpus{}
		}
		t.Fatalf("%s does not exist. Regenerate it with -update-vectors.", corpusPath)
	}
	require.NoError(t, err)
	var c corpus
	require.NoError(t, json.Unmarshal(body, &c), "%s is not readable as a corpus", corpusPath)
	return c
}

// buildCorpus keeps every token the file already carries and recomputes every
// verdict.
func buildCorpus(t *testing.T, stored corpus) corpus {
	t.Helper()

	tokens := map[string]string{}
	for _, c := range stored.Cases {
		if c.Token != "" {
			tokens[c.Name] = c.Token
		}
	}

	out := corpus{
		Note:        corpusNote,
		Keys:        stored.Keys,
		AllFeatures: featureNames(license.AllFeatures()),
		NotShipped:  featureNames(license.NotShippedFeatures()),
	}

	needsMinting := false
	for _, s := range specs() {
		if tokens[s.name] == "" {
			needsMinting = true
		}
	}
	if needsMinting {
		require.Empty(t, stored.Keys,
			"a case has no token and the corpus already publishes a key, so minting one now would "+
				"invalidate every other token. Delete %s and regenerate the whole file.", corpusPath)
		mintSigner(t)
		out.Keys = corpusKeyID + "=" + base64.StdEncoding.EncodeToString(corpusPub)
	}

	for _, s := range specs() {
		c := vcase{Name: s.name, Why: s.why, Org: s.org, Now: s.now.UTC().Format(time.RFC3339)}
		if !s.lastSeen.IsZero() {
			c.LastSeen = s.lastSeen.UTC().Format(time.RFC3339)
		}
		c.Revoked = s.revoked
		if token, ok := tokens[s.name]; ok {
			c.Token = token
		} else {
			signed := signClaims(t, s.claims)
			if s.mint != nil {
				signed = s.mint(signed)
			}
			c.Token = signed
		}
		for _, current := range s.seats {
			c.Seats = append(c.Seats, seatProbe{Current: current})
		}
		out.Cases = append(out.Cases, answer(t, c, out.Keys))
	}
	return out
}

func writeCorpus(t *testing.T, c corpus) {
	t.Helper()
	body, err := json.MarshalIndent(c, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Clean(corpusPath), append(body, '\n'), 0o600))
	t.Logf("wrote %d cases to %s", len(c.Cases), corpusPath)
}
