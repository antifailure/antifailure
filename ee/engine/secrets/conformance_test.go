package secrets

// The suite, proved able to fail.
//
// A conformance suite nobody has watched fail is a suite that might be
// asserting nothing, and it is worse than no suite because it reads as
// coverage. So every behaviour in Run is broken here on purpose, one at a time,
// and this file asserts that breaking it is reported.
//
// In-package rather than in secrets_test, because the suite reaches the
// backend through the Source to count refreshes and the point of that reach is
// that adapters do not have to expose it.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// recorder is a T that remembers rather than failing.
type recorder struct {
	mu     sync.Mutex
	errors []string
	logs   []string
}

func (r *recorder) Helper() {}
func (r *recorder) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}
func (r *recorder) Logf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
}
func (r *recorder) said(substring string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.errors {
		if strings.Contains(e, substring) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------

// fake is a backend whose every behaviour can be broken on purpose.
type fake struct {
	describe string
	reach    error
	values   map[string]string

	// missIsAnError makes a name it does not hold produce a failure, which is
	// the mistake that turns one added source into nineteen broken variables.
	missIsAnError bool
	// rejectAlways refuses the credential no matter how often it is renewed.
	rejectAlways bool
	// fetchErr is what a lookup fails with. Set alongside reach for a store
	// that is genuinely down, because a real one fails both: reachability is a
	// probe and a lookup is the same connection.
	fetchErr error

	refreshes int
	mu        sync.Mutex
}

func (f *fake) Describe() string { return f.describe }

func (f *fake) Reach(context.Context) error { return f.reach }

func (f *fake) Fetch(_ context.Context, name string) (string, bool, error) {
	if f.fetchErr != nil {
		return "", false, f.fetchErr
	}
	if f.rejectAlways {
		return "", false, wrap(ErrRejected, "403 permission denied")
	}
	value, ok := f.values[name]
	if !ok {
		if f.missIsAnError {
			return "", false, fmt.Errorf("no such secret %q", name)
		}
		return "", false, nil
	}
	return value, true, nil
}

// refreshingFake adds a renewable credential. Two types rather than a flag,
// because Refresher is satisfied by a method set and a field cannot remove a
// method.
type refreshingFake struct{ *fake }

func (f *refreshingFake) Refresh(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshes++
	return nil
}

func (f *refreshingFake) Refreshes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refreshes
}

// good builds a harness that passes everything, and hands back the working
// backend so a test can break exactly one thing about it.
func good() (Harness, *fake) {
	working := &fake{
		describe: "A Fake Store at fake.internal (secret/antifailure)",
		values:   map[string]string{"PRESENT": "a-value", "EMPTY": ""},
	}
	rejecting := &refreshingFake{fake: &fake{
		describe: "A Fake Store at fake.internal (rejecting)", rejectAlways: true,
	}}
	down := errors.New("cannot be reached: dial tcp: connection refused")
	unreachable := &fake{
		describe: "A Fake Store at nothing.invalid",
		reach:    down,
		fetchErr: down,
	}
	return Harness{
		Name:         "fake",
		Working:      New(working),
		Present:      "PRESENT",
		PresentValue: "a-value",
		Empty:        "EMPTY",
		Absent:       "NOT_THERE",
		Rejecting:    New(rejecting),
		Refreshes:    rejecting.Refreshes,
		Unreachable:  New(unreachable),
	}, working
}

func TestTheSuitePassesABackendThatKeepsTheContract(t *testing.T) {
	rec := &recorder{}
	h, _ := good()
	result := Run(context.Background(), rec, h)

	require.Empty(t, rec.errors, "a conforming backend failed: %v", rec.errors)
	require.Empty(t, result.Failed)
	require.NotEmpty(t, result.Passed)
	// Nothing skipped, because this harness supplies every state. A suite where
	// most behaviours skip on the happy path is a suite that is not running.
	require.Empty(t, result.Skipped, "the fake harness supplies every state, so nothing should skip")
}

// Each of these breaks exactly one promise and asserts the suite notices. If a
// case here ever passes, the corresponding behaviour has stopped asserting.

func TestTheSuiteCatchesAMissReportedAsAFailure(t *testing.T) {
	h, working := good()
	working.missIsAnError = true

	rec := &recorder{}
	result := Run(context.Background(), rec, h)
	require.Contains(t, result.Failed, "reports a name it does not hold as a miss, not a failure")
	require.True(t, rec.said("rather than a miss"))
}

func TestTheSuiteCatchesAnUnavailableSourceWithNoReason(t *testing.T) {
	h, _ := good()
	// Unreachable and silent about it, which is the failure that turns "the
	// token expired at 09:14" into "not present".
	//
	// A raw source rather than one built with New, because Source refuses to
	// produce an empty reason: a backend that fails and says nothing gets
	// "unavailable" rather than "". That is a property worth having and it
	// means the only way to reach this behaviour is from outside Source, which
	// is exactly what a customer's own adapter is.
	h.Unreachable = &silent{name: "A Silent Store"}

	rec := &recorder{}
	result := Run(context.Background(), rec, h)
	require.Contains(t, result.Failed, "is unavailable with a reason when the store cannot be reached")
	require.True(t, rec.said("gave no reason"))
}

func TestTheSuiteCatchesAnUnreachableStoreThatAnswersMisses(t *testing.T) {
	h, _ := good()
	// Reachability fails, but a lookup quietly returns "not found" instead of
	// an error. The chain then falls through to nothing and the user is told
	// the variable is unset.
	h.Unreachable = New(&fake{
		describe: "A Lying Store", reach: errors.New("cannot be reached"), values: map[string]string{},
	})

	rec := &recorder{}
	result := Run(context.Background(), rec, h)
	require.Contains(t, result.Failed, "an unreachable store is never mistaken for a miss")
	require.True(t, rec.said("reported a miss rather than a failure"))
}

func TestTheSuiteCatchesARefusalReportedAsAMiss(t *testing.T) {
	h, _ := good()
	// A store that refuses the credential and says "not found". The chain then
	// hands the application whatever a lower priority source holds, which is a
	// different secret than yesterday with nothing said about it.
	h.Rejecting = New(&fake{describe: "A Quiet Refuser", values: map[string]string{}})
	h.Refreshes = nil

	rec := &recorder{}
	result := Run(context.Background(), rec, h)
	require.Contains(t, result.Failed, "reports a refused credential as refused, naming itself")
	require.True(t, rec.said("reported as a miss"))
}

func TestTheSuiteCatchesARefreshableCredentialThatIsNeverRenewed(t *testing.T) {
	h, _ := good()
	// Declared renewable and refusing, and the backend has no Refresh, so
	// nothing is ever renewed and a token that merely expired is reported as
	// revoked. The counter is supplied and stays at zero, which is exactly the
	// shape an adapter that forgot to implement Refresher would have.
	h.Rejecting = New(&fake{describe: "A Store That Cannot Renew", rejectAlways: true})
	h.Refreshes = func() int { return 0 }

	rec := &recorder{}
	result := Run(context.Background(), rec, h)
	require.Contains(t, result.Failed, "refreshes a refused credential once and no more")
	require.True(t, rec.said("never renewed"))
}

func TestTheSuiteCatchesACredentialRenewedOnEveryLookup(t *testing.T) {
	// The other direction, and the expensive one. Renewing per lookup turns
	// twenty declared variables against a revoked credential into twenty logins
	// and twenty rejections, which is how a configuration mistake becomes a
	// rate limit on the store everybody else is also using.
	h, _ := good()
	h.Rejecting = New(&fake{describe: "An Eager Renewer", rejectAlways: true})
	h.Refreshes = func() int { return 7 }

	rec := &recorder{}
	result := Run(context.Background(), rec, h)
	require.Contains(t, result.Failed, "refreshes a refused credential once and no more")
	require.True(t, rec.said("renewed 7 times"))
}

func TestTheRefreshIsOncePerProcessAcrossManyLookups(t *testing.T) {
	// The positive statement of the same rule, against the real Source rather
	// than through the suite: five refused lookups, one renewal.
	backend := &refreshingFake{fake: &fake{describe: "A Renewer", rejectAlways: true}}
	source := New(backend)
	ctx := withFeatures(context.Background(), "enterprise_secrets")

	for range 5 {
		_, found, err := source.Lookup(ctx, "ANYTHING")
		require.False(t, found)
		require.Error(t, err)
		var rejected *extension.CredentialRejectedError
		require.ErrorAs(t, err, &rejected)
		require.Equal(t, "A Renewer", rejected.Source)
	}
	require.Equal(t, 1, backend.Refreshes())
}

func TestTheSuiteCatchesAValueThatComesBackChanged(t *testing.T) {
	h, _ := good()
	h.PresentValue = "what-it-should-have-been"

	rec := &recorder{}
	result := Run(context.Background(), rec, h)
	require.Contains(t, result.Failed, "finds a value it holds")
	require.True(t, rec.said("came back changed"))
	// And the message says how many bytes, never which bytes. It is printed on
	// a terminal and goes into a support bundle.
	require.False(t, rec.said("a-value"))
	require.False(t, rec.said("what-it-should-have-been"))
}

func TestTheSuiteCatchesAnEmptyValueReportedAsAbsent(t *testing.T) {
	h, working := good()
	delete(working.values, "EMPTY")

	rec := &recorder{}
	result := Run(context.Background(), rec, h)
	require.Contains(t, result.Failed, "reports a value it holds as empty as present")
	require.True(t, rec.said("reported as absent"))
}

func TestTheSuiteCatchesABlankName(t *testing.T) {
	h, _ := good()
	h.Working = New(&fake{describe: "", values: map[string]string{"PRESENT": "a-value", "EMPTY": ""}})

	rec := &recorder{}
	result := Run(context.Background(), rec, h)
	require.Contains(t, result.Failed, "describes where it is")
	require.True(t, rec.said("Name() is empty"))
}

// ---------------------------------------------------------------------------
// The licence behaviours, which are the ones an adapter cannot get wrong
// because Source owns them. Broken here by breaking Source's own rule, so that
// a future change to the gate is caught rather than silently permitted.

func TestTheSourceIsOffWithoutALicenceAndSaysSo(t *testing.T) {
	h, _ := good()
	ok, why := h.Working.Available(context.Background())
	require.False(t, ok, "the feature is free without a licence")
	require.Contains(t, why, "licence")
	require.Contains(t, why, "enterprise_secrets",
		"the reason has to name the feature an administrator would buy")
}

func TestTheSuiteCatchesAFeatureThatIsFree(t *testing.T) {
	// A Source that answered regardless of the licence would pass every other
	// behaviour in the suite. This is the case that proves the gate is checked
	// per call rather than at registration, which is what makes a licence that
	// lapses mid-process actually turn the feature off.
	h, working := good()
	h.Working = &ungated{fake: working}

	rec := &recorder{}
	result := Run(context.Background(), rec, h)
	require.Contains(t, result.Failed, "is off with a reason when there is no licence")
	require.Contains(t, result.Failed, "is off with a reason when the licence has expired")
	require.Contains(t, result.Failed,
		"is off with a reason when the licence does not include this feature")
}

// silent is a source that cannot be used and will not say why.
type silent struct{ name string }

func (s *silent) Name() string                             { return s.name }
func (s *silent) Available(context.Context) (bool, string) { return false, "" }
func (s *silent) Lookup(context.Context, string) (string, bool, error) {
	return "", false, errors.New("unavailable")
}

// ungated is what a source would be if the licence were checked at
// registration rather than at every call: correct on the day it was plugged in
// and still answering a month after the licence lapsed. It plugs into the
// engine exactly as a real one does, which is why this is worth guarding.
type ungated struct{ *fake }

func (u *ungated) Name() string                             { return u.describe }
func (u *ungated) Available(context.Context) (bool, string) { return true, "" }
func (u *ungated) Lookup(ctx context.Context, name string) (string, bool, error) {
	return u.Fetch(ctx, name)
}
