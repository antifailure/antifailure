package license_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/license"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type signer struct {
	keyID string
	priv  ed25519.PrivateKey
	pub   ed25519.PublicKey
}

func newSigner(t *testing.T, keyID string) signer {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	return signer{keyID: keyID, priv: priv, pub: pub}
}

// sign builds a token the way licensegen does, so the tests exercise the real
// wire form rather than a convenient one.
func (s signer) sign(t *testing.T, claims license.Claims) string {
	t.Helper()
	if claims.KeyID == "" {
		claims.KeyID = s.keyID
	}
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	return fmt.Sprintf("aflic_%s.%s",
		base64.RawURLEncoding.EncodeToString(payload),
		base64.RawURLEncoding.EncodeToString(ed25519.Sign(s.priv, payload)))
}

func validClaims() license.Claims {
	return license.Claims{
		ID:        "lic-1",
		Org:       "acme",
		Plan:      "enterprise",
		Features:  []license.Feature{license.FeatureSSO, license.FeatureSCIM},
		Seats:     50,
		IssuedAt:  epoch,
		ExpiresAt: epoch.AddDate(1, 0, 0),
	}
}

// ---------------------------------------------------------------------------
// Parsing
// ---------------------------------------------------------------------------

func TestParse_AcceptsAKeyItSigned(t *testing.T) {
	t.Parallel()
	s := newSigner(t, "2026-01")
	v := license.NewVerifier(map[string]ed25519.PublicKey{s.keyID: s.pub})

	claims, err := v.Parse(s.sign(t, validClaims()))
	require.NoError(t, err)
	require.Equal(t, "acme", claims.Org)
	require.Equal(t, 50, claims.Seats)
	require.Contains(t, claims.Features, license.FeatureSSO)
}

func TestParse_ToleratesWhitespaceFromAPastedKey(t *testing.T) {
	t.Parallel()
	// A license arrives in an email and gets pasted with a line break in it.
	// Refusing that is a support ticket, not a security property.
	s := newSigner(t, "k")
	v := license.NewVerifier(map[string]ed25519.PublicKey{s.keyID: s.pub})
	token := s.sign(t, validClaims())

	middle := len(token) / 2
	wrapped := "  " + token[:middle] + "\n   " + token[middle:] + "\n"
	_, err := v.Parse(wrapped)
	require.NoError(t, err)
}

func TestParse_RefusesATamperedPayload(t *testing.T) {
	t.Parallel()
	s := newSigner(t, "k")
	v := license.NewVerifier(map[string]ed25519.PublicKey{s.keyID: s.pub})

	// Every field somebody would want to change: more seats, a later expiry, a
	// different organization, an extra feature.
	for name, edit := range map[string]func(*license.Claims){
		"more seats":       func(c *license.Claims) { c.Seats = 100000 },
		"a later expiry":   func(c *license.Claims) { c.ExpiresAt = epoch.AddDate(50, 0, 0) },
		"another org":      func(c *license.Claims) { c.Org = "someone-else" },
		"an extra feature": func(c *license.Claims) { c.Features = license.AllFeatures() },
	} {
		t.Run(name, func(t *testing.T) {
			honest := validClaims()
			token := s.sign(t, honest)

			forged := validClaims()
			forged.KeyID = s.keyID
			edit(&forged)
			// The key id is set before marshalling, not after. Setting it after
			// leaves the forged payload naming no key, and the parse is then
			// refused for an unknown key rather than a bad signature, which
			// would pass this test while proving nothing about the signature.
			payload, err := json.Marshal(forged)
			require.NoError(t, err)

			// The original signature over the edited payload, which is what an
			// attacker with a real license can construct.
			parts := strings.SplitN(strings.TrimPrefix(token, "aflic_"), ".", 2)
			tampered := "aflic_" + base64.RawURLEncoding.EncodeToString(payload) + "." + parts[1]

			_, err = v.Parse(tampered)
			require.ErrorIs(t, err, license.ErrTampered)
		})
	}
}

func TestParse_RefusesAKeyItDoesNotKnow(t *testing.T) {
	t.Parallel()
	mine := newSigner(t, "mine")
	theirs := newSigner(t, "theirs")
	v := license.NewVerifier(map[string]ed25519.PublicKey{mine.keyID: mine.pub})

	_, err := v.Parse(theirs.sign(t, validClaims()))
	require.ErrorIs(t, err, license.ErrUnknownKey)
}

func TestParse_AcceptsAnOldKeyAfterRotation(t *testing.T) {
	t.Parallel()
	// A build that trusts only the newest key cannot verify any license already
	// in the field, which turns a key rotation into an outage for every
	// customer at once.
	old := newSigner(t, "2025-01")
	current := newSigner(t, "2026-01")
	v := license.NewVerifier(map[string]ed25519.PublicKey{
		old.keyID: old.pub, current.keyID: current.pub,
	})

	for _, s := range []signer{old, current} {
		_, err := v.Parse(s.sign(t, validClaims()))
		require.NoErrorf(t, err, "key %s", s.keyID)
	}
}

func TestParse_RefusesMalformedInputWithoutPanicking(t *testing.T) {
	t.Parallel()
	s := newSigner(t, "k")
	v := license.NewVerifier(map[string]ed25519.PublicKey{s.keyID: s.pub})

	for _, token := range []string{
		"", "   ", "not-a-license", "aflic_", "aflic_.", "aflic_a", "aflic_a.",
		"aflic_.b", "aflic_!!!.???", "aflic_" + strings.Repeat("a", 10) + ".short",
		// A payload that decodes but is not JSON.
		"aflic_" + base64.RawURLEncoding.EncodeToString([]byte("hello")) + "." +
			base64.RawURLEncoding.EncodeToString(make([]byte, ed25519.SignatureSize)),
	} {
		_, err := v.Parse(token)
		require.Errorf(t, err, "token %q was accepted", token)
	}
}

func TestParse_RefusesClaimsMissingWhatMakesALicenseMeanAnything(t *testing.T) {
	t.Parallel()
	s := newSigner(t, "k")
	v := license.NewVerifier(map[string]ed25519.PublicKey{s.keyID: s.pub})

	noOrg := validClaims()
	noOrg.Org = ""
	_, err := v.Parse(s.sign(t, noOrg))
	// A license naming no organization would work everywhere.
	require.ErrorIs(t, err, license.ErrMalformed)

	noExpiry := validClaims()
	noExpiry.ExpiresAt = time.Time{}
	_, err = v.Parse(s.sign(t, noExpiry))
	// One with no expiry never ends.
	require.ErrorIs(t, err, license.ErrMalformed)
}

// FuzzParse is the parser's real test. It reads attacker-controlled input, and
// the only property that matters is that no input makes it panic or accept
// something it did not sign.
func FuzzParse(f *testing.F) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		f.Fatal(err)
	}
	v := license.NewVerifier(map[string]ed25519.PublicKey{"k": pub})

	payload, _ := json.Marshal(license.Claims{
		ID: "l", Org: "acme", ExpiresAt: epoch, KeyID: "k",
	})
	good := fmt.Sprintf("aflic_%s.%s",
		base64.RawURLEncoding.EncodeToString(payload),
		base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, payload)))

	for _, seed := range []string{good, "", "aflic_", "aflic_a.b", "\x00", strings.Repeat("a", 4096)} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, token string) {
		claims, err := v.Parse(token)
		if err != nil {
			return
		}
		// Anything accepted must have been signed by the key, and must carry the
		// fields that make a license mean something.
		if claims.Org == "" || claims.ExpiresAt.IsZero() {
			t.Fatalf("accepted a license with no organization or no expiry: %+v", claims)
		}
		if claims.KeyID != "k" {
			t.Fatalf("accepted a license signed by %q", claims.KeyID)
		}
	})
}

// ---------------------------------------------------------------------------
// Evaluation
// ---------------------------------------------------------------------------

func evaluate(t *testing.T, claims license.Claims, ev license.Evaluation) license.Status {
	t.Helper()
	s := newSigner(t, "k")
	v := license.NewVerifier(map[string]ed25519.PublicKey{s.keyID: s.pub})
	parsed, err := v.Parse(s.sign(t, claims))
	require.NoError(t, err)
	return v.Evaluate(parsed, ev)
}

func TestEvaluate_ACurrentLicenseGrantsExactlyWhatItNames(t *testing.T) {
	t.Parallel()
	status := evaluate(t, validClaims(), license.Evaluation{Org: "acme", Now: epoch.AddDate(0, 1, 0)})

	require.Equal(t, license.StateActive, status.State)
	require.True(t, status.Honoured())
	require.True(t, status.Enabled(license.FeatureSSO))
	require.True(t, status.Enabled(license.FeatureSCIM))
	// And nothing it does not name. A license that grants everything it was not
	// sold is a license nobody needs to buy the right tier of.
	require.False(t, status.Enabled(license.FeatureBilling))
	require.False(t, status.Enabled(license.FeatureMultiRuntime))
}

func TestEvaluate_TheEntitlementMatrix(t *testing.T) {
	t.Parallel()
	// Every feature, granted and not granted, so that a feature added to the
	// list without being handled fails here.
	for _, granted := range license.AllFeatures() {
		claims := validClaims()
		claims.Features = []license.Feature{granted}
		status := evaluate(t, claims, license.Evaluation{Org: "acme", Now: epoch.AddDate(0, 1, 0)})

		for _, f := range license.AllFeatures() {
			if f == granted {
				require.Truef(t, status.Enabled(f), "a license naming %s does not grant it", f)
				continue
			}
			require.Falsef(t, status.Enabled(f), "a license naming only %s also granted %s", granted, f)
		}
	}
}

func TestEvaluate_ALicenseForAnotherOrganizationIsRefused(t *testing.T) {
	t.Parallel()
	// What stops a key from being passed between companies.
	status := evaluate(t, validClaims(), license.Evaluation{Org: "other-co", Now: epoch})

	require.Equal(t, license.StateWrongOrg, status.State)
	require.False(t, status.Honoured())
	for _, f := range license.AllFeatures() {
		require.Falsef(t, status.Enabled(f), "%s was granted to the wrong organization", f)
	}
	require.Contains(t, status.Warning, "acme")
	require.Contains(t, status.Warning, "other-co")
}

func TestEvaluate_TheOrganizationComparisonIgnoresCaseAndSurroundingSpace(t *testing.T) {
	t.Parallel()
	status := evaluate(t, validClaims(), license.Evaluation{Org: "  ACME ", Now: epoch})
	require.Equal(t, license.StateActive, status.State)
}

func TestEvaluate_ExpiryMovesThroughGraceAndThenDegrades(t *testing.T) {
	t.Parallel()
	claims := validClaims()
	claims.GraceDays = 14
	expires := claims.ExpiresAt

	for _, tc := range []struct {
		name  string
		now   time.Time
		state license.State
		sso   bool
	}{
		{"a month before", expires.AddDate(0, 0, -30), license.StateActive, true},
		{"the day before", expires.AddDate(0, 0, -1), license.StateActive, true},
		{"the day after", expires.AddDate(0, 0, 1), license.StateGrace, true},
		{"the last day of grace", expires.AddDate(0, 0, 13), license.StateGrace, true},
		{"the day grace ends", expires.AddDate(0, 0, 14), license.StateExpired, false},
		{"a year later", expires.AddDate(1, 0, 0), license.StateExpired, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := evaluate(t, claims, license.Evaluation{Org: "acme", Now: tc.now})
			require.Equal(t, tc.state, status.State)
			require.Equal(t, tc.sso, status.Enabled(license.FeatureSSO))
		})
	}
}

func TestEvaluate_AnExpiredLicenseSaysNothingIsDeleted(t *testing.T) {
	t.Parallel()
	// The behaviour that matters more than the mechanism: settings survive
	// expiry, and renewing restores them. Saying so is what stops somebody from
	// reconfiguring everything after a late renewal.
	claims := validClaims()
	status := evaluate(t, claims, license.Evaluation{
		Org: "acme", Now: claims.ExpiresAt.AddDate(0, 1, 0),
	})
	require.Equal(t, license.StateExpired, status.State)
	require.Contains(t, status.Warning, "preserved")
	require.Contains(t, status.Warning, "renewing")
}

func TestEvaluate_WarnsBeforeExpiryRatherThanAtIt(t *testing.T) {
	t.Parallel()
	claims := validClaims()
	// Thirty days out, which is enough time for a purchase order.
	status := evaluate(t, claims, license.Evaluation{
		Org: "acme", Now: claims.ExpiresAt.AddDate(0, 0, -20),
	})
	require.Equal(t, license.StateActive, status.State)
	require.Contains(t, status.Warning, "expires in 20 days")

	quiet := evaluate(t, claims, license.Evaluation{
		Org: "acme", Now: claims.ExpiresAt.AddDate(0, 0, -200),
	})
	require.Empty(t, quiet.Warning, "a license with months left should say nothing")
}

func TestEvaluate_ClockRollbackDegradesUntilTheClockCatchesUp(t *testing.T) {
	t.Parallel()
	claims := validClaims()
	lastSeen := epoch.AddDate(0, 6, 0)

	// The clock moved back six months, which is how an expired license is made
	// to look current.
	rolled := evaluate(t, claims, license.Evaluation{
		Org: "acme", Now: epoch.AddDate(0, 0, 1), LastSeen: lastSeen,
	})
	require.Equal(t, license.StateClockRollback, rolled.State)
	require.False(t, rolled.Enabled(license.FeatureSSO))
	require.Contains(t, rolled.Warning, "clock")

	// Back past the last seen time and it works again, with nothing to undo.
	recovered := evaluate(t, claims, license.Evaluation{
		Org: "acme", Now: lastSeen.Add(time.Second), LastSeen: lastSeen,
	})
	require.Equal(t, license.StateActive, recovered.State)
	require.True(t, recovered.Enabled(license.FeatureSSO))
}

func TestEvaluate_OrdinaryClockCorrectionIsNotTreatedAsRollback(t *testing.T) {
	t.Parallel()
	// Time synchronisation moves a clock by seconds and a resumed virtual
	// machine by more. Treating either as tampering would disable enterprise
	// features on a laptop that woke from sleep.
	claims := validClaims()
	lastSeen := epoch.AddDate(0, 6, 0)

	for _, back := range []time.Duration{time.Second, time.Minute, 59 * time.Minute} {
		status := evaluate(t, claims, license.Evaluation{
			Org: "acme", Now: lastSeen.Add(-back), LastSeen: lastSeen,
		})
		require.Equalf(t, license.StateActive, status.State,
			"a clock %s behind was treated as tampering", back)
	}

	// An hour and a bit is past the tolerance.
	beyond := evaluate(t, claims, license.Evaluation{
		Org: "acme", Now: lastSeen.Add(-2 * time.Hour), LastSeen: lastSeen,
	})
	require.Equal(t, license.StateClockRollback, beyond.State)
}

func TestEvaluate_TheClockCheckHappensBeforeExpiry(t *testing.T) {
	t.Parallel()
	// The order is the point. An expired license with the clock wound back looks
	// current, so checking expiry first would let exactly the attack this
	// prevents succeed.
	claims := validClaims()
	lastSeen := claims.ExpiresAt.AddDate(0, 1, 0) // already seen after expiry

	status := evaluate(t, claims, license.Evaluation{
		Org: "acme", Now: epoch.AddDate(0, 1, 0), LastSeen: lastSeen,
	})
	require.Equal(t, license.StateClockRollback, status.State)
	require.False(t, status.Enabled(license.FeatureSSO))
}

func TestEvaluate_ARevokedLicenseIsRefusedEvenWhileCurrent(t *testing.T) {
	t.Parallel()
	s := newSigner(t, "k")
	v := license.NewVerifier(map[string]ed25519.PublicKey{s.keyID: s.pub})
	claims, err := v.Parse(s.sign(t, validClaims()))
	require.NoError(t, err)

	before := v.Evaluate(claims, license.Evaluation{Org: "acme", Now: epoch})
	require.Equal(t, license.StateActive, before.State)

	v.Revoke("lic-1")
	after := v.Evaluate(claims, license.Evaluation{Org: "acme", Now: epoch})
	require.Equal(t, license.StateRevoked, after.State)
	require.False(t, after.Enabled(license.FeatureSSO))
}

func TestEvaluate_ATrialSaysSo(t *testing.T) {
	t.Parallel()
	claims := validClaims()
	claims.Trial = true
	status := evaluate(t, claims, license.Evaluation{Org: "acme", Now: epoch})
	require.Contains(t, status.Warning, "trial")
	require.True(t, status.Enabled(license.FeatureSSO))
}

// ---------------------------------------------------------------------------
// No license, which is the ordinary case
// ---------------------------------------------------------------------------

func TestNone_IsTheCommunityEditionAndNotAnError(t *testing.T) {
	t.Parallel()
	status := license.None()
	require.Equal(t, license.StateNone, status.State)
	require.False(t, status.Honoured())
	require.Empty(t, status.Warning,
		"most installations have no license, deliberately and permanently; warning them would be untrue")
	for _, f := range license.AllFeatures() {
		require.False(t, status.Enabled(f))
	}
}

func TestZeroStatus_GrantsNothing(t *testing.T) {
	t.Parallel()
	// A Status that was never evaluated must not grant anything. The direction
	// the mistake fails in is the whole design.
	var zero license.Status
	for _, f := range license.AllFeatures() {
		require.Falsef(t, zero.Enabled(f), "the zero Status granted %s", f)
	}
	require.False(t, zero.Honoured())
}

// ---------------------------------------------------------------------------
// Seats
// ---------------------------------------------------------------------------

func TestSeats_AreReportedAndNeverEnforcedByRemovingAnyone(t *testing.T) {
	t.Parallel()
	claims := validClaims()
	claims.Seats = 3
	status := evaluate(t, claims, license.Evaluation{Org: "acme", Now: epoch})

	require.False(t, status.SeatsExceeded(2), "there is room for a third")
	require.True(t, status.SeatsExceeded(3), "a fourth is past the limit")
	require.True(t, status.SeatsExceeded(99))
}

func TestSeats_ZeroMeansUnlimited(t *testing.T) {
	t.Parallel()
	claims := validClaims()
	claims.Seats = 0
	status := evaluate(t, claims, license.Evaluation{Org: "acme", Now: epoch})
	require.False(t, status.SeatsExceeded(100_000))
}

func TestSeats_AreNotEnforcedWithoutAnHonouredLicense(t *testing.T) {
	t.Parallel()
	// An expired license must not start refusing new members. Degrading to the
	// community behaviour means the community behaviour, which has no seats.
	claims := validClaims()
	claims.Seats = 1
	status := evaluate(t, claims, license.Evaluation{
		Org: "acme", Now: claims.ExpiresAt.AddDate(1, 0, 0),
	})
	require.Equal(t, license.StateExpired, status.State)
	require.False(t, status.SeatsExceeded(500))
}

// ---------------------------------------------------------------------------

func TestAllFeatures_IsSortedAndComplete(t *testing.T) {
	t.Parallel()
	all := license.AllFeatures()
	require.NotEmpty(t, all)
	for i := 1; i < len(all); i++ {
		require.Less(t, string(all[i-1]), string(all[i]), "AllFeatures must be sorted")
	}
	seen := map[license.Feature]bool{}
	for _, f := range all {
		require.False(t, seen[f], "%s appears twice", f)
		seen[f] = true
	}
}

func TestErrors_AreDistinguishable(t *testing.T) {
	t.Parallel()
	// A caller maps each to a different error code and a different next step:
	// a malformed key is a paste error, a tampered one is a security event, and
	// an unknown key means the build is older than the license.
	require.False(t, errors.Is(license.ErrMalformed, license.ErrTampered))
	require.False(t, errors.Is(license.ErrTampered, license.ErrUnknownKey))
	require.False(t, errors.Is(license.ErrUnknownKey, license.ErrMalformed))
}

// TestParse_ToleratesAFeatureThisBuildDoesNotKnow pins the permissiveness.
//
// It looks like a bug and it is load bearing. A license issued for a newer
// release names features an older binary does not carry, and a verifier that
// refused the whole license over one unknown name would take away the features
// the customer did buy every time an upgrade and a renewal crossed. The unknown
// name is carried and never permitted, and tools/licensegen is what stops one
// being signed in the first place.
func TestParse_ToleratesAFeatureThisBuildDoesNotKnow(t *testing.T) {
	s := newSigner(t, "k1")
	claims := validClaims()
	// One real feature and one from a release this build predates.
	claims.Features = []license.Feature{license.FeatureSSO, license.Feature("a_later_release")}

	v := license.NewVerifier(map[string]ed25519.PublicKey{s.keyID: s.pub})
	parsed, err := v.Parse(s.sign(t, claims))
	require.NoError(t, err, "an unknown feature must not invalidate the whole license")

	status := v.Evaluate(parsed, license.Evaluation{Org: claims.Org, Now: claims.IssuedAt})
	require.Equal(t, license.StateActive, status.State)
	require.True(t, status.Enabled(license.FeatureSSO), "the known feature is still permitted")
	// The unknown name is carried through and would answer true if anything
	// asked, which nothing does: every caller asks by constant, and this build
	// has no constant for a feature a later release introduced. Asserted rather
	// than left implied, because the harmless version of this and the dangerous
	// version look identical from the outside.
	require.True(t, status.Enabled(license.Feature("a_later_release")),
		"the unknown name is carried rather than dropped")
	for _, f := range license.AllFeatures() {
		if f == license.FeatureSSO {
			continue
		}
		require.False(t, status.Enabled(f), "no feature this build knows is granted by accident")
	}
}
