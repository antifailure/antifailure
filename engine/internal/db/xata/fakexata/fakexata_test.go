package fakexata

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Every database name is new across Servers, which is the case that collided:
// each conformance case builds its own Server against one shared Postgres. Ten
// thousand Servers, because the clock based name this replaced drew from a
// space of a million, so this many Servers find a repeat in it about fifty
// times over on any clock, rather than only on the coarse one Windows has.
func TestNewBranch_EveryServerNamesADatabaseNoOtherServerHas(t *testing.T) {
	seen := make(map[string]bool, 10_000)
	for i := 0; i < 10_000; i++ {
		s := &Server{branches: map[string]*branch{}}
		name := s.newBranch("main", "", "").dbName
		require.Falsef(t, seen[name], "server %d named %s, which an earlier server had already used", i, name)
		seen[name] = true
		require.LessOrEqual(t, len(name), 63, "a Postgres identifier is at most 63 bytes")
	}
}
