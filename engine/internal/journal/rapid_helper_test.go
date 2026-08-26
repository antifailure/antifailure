package journal_test

import (
	"context"
	"os"
	"path/filepath"

	"pgregory.net/rapid"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/journal"
	"github.com/antifailure/antifailure/engine/internal/state"
)

// newHarnessRapid is the property test's version of newHarness. rapid.T offers
// Cleanup but not TempDir, so the directory is made here and removed with the
// rest of the harness.
func newHarnessRapid(rt *rapid.T) *harness {
	base, err := os.MkdirTemp("", "af-journal-*")
	if err != nil {
		rt.Fatalf("temp dir: %v", err)
	}
	rt.Cleanup(func() { _ = os.RemoveAll(base) })
	dir := filepath.Join(base, state.DirName)
	db, err := state.Open(context.Background(), dir)
	if err != nil {
		rt.Fatalf("open state: %v", err)
	}
	rt.Cleanup(func() { _ = db.Close() })

	c := clock.NewFake(epoch)
	bus := events.NewBus(c)
	sink := events.NewMemorySink(0)
	bus.AddSink(sink)
	rt.Cleanup(func() { _ = bus.Close() })

	prov := newFakeProvider()
	reg := journal.NewRegistry()
	reg.Register("fake", journal.KindContainer, prov)
	reg.Register("fake", journal.KindDatabaseBranch, prov)

	return &harness{j: journal.New(db, c, bus), reg: reg, prov: prov, sink: sink, clock: c}
}
