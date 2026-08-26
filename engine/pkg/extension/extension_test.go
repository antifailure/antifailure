package extension_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// The whole point of this package is that with nothing registered it does
// nothing. The community edition ships that way, and these are the tests that
// say so: an empty registry has to behave exactly as if the calls were not
// there, or adding the hooks changed the product for everybody who does not use
// them.

func TestAnEmptyRegistryRefusesNothing(t *testing.T) {
	t.Parallel()
	r := extension.NewRegistry()
	require.True(t, r.Empty())
	require.NoError(t, r.CheckPolicy(context.Background(), extension.EnvironmentRequest{
		Org: "acme", Repository: "acme/app", Branch: "main",
	}))
}

func TestAnEmptyRegistryObservesNothingAndReportsNoProblems(t *testing.T) {
	t.Parallel()
	r := extension.NewRegistry()
	require.Empty(t, r.Observe(context.Background(), extension.LifecycleEvent{Kind: "up"}))
	require.Empty(t, r.Audit(context.Background(), extension.AuditEntry{Action: "x"}))
	require.Empty(t, r.Registered())
}

func TestTheDefaultRegistryIsEmpty(t *testing.T) {
	// Not parallel: it reads the package-level registry that other tests could
	// register into.
	require.True(t, extension.Default.Empty(),
		"the shipped default has something registered, so the community build is not the no-op build")
}

// ---------------------------------------------------------------------------

type refusing struct {
	name string
	err  error
	seen *extension.EnvironmentRequest
}

func (r *refusing) Name() string { return r.name }
func (r *refusing) Check(_ context.Context, req extension.EnvironmentRequest) error {
	copied := req
	r.seen = &copied
	return r.err
}

func TestAPolicyHookCanRefuseAndTheRefusalReachesTheCaller(t *testing.T) {
	t.Parallel()
	r := extension.NewRegistry()
	refusal := errors.New("organization policy requires users.email to be masked")
	r.AddPolicy(&refusing{name: "masking", err: refusal})

	err := r.CheckPolicy(context.Background(), extension.EnvironmentRequest{Org: "acme"})
	require.ErrorIs(t, err, refusal)
}

func TestTheFirstRefusalWinsAndLaterHooksDoNotRun(t *testing.T) {
	t.Parallel()
	// Reproducible refusals, and an environment violating three policies names
	// one of them rather than three, which is the right amount for somebody
	// fixing them one at a time.
	r := extension.NewRegistry()
	first := &refusing{name: "first", err: errors.New("no")}
	second := &refusing{name: "second", err: errors.New("also no")}
	r.AddPolicy(first)
	r.AddPolicy(second)

	err := r.CheckPolicy(context.Background(), extension.EnvironmentRequest{Org: "acme"})
	require.ErrorIs(t, err, first.err)
	require.Nil(t, second.seen, "a later hook ran after an earlier one refused")
}

func TestAPolicyHookSeesWhatItNeedsToDecide(t *testing.T) {
	t.Parallel()
	r := extension.NewRegistry()
	spy := &refusing{name: "spy"}
	r.AddPolicy(spy)

	req := extension.EnvironmentRequest{
		Org: "acme", Repository: "acme/app", Branch: "feature/x", EnvID: "af-1",
		EgressHosts:   []string{"api.stripe.com"},
		EgressModes:   map[string]string{"api.stripe.com": "sandbox"},
		MaskedColumns: []string{"users.email"},
		Provider:      "neon", Region: "weu",
	}
	require.NoError(t, r.CheckPolicy(context.Background(), req))
	require.Equal(t, req, *spy.seen)
}

// ---------------------------------------------------------------------------

type failingLifecycle struct{ calls int }

func (f *failingLifecycle) Name() string { return "meter" }
func (f *failingLifecycle) Observe(context.Context, extension.LifecycleEvent) error {
	f.calls++
	return errors.New("the metering pipeline is down")
}

func TestALifecycleHookThatFailsDoesNotStopTheOthers(t *testing.T) {
	t.Parallel()
	// A metering pipeline that is down must not prevent a teardown, or a billing
	// outage becomes a resource leak.
	r := extension.NewRegistry()
	first := &failingLifecycle{}
	second := &failingLifecycle{}
	r.AddLifecycle(first)
	r.AddLifecycle(second)

	problems := r.Observe(context.Background(), extension.LifecycleEvent{Kind: "down"})
	require.Len(t, problems, 2)
	require.Equal(t, 1, first.calls)
	require.Equal(t, 1, second.calls, "the second hook was skipped after the first failed")
}

type failingSink struct{ calls int }

func (f *failingSink) Name() string { return "splunk" }
func (f *failingSink) Write(context.Context, extension.AuditEntry) error {
	f.calls++
	return errors.New("the sink is unreachable")
}

func TestAnAuditSinkThatFailsIsReportedAndNotFatal(t *testing.T) {
	t.Parallel()
	// The primary audit log is written regardless of what a sink does, so a
	// sink that is down loses forwarding and never loses the entry.
	r := extension.NewRegistry()
	sink := &failingSink{}
	r.AddAuditSink(sink)

	problems := r.Audit(context.Background(), extension.AuditEntry{Action: "environment.created"})
	require.Len(t, problems, 1)
	require.Equal(t, 1, sink.calls)
}

// ---------------------------------------------------------------------------

func TestRegisteredNamesWhatIsPluggedInSorted(t *testing.T) {
	t.Parallel()
	// An operator debugging why an environment was refused needs to know a
	// policy hook exists at all, and the list has to read the same every time.
	r := extension.NewRegistry()
	r.AddPolicy(&refusing{name: "masking"})
	r.AddLifecycle(&failingLifecycle{})
	r.AddAuditSink(&failingSink{})

	require.Equal(t, []string{"audit:splunk", "lifecycle:meter", "policy:masking"}, r.Registered())
	require.False(t, r.Empty())
}

func TestTwoRegistriesDoNotShareHooks(t *testing.T) {
	t.Parallel()
	// Two engines in one process, which is what the test suite is.
	a := extension.NewRegistry()
	b := extension.NewRegistry()
	a.AddPolicy(&refusing{name: "only-a", err: errors.New("no")})

	require.Error(t, a.CheckPolicy(context.Background(), extension.EnvironmentRequest{}))
	require.NoError(t, b.CheckPolicy(context.Background(), extension.EnvironmentRequest{}))
}

func TestConcurrentRegistrationAndUseIsSafe(t *testing.T) {
	t.Parallel()
	// Registration happens at startup and use happens per request, and the race
	// detector is the only thing that would ever catch them overlapping.
	r := extension.NewRegistry()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 200 {
			r.AddPolicy(&refusing{name: "p"})
		}
	}()
	for range 200 {
		_ = r.CheckPolicy(context.Background(), extension.EnvironmentRequest{})
		_ = r.Registered()
		_ = r.Empty()
	}
	<-done
}
