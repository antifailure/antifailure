// Package license parses and verifies enterprise license keys.
//
// Not MIT. This directory is covered by the Antifailure Enterprise License; see
// ee/LICENSE.md.
//
// The design follows from one decision: verification is offline. A license is a
// signed statement that this organization bought these entitlements until this
// date, and checking it is checking a signature. No network call, no phone
// home, nothing that can fail at three in the morning because a licensing
// service is down. That rules out the usual mechanisms and forces the awkward
// parts to be handled honestly.
//
// Expiry, honestly. A license that has run out does not stop the software. It
// enters a grace period with a daily warning, and after that the enterprise
// features degrade to the community behaviour while every enterprise setting
// stays on disk, so renewing restores them exactly. Nobody's preview
// environments stop working because a purchase order was slow.
//
// Clock rollback, because offline verification is only as good as the clock. A
// machine whose clock moves backwards past a time this license has already been
// seen at is either broken or being used to extend an expired license, and both
// cases get the same answer: features degrade until the clock catches up. The
// last-seen time is kept by the caller, so the check works whether that is a
// file on a laptop or a row in the control plane's database.
//
// Key rotation, because the signing key will change. Several public keys are
// embedded and any of them verifies, so a key introduced today does not
// invalidate licenses signed with yesterday's.
package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Feature is one thing a license may permit.
//
// A closed set rather than free-form strings, and the set is closed AT ISSUE
// TIME rather than here. This comment used to say the opposite, that a typo in
// an issued license is caught when the license is parsed, and Parse has never
// done that: `"features": ["ssoo"]` verifies, reports the license active, and
// permits nothing, with no error at either end.
//
// Parse is right to be permissive and the fix belongs where it now is. A
// license issued for a newer release names features an older binary has never
// heard of, and refusing the whole license over one unknown name would turn
// every ordering of upgrade and renewal into an outage for features the
// customer did buy. So the verifier tolerates a name it does not know and
// simply never permits it, and tools/licensegen refuses to sign one, which is
// the only place the set can be closed without that cost.
type Feature string

const (
	FeatureSSO           Feature = "sso"
	FeatureSCIM          Feature = "scim"
	FeatureRBAC          Feature = "rbac"
	FeatureAuditStream   Feature = "audit_stream"
	FeaturePolicy        Feature = "policy_enforcement"
	FeatureMultiRuntime  Feature = "multi_runtime"
	FeatureSecrets       Feature = "enterprise_secrets"
	FeatureBilling       Feature = "billing"
	FeatureDashboard     Feature = "enterprise_dashboard"
	FeatureSupportAccess Feature = "support_access"
	FeatureCompliance    Feature = "compliance_packs"
	FeatureAirGapped     Feature = "air_gapped"
)

// AllFeatures is every feature a license can carry, sorted, for the comparison
// page and for the entitlement matrix test.
func AllFeatures() []Feature {
	out := []Feature{
		FeatureSSO, FeatureSCIM, FeatureRBAC, FeatureAuditStream, FeaturePolicy,
		FeatureMultiRuntime, FeatureSecrets, FeatureBilling, FeatureDashboard,
		FeatureSupportAccess, FeatureCompliance, FeatureAirGapped,
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Claims are what a license asserts. This is the signed payload.
type Claims struct {
	// ID identifies this license, for the revocation list.
	ID string `json:"id"`
	// Org is the organization slug the license was issued to. A license for one
	// organization must not work for another, which is what stops a key from
	// being passed around.
	Org string `json:"org"`
	// Plan names the tier, for display.
	Plan string `json:"plan"`
	// Features are what it permits.
	Features []Feature `json:"features"`
	// Seats is the member limit. Zero means unlimited.
	Seats int `json:"seats"`
	// IssuedAt is when it was signed.
	IssuedAt time.Time `json:"issued_at"`
	// ExpiresAt is when it stops being current.
	ExpiresAt time.Time `json:"expires_at"`
	// GraceDays is how long after expiry the features keep working, with
	// warnings. Zero uses DefaultGraceDays.
	GraceDays int `json:"grace_days,omitempty"`
	// Trial marks an evaluation license, which shows a banner and cannot be
	// followed by a second trial.
	Trial bool `json:"trial,omitempty"`
	// KeyID names the signing key, so verification does not have to try them
	// all and so a rotation is auditable.
	KeyID string `json:"kid"`
}

// DefaultGraceDays is how long an expired license keeps working.
//
// Two weeks, because the thing that expires a license is usually a purchase
// order moving slowly through somebody else's company, and two weeks is long
// enough to fix that without being long enough to ignore.
const DefaultGraceDays = 14

// State is what a license is doing right now.
type State string

const (
	// StateActive is a current license.
	StateActive State = "active"
	// StateGrace is expired but still honoured, with warnings.
	StateGrace State = "grace"
	// StateExpired is past its grace period. Features degrade to community
	// behaviour and every setting is preserved.
	StateExpired State = "expired"
	// StateRevoked is a license on the revocation list.
	StateRevoked State = "revoked"
	// StateClockRollback is a machine whose clock moved backwards past a time
	// this license was already seen at.
	StateClockRollback State = "clock_rollback"
	// StateWrongOrg is a valid license belonging to somebody else.
	StateWrongOrg State = "wrong_org"
	// StateNone is no license at all, which is the community edition and is not
	// an error.
	StateNone State = "none"
)

// Status is the evaluated state of a license.
type Status struct {
	State  State
	Claims Claims
	// DaysLeft counts down to expiry when active, and to the end of grace when
	// in grace. Negative once expired.
	DaysLeft int
	// Warning is what to print, empty when there is nothing to say.
	Warning string
	// features is the effective set, empty unless the license is honoured.
	features map[Feature]bool
}

// Enabled reports whether a feature is permitted right now.
//
// The only question the rest of the code asks. Everything above is in service
// of this returning false safely: an expired license, a revoked one, a
// rolled-back clock, and no license at all all answer the same way, because the
// caller's job is to fall back to the community behaviour and it should not
// have to know which of those happened.
func (s Status) Enabled(f Feature) bool { return s.features[f] }

// Honoured reports whether the license is being acted on.
func (s Status) Honoured() bool { return s.State == StateActive || s.State == StateGrace }

// Errors a caller distinguishes. Each maps to an error code in the catalog.
var (
	// ErrMalformed is a token that is not a license at all.
	ErrMalformed = errors.New("the license key is not in the expected form")
	// ErrTampered is a token whose signature does not verify.
	ErrTampered = errors.New("the license key's signature does not verify")
	// ErrUnknownKey is a token signed by a key this build does not carry.
	ErrUnknownKey = errors.New("the license key was signed by a key this build does not know")
)

// Verifier holds the public keys a build trusts.
type Verifier struct {
	keys map[string]ed25519.PublicKey
	// revoked is the set of license identifiers that have been withdrawn. Only
	// consulted when a revocation list has been loaded, which requires the
	// operator to have opted into online checks.
	revoked map[string]bool
}

// NewVerifier builds a verifier from named public keys.
//
// Several, always. A build that trusts one key cannot rotate without
// invalidating every license in the field, so the shape is plural from the
// start even when there is one.
func NewVerifier(keys map[string]ed25519.PublicKey) *Verifier {
	copied := make(map[string]ed25519.PublicKey, len(keys))
	for id, k := range keys {
		copied[id] = k
	}
	return &Verifier{keys: copied, revoked: map[string]bool{}}
}

// Revoke marks a license identifier as withdrawn.
func (v *Verifier) Revoke(ids ...string) {
	for _, id := range ids {
		v.revoked[id] = true
	}
}

// Parse verifies a token's signature and returns its claims.
//
// It does not evaluate expiry, the organization, or the clock. Those depend on
// the moment and on the caller's state, and mixing them in would mean a
// signature check that can fail for reasons that have nothing to do with the
// signature.
//
// The wire form is two base64url segments separated by a dot: the claims and
// the signature over them. Deliberately not a JWT. A JWT carries its own
// algorithm field, and the first thing anybody does with an algorithm field is
// forget to pin it, at which point "none" is a valid signature.
func (v *Verifier) Parse(token string) (Claims, error) {
	var zero Claims
	token = strings.TrimSpace(token)
	// Whitespace and newlines come from a license pasted out of an email, and
	// refusing that is a support ticket rather than a security property.
	token = strings.Join(strings.Fields(token), "")
	if token == "" {
		return zero, ErrMalformed
	}

	const prefix = "aflic_"
	if !strings.HasPrefix(token, prefix) {
		return zero, fmt.Errorf("%w: it should start with %s", ErrMalformed, prefix)
	}
	body := token[len(prefix):]

	dot := strings.IndexByte(body, '.')
	if dot <= 0 || dot == len(body)-1 {
		return zero, fmt.Errorf("%w: it should be two parts separated by a dot", ErrMalformed)
	}

	payload, err := base64.RawURLEncoding.DecodeString(body[:dot])
	if err != nil {
		return zero, fmt.Errorf("%w: the first part is not base64url", ErrMalformed)
	}
	signature, err := base64.RawURLEncoding.DecodeString(body[dot+1:])
	if err != nil {
		return zero, fmt.Errorf("%w: the second part is not base64url", ErrMalformed)
	}
	if len(signature) != ed25519.SignatureSize {
		return zero, fmt.Errorf("%w: the signature is the wrong length", ErrTampered)
	}

	// Decoded before the signature is checked, only to read the key identifier.
	// Nothing from this is trusted or returned unless the signature verifies
	// afterwards, and the claims returned are the ones parsed here from bytes
	// that have been verified.
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return zero, fmt.Errorf("%w: the payload is not JSON", ErrMalformed)
	}

	key, ok := v.keys[claims.KeyID]
	if !ok {
		return zero, fmt.Errorf("%w: key %q", ErrUnknownKey, claims.KeyID)
	}
	if !ed25519.Verify(key, payload, signature) {
		return zero, ErrTampered
	}

	if claims.Org == "" {
		return zero, fmt.Errorf("%w: it names no organization", ErrMalformed)
	}
	if claims.ExpiresAt.IsZero() {
		return zero, fmt.Errorf("%w: it has no expiry", ErrMalformed)
	}
	return claims, nil
}

// Evaluation is the state a caller supplies alongside the moment.
type Evaluation struct {
	// Org is the organization the software is running as. A license issued to
	// another one is refused.
	Org string
	// Now is the current time, from the injected clock.
	Now time.Time
	// LastSeen is the latest time this license was previously evaluated at,
	// persisted by the caller. Zero means it has never been seen.
	//
	// Kept by the caller rather than here because where it lives differs: a row
	// in the control plane's database for a hosted organization, a file for a
	// self-hosted one. What matters is only that it is monotonic.
	LastSeen time.Time
}

// Evaluate turns claims into a status at a moment.
func (v *Verifier) Evaluate(claims Claims, ev Evaluation) Status {
	status := Status{Claims: claims, features: map[Feature]bool{}}

	if v.revoked[claims.ID] {
		status.State = StateRevoked
		status.Warning = "This license has been revoked. Ask about it at https://antifailure.dev/contact."
		return status
	}

	if !strings.EqualFold(strings.TrimSpace(claims.Org), strings.TrimSpace(ev.Org)) {
		status.State = StateWrongOrg
		status.Warning = fmt.Sprintf(
			"This license was issued to %s and this installation is %s.", claims.Org, ev.Org)
		return status
	}

	// The clock check comes before expiry, because a rolled-back clock makes an
	// expired license look current and that is exactly what somebody moving the
	// clock is trying to achieve. An hour of tolerance, because ordinary time
	// synchronisation moves a clock by seconds and a virtual machine resuming
	// from a snapshot can move it by more.
	const rollbackTolerance = time.Hour
	if !ev.LastSeen.IsZero() && ev.Now.Add(rollbackTolerance).Before(ev.LastSeen) {
		status.State = StateClockRollback
		status.Warning = fmt.Sprintf(
			"This machine's clock reads %s, which is earlier than the %s this license was last checked at. "+
				"Enterprise features stay off until the clock passes that time.",
			ev.Now.UTC().Format(time.RFC3339), ev.LastSeen.UTC().Format(time.RFC3339))
		return status
	}

	graceDays := claims.GraceDays
	if graceDays <= 0 {
		graceDays = DefaultGraceDays
	}
	graceEnds := claims.ExpiresAt.AddDate(0, 0, graceDays)

	switch {
	case ev.Now.Before(claims.ExpiresAt):
		status.State = StateActive
		status.DaysLeft = daysBetween(ev.Now, claims.ExpiresAt)
		if status.DaysLeft <= 30 {
			status.Warning = fmt.Sprintf("This license expires in %d days.", status.DaysLeft)
		}
	case ev.Now.Before(graceEnds):
		status.State = StateGrace
		status.DaysLeft = daysBetween(ev.Now, graceEnds)
		status.Warning = fmt.Sprintf(
			"This license expired on %s. Enterprise features keep working for %d more days, "+
				"then fall back to the community behaviour. Nothing is deleted and renewing restores them.",
			claims.ExpiresAt.UTC().Format("2 January 2006"), status.DaysLeft)
	default:
		status.State = StateExpired
		status.DaysLeft = -daysBetween(graceEnds, ev.Now)
		status.Warning = fmt.Sprintf(
			"This license expired on %s and its grace period has ended. Enterprise features are off "+
				"and every enterprise setting is preserved; renewing turns them back on unchanged.",
			claims.ExpiresAt.UTC().Format("2 January 2006"))
		return status
	}

	if claims.Trial {
		status.Warning = strings.TrimSpace("This is a trial license. " + status.Warning)
	}

	for _, f := range claims.Features {
		status.features[f] = true
	}
	return status
}

// daysBetween rounds up, so "expires in 0 days" never appears for a license
// that is still valid for another few hours.
func daysBetween(from, to time.Time) int {
	d := to.Sub(from)
	if d <= 0 {
		return 0
	}
	days := int(d / (24 * time.Hour))
	if d%(24*time.Hour) != 0 {
		days++
	}
	return days
}

// None is the status of an installation with no license: the community edition.
//
// Not an error and not a warning. Most installations are this, deliberately and
// permanently, and telling them something is wrong would be both untrue and
// obnoxious.
func None() Status {
	return Status{State: StateNone, features: map[Feature]bool{}}
}

// SeatsExceeded reports whether adding one more member would pass the limit.
//
// Reported rather than enforced here, and the caller refuses the new member
// rather than removing an existing one. Automatically removing somebody's
// colleagues because a renewal is late is not a behaviour any product should
// have.
func (s Status) SeatsExceeded(current int) bool {
	if !s.Honoured() || s.Claims.Seats <= 0 {
		return false
	}
	return current >= s.Claims.Seats
}
