package clickhouse_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/internal/datastore/clickhouse"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The lane's first acceptance: the datastore conformance suite, against a real
// ClickHouse.
//
// The suite checks the CONTRACT rather than the contents: that a refresh masks
// before it verifies and publishes nothing when verification fails, that
// branching twice for one environment produces one branch, that destroying
// twice succeeds, that a connection string is a secret, that a destroyed
// golden can no longer be branched. Everything about what is IN the store is
// checked by the live tests beside this one, where there is a client that can
// read it.
func TestConformance_AgainstARealClickHouse(t *testing.T) {
	server := requireServer(t)
	conformance.RunDatastore(t, func(t *testing.T) provider.Datastore {
		p, err := clickhouse.New(clickhouse.Options{
			ServerURL: server,
			Name:      "events",
			Progress:  func(line string) { t.Log(line) },
		})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, p.Close()) })
		return p
	}, conformance.DatastoreOptions{
		// Generous, because this runs against a server that other work on the
		// machine is also using and a behaviour that waits on a busy daemon
		// is not a behaviour that failed.
		Timeout: 3 * time.Minute,
	})
}
