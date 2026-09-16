package env

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// A throwaway run has no use for the day-long default lifetime, and the day is
// exactly what turns a crashed af ci into a day of paying for an environment
// nobody will look at again. MarkEphemeral bounds the lifetime to the run's own
// budget, so the reaper collects a leaked run env within the hour. These prove
// the override reaches the one method every stamp and every reported expiry
// reads, o.ttl, so console and reaper cannot disagree about when it dies.

func ephemeralOrchestrator(ttl string) *Orchestrator {
	return &Orchestrator{opts: Options{
		Manifest: &schema.Manifest{Runtime: &schema.Runtime{TTL: ttl}},
	}}
}

func TestMarkEphemeral_OverridesTheManifestLifetime(t *testing.T) {
	o := ephemeralOrchestrator("24h")
	require.Equal(t, 24*time.Hour, o.ttl(), "the baseline is the manifest lifetime")

	o.MarkEphemeral(90 * time.Minute)

	require.Equal(t, 90*time.Minute, o.ttl(),
		"the throwaway run's budget must win over the manifest's day-long default")
}

func TestMarkEphemeral_ZeroLeavesTheManifestLifetimeInForce(t *testing.T) {
	// A caller that could not work out a budget must not blank the lifetime: a
	// zero here would be the immortal-environment hole reopened from the other
	// side. The manifest's lifetime stays in force instead.
	o := ephemeralOrchestrator("24h")

	o.MarkEphemeral(0)

	require.Equal(t, 24*time.Hour, o.ttl(),
		"a zero budget must fall back to the manifest lifetime, never to no lifetime")
}

func TestMarkEphemeral_ReachesTheControlPlaneExpiry(t *testing.T) {
	// ttlSeconds is the number the control plane fills expires_at from. A
	// short-lived CI environment must report the short expiry, so the console
	// shows the same lifetime the reaper enforces.
	o := ephemeralOrchestrator("24h")
	o.MarkEphemeral(90 * time.Minute)

	secs, ok := o.ttlSeconds()

	require.True(t, ok, "a positive ephemeral lifetime is a real expiry, not absent")
	require.Equal(t, float64(90*60), secs,
		"the reported expiry must be the ephemeral budget, not the manifest default")
}

// End to end: the ephemeral budget must reach the runtime that writes the
// expiry label, or the reaper backstop is stamped with the manifest's day-long
// lifetime and a crashed run still leaks for a day. The registered runtime
// records the TTL the engine handed it. The manifest states 30m and the run
// marks itself ephemeral at 5m, so 5m is what must arrive at the stamp.
func TestMarkEphemeral_ReachesTheRuntimeThatStampsTheExpiry(t *testing.T) {
	db := newFakeDB("acmedb")
	dbp := &fakeDBProvider{name: "acmedb", db: db}
	rt := &fakeRT{name: "acmert"}
	rtp := &fakeRTProvider{name: "acmert", rt: rt}

	reg := extension.NewRegistry()
	reg.AddDatabaseProvider(dbp)
	reg.AddRuntimeProvider(rtp)

	o := registeredOrchestrator(t, reg)
	o.MarkEphemeral(5 * time.Minute)

	_, err := o.Up(context.Background())
	require.NoError(t, err)

	require.Equal(t, 5*time.Minute, rtp.seen.TTL,
		"the ephemeral budget must override runtime.ttl at the runtime that stamps the expiry")
}
