package env

import (
	"context"
	"reflect"
	"testing"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/journal"
	"github.com/antifailure/antifailure/engine/internal/lease"
	"github.com/antifailure/antifailure/engine/internal/state"
	"github.com/antifailure/antifailure/engine/internal/testutil/fakes"
)

func TestReaperLifecycleNamesTheEnvironmentItActuallyRemoved(t *testing.T) {
	for _, eventType := range []events.Type{events.EnvDestroying, events.EnvDestroyed} {
		t.Run(string(eventType), func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			db, err := state.Open(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			c := clock.New()
			bus := events.NewBus(c)
			sink := events.NewMemorySink(64)
			bus.AddSink(sink)
			o := &Orchestrator{
				envID: "checkout-environment", opts: Options{Root: root, Clock: c},
				progress: func(string) {},
			}
			s := &session{db: db, bus: bus, runtime: fakes.NewRuntime(),
				dbProv: fakes.NewInMemoryDatabase(), journal: journal.New(db, c, bus)}
			w := &sweeper{o: o, s: s, stateDir: root, leases: lease.NewStore(db, c),
				result: &ReapResult{Teardowns: map[string]*Teardown{}}}
			if _, err := w.Destroy(ctx, "expired-other-branch"); err != nil {
				t.Fatal(err)
			}
			if err := bus.Close(); err != nil {
				t.Fatal(err)
			}
			var targets []string
			for _, event := range sink.OfType(eventType) {
				targets = append(targets, event.Env)
			}
			if !reflect.DeepEqual(targets, []string{"expired-other-branch"}) {
				t.Fatalf("%s targeted %v, want only the expired environment", eventType, targets)
			}
		})
	}
}
