package local

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/journal"
)

// TestJournalledKindsAreTheOnesTheJournalKnows is the same guard the cluster
// runtime carries, here because this runtime had the right answer by accident.
//
// A runtime does not import the journal: it is handed a callback taking a
// plain string. Nothing in the compiler notices a runtime that invents a kind
// and nothing validates one on the way in, so "container" and "network" were
// correct only because whoever typed them happened to type the catalogue's
// strings. The cluster runtime, written later, typed "namespace" where the
// catalogue said k8s.namespace and "deployment" where it said nothing at all,
// and every row it wrote was addressed to a kind Replay could not look up.
//
// An in-package test, because the constants are deliberately unexported: what
// is exported is the behaviour, and a test may import what the file under test
// may not.
func TestJournalledKindsAreTheOnesTheJournalKnows(t *testing.T) {
	require.Equal(t, string(journal.KindContainer), kindContainer,
		"the container kind this runtime writes is not the one the journal names, "+
			"so nothing registered for journal.KindContainer can ever delete it")
	require.Equal(t, string(journal.KindNetwork), kindNetwork,
		"the network kind this runtime writes is not the one the journal names")
}
