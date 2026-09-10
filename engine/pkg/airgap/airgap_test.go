package airgap_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/airgap"
)

// sealed puts the process into air gapped mode for one test and takes it out
// again, because the state is process wide by design: a guard a caller could
// hold an unsealed handle to would be a guard that could be bypassed by holding
// the wrong one.
func sealed(t *testing.T, allow ...string) {
	t.Helper()
	airgap.Reset()
	t.Cleanup(airgap.Reset)
	require.NoError(t, airgap.Allow(allow...))
	airgap.Seal("this is a test")
}

func TestWithNothingSealedEveryDialPassesThroughAndNothingIsRecorded(t *testing.T) {
	airgap.Reset()
	t.Cleanup(airgap.Reset)

	require.NoError(t, airgap.Check(airgap.SiteTelemetry, "tcp", "example.com:443"))
	require.NoError(t, airgap.Refuse(airgap.SiteTelemetry, "exporting a batch"))
	require.False(t, airgap.Sealed())
	require.Empty(t, airgap.Attempts(),
		"the community edition must carry no ledger, so the guard costs it nothing")
}

func TestSealedAnAddressTheOperatorDidNotNameIsRefusedAndRecorded(t *testing.T) {
	sealed(t)

	err := airgap.Check(airgap.SiteReleaseCheck, "tcp", "api.github.com:443")
	require.Error(t, err)
	require.ErrorIs(t, err, airgap.ErrSealed)
	require.Contains(t, err.Error(), "the release check")
	require.Contains(t, err.Error(), "api.github.com:443")

	refusals := airgap.Refusals()
	require.Len(t, refusals, 1)
	require.Equal(t, airgap.SiteReleaseCheck, refusals[0].Site)
	require.Equal(t, "api.github.com:443", refusals[0].Address)
	require.True(t, refusals[0].Refused)
}

func TestSealedAHostnameIsRefusedBeforeItIsResolved(t *testing.T) {
	sealed(t)

	// The name cannot resolve anywhere. If the guard reached the resolver
	// first, the error would be a DNS failure and the DNS query would already
	// have left the machine carrying the name. The assertion is that the error
	// is the air gap's, which it can only be if nothing looked the name up.
	err := airgap.Check(airgap.SiteTelemetry, "tcp",
		"collector.invalid.example.does.not.resolve:4318")
	require.ErrorIs(t, err, airgap.ErrSealed)
	require.NotContains(t, strings.ToLower(err.Error()), "no such host")
}

func TestSealedLoopbackIsAlwaysReachableBecauseTheProductRunsOnIt(t *testing.T) {
	sealed(t)

	for _, addr := range []string{"127.0.0.1:5432", "localhost:8080", "[::1]:9000"} {
		require.NoErrorf(t, airgap.Check(airgap.SiteServiceProbe, "tcp", addr),
			"%s is this machine talking to itself", addr)
	}
	require.Empty(t, airgap.Refusals())
	require.Len(t, airgap.Attempts(), 3,
		"a permitted connection is recorded too, because the count of what was allowed "+
			"is as much a part of the report as the count of what was not")
}

func TestSealedAUnixSocketIsReachableBecauseTheDockerDaemonIsOne(t *testing.T) {
	sealed(t)
	require.NoError(t, airgap.Check(airgap.SiteImagePull, "unix", "/var/run/docker.sock"))
}

func TestSealedAPrivateAddressIsRefusedUntilTheOperatorNamesIt(t *testing.T) {
	sealed(t)
	err := airgap.Check(airgap.SiteImagePull, "tcp", "10.4.0.9:5000")
	require.ErrorIs(t, err, airgap.ErrSealed,
		"a private range is not implicitly inside the operator's network, because "+
			"a flat corporate network would make that a silent widening")
}

func TestSealedWhatTheOperatorNamedIsReachable(t *testing.T) {
	sealed(t, "registry.internal:5000", "10.4.0.0/16", "vault.internal")

	require.NoError(t, airgap.Check(airgap.SiteImagePull, "tcp", "registry.internal:5000"))
	require.NoError(t, airgap.Check(airgap.SiteImagePull, "tcp", "10.4.0.9:5000"))
	require.NoError(t, airgap.Check(airgap.SiteCloudAuth, "tcp", "vault.internal:8200"),
		"a bare hostname permits every port on it")
	require.Empty(t, airgap.Refusals())

	require.ErrorIs(t,
		airgap.Check(airgap.SiteImagePull, "tcp", "registry.internal:5001"),
		airgap.ErrSealed,
		"an entry that named a port permits that port and not another")
	require.ErrorIs(t,
		airgap.Check(airgap.SiteImagePull, "tcp", "10.5.0.9:5000"),
		airgap.ErrSealed)
}

func TestAnAllowEntryThatIsNotAnAddressIsRefusedRatherThanIgnored(t *testing.T) {
	airgap.Reset()
	t.Cleanup(airgap.Reset)

	// net.SplitHostPort splits on the last colon and validates neither half, so
	// each of these comes back as a plausible looking host and port with no
	// error and would sit in the allow list matching nothing for ever.
	for _, entry := range []string{
		"https://registry.internal/v2/",
		"registry.internal:5000/v2",
		"registry.internal:https",
		"registry internal",
	} {
		require.Errorf(t, airgap.Allow(entry),
			"%q was accepted, and an allow list with a typo is one that is quietly "+
				"narrower than the operator believes", entry)
	}
	require.Empty(t, airgap.Allowed())

	// And the shapes that are addresses are still accepted, or the check above
	// would pass by refusing everything.
	require.NoError(t, airgap.Allow("registry.internal:5000", "10.4.0.0/16", "vault.internal", "::1"))
	require.Len(t, airgap.Allowed(), 4)
}

func TestRefuseStopsAPathThatHasNoAddressToPermit(t *testing.T) {
	sealed(t, "registry.internal:5000")

	err := airgap.Refuse(airgap.SiteTelemetry, "exporting 40 spans")
	require.ErrorIs(t, err, airgap.ErrSealed)
	require.Contains(t, err.Error(), "exporting 40 spans")
	require.Contains(t, err.Error(), "does not fall back to a degraded version of itself",
		"the point of the mode is that every one of these is a refusal rather than a fallback")

	require.Len(t, airgap.Refusals(), 1)
}

func TestAClientBuiltHereRefusesThroughItsTransport(t *testing.T) {
	sealed(t)

	// Not a unit test of Check. The whole design rests on the transport
	// actually carrying the guard, and a client built with the right helper and
	// the wrong transport would pass every test above.
	resp, err := airgap.Client(airgap.SiteReleaseCheck, 5*time.Second).
		Get("https://api.github.com/repos/antifailure/antifailure/releases/latest")
	if resp != nil {
		// Unreachable while the guard works, and closed anyway. A refused dial
		// returns a nil response, so this is here for the build where it does
		// not: a test that leaked a body in the failure case would leak it
		// exactly when somebody is already debugging.
		_ = resp.Body.Close()
	}
	require.ErrorIs(t, err, airgap.ErrSealed)
	require.Len(t, airgap.Refusals(), 1)
}

func TestAClientBuiltHereStillReachesTheEnvironmentItIsSupposedTo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	sealed(t)

	// A guard that refused this would be a guard that stops af up working at
	// all, since every container this engine starts is reached on loopback.
	resp, err := airgap.Client(airgap.SiteServiceProbe, 5*time.Second).Get(srv.URL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.Empty(t, airgap.Refusals())
}

func TestTwoClientsForOneSiteShareAPool(t *testing.T) {
	// Not a micro optimisation. neon, supabase and dblab build a client PER
	// REQUEST and poll an operation in a loop, and before the guard those were
	// http.Client values with a nil Transport, which is one shared
	// http.DefaultTransport with one shared pool. A transport per client would
	// turn every poll into a fresh TCP and TLS handshake and leave an idle
	// connection behind each time.
	a := airgap.Client(airgap.SiteNeon, time.Second)
	b := airgap.Client(airgap.SiteNeon, 2*time.Second)
	require.Same(t, a.Transport, b.Transport)
	require.NotSame(t, a, b, "the timeout belongs to the client, not to the pool")

	c := airgap.Client(airgap.SiteSupabase, time.Second)
	require.NotSame(t, a.Transport, c.Transport,
		"a site is what the ledger names, so the pools are separated the same way")
}

func TestTransportIsNewEachTimeBecauseItsCallersWriteToIt(t *testing.T) {
	// The load generator and the conformance suite both set fields on what
	// they get back. Handing them a shared value would be one goroutine
	// writing a transport another is reading.
	require.NotSame(t, airgap.Transport(airgap.SiteLoadTest), airgap.Transport(airgap.SiteLoadTest))
}

func TestDialContextRefusesBeforeItOpensASocket(t *testing.T) {
	sealed(t)
	_, err := airgap.DialContext(airgap.SiteDoctor)(context.Background(), "tcp", "1.1.1.1:53")
	require.ErrorIs(t, err, airgap.ErrSealed)
}

func TestDialTimeoutFormRefusesToo(t *testing.T) {
	sealed(t)
	_, err := airgap.Dial(airgap.SiteDoctor, "tcp", "1.1.1.1:53", time.Second)
	require.ErrorIs(t, err, airgap.ErrSealed)
}

func TestLookupHostRefusesTheQueryThatWouldHaveLeakedTheName(t *testing.T) {
	sealed(t)
	_, err := airgap.LookupHost(airgap.SiteDoctor, "api.github.com")
	require.ErrorIs(t, err, airgap.ErrSealed)

	// The permitted half is asserted through Check rather than through
	// LookupHost, because asserting it through LookupHost would put a real DNS
	// query on the wire from a unit test, which is the exact behaviour this
	// package exists to stop.
	require.NoError(t, airgap.Allow("api.github.com"))
	require.NoError(t, airgap.Check(airgap.SiteDoctor, "dns", "api.github.com:53"),
		"a name the operator permitted must resolve, or the allow list would be unusable")
}

func TestRegistryHostFollowsDockersOwnRule(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"postgres:17":                             "registry-1.docker.io",
		"clickhouse/clickhouse-server:24.3":       "registry-1.docker.io",
		"registry.internal/clickhouse-server:24":  "registry.internal",
		"registry.internal:5000/pg@sha256:abc":    "registry.internal:5000",
		"localhost:5000/pg:17":                    "localhost:5000",
		"ghcr.io/antifailure/af-proxy@sha256:abc": "ghcr.io",
	}
	for ref, want := range cases {
		require.Equalf(t, want, airgap.RegistryHost(ref), "the registry for %s", ref)
	}
}

func TestCheckImageRefusesDockerHubAndPermitsTheOperatorsRegistry(t *testing.T) {
	sealed(t, "registry.internal:5000")

	require.ErrorIs(t, airgap.CheckImage(airgap.SiteImagePull, "postgres:17"), airgap.ErrSealed)
	require.NoError(t, airgap.CheckImage(airgap.SiteImagePull,
		"registry.internal:5000/postgres@sha256:aaaa"))
}

func TestAttemptRendersForAReport(t *testing.T) {
	t.Parallel()
	refused := airgap.Attempt{Site: airgap.SiteTelemetry, Address: "otel.vendor.com:4318", Refused: true}
	require.Equal(t, "the telemetry exporter was refused reaching otel.vendor.com:4318", refused.String())
	allowed := airgap.Attempt{Site: airgap.SiteImagePull, Address: "registry.internal:5000"}
	require.Equal(t, "the container image pull reached registry.internal:5000", allowed.String())
}

func TestTheLedgerSurvivesConcurrentUse(t *testing.T) {
	sealed(t)
	// The guard sits on every dial in the process, so it is reached from
	// several goroutines at once during any af up. A ledger that raced would
	// corrupt the number the whole feature is measured by, and the race would
	// only show under load.
	done := make(chan struct{})
	for i := 0; i < 16; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 32; j++ {
				_ = airgap.Check(airgap.SiteLoadTest, "tcp", "example.com:443")
			}
		}()
	}
	for i := 0; i < 16; i++ {
		<-done
	}
	require.Len(t, airgap.Refusals(), 16*32)
}

func TestASealedProcessStaysSealed(t *testing.T) {
	sealed(t)
	require.True(t, airgap.Sealed())
	require.Equal(t, "this is a test", airgap.Reason())
	// There is deliberately no Unseal, and Reset is a test helper rather than
	// a way out: the expensive direction of a mistake here is not "the air gap
	// stopped working", it is "the machine in the secure facility started
	// talking to the internet". Sealing twice with a different reason keeps the
	// installation sealed rather than replacing a stricter state with a looser
	// one.
	airgap.Seal("a second reason")
	require.True(t, airgap.Sealed())
	require.ErrorIs(t, airgap.Check(airgap.SiteTelemetry, "tcp", "example.com:443"),
		airgap.ErrSealed)
}

func TestARefusalIsDistinguishableFromANetworkThatIsDown(t *testing.T) {
	sealed(t)
	err := airgap.Check(airgap.SiteNeon, "tcp", "console.neon.tech:443")
	require.ErrorIs(t, err, airgap.ErrSealed)

	var dnsErr *net.DNSError
	require.False(t, errors.As(err, &dnsErr),
		"an operator meeting this in a log needs to know it is a decision and not a fault")
}
