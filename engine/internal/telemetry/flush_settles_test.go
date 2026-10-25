package telemetry

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/state"
)

// heldSink keeps every event until it is opened.
type heldSink struct{ open chan struct{} }

func (s heldSink) Name() string { return "held" }
func (s heldSink) Deliver(context.Context, events.Event) error {
	<-s.open
	return nil
}
func (s heldSink) Close() error { return nil }

// FlushControlPlane used to flush the sink without waiting for the bus, so an
// event emitted just before it could still be queued and miss the flush. The
// evidence upload emits one artifact.stored per recording and then flushes,
// and on CI on 2026-10-25 one of three missed. A flush must settle the bus
// first, which a sink held on the same bus makes visible: the flush cannot
// finish while an event emitted before it is still undelivered.
func TestAFlushWaitsForEventsStillOnTheBus(t *testing.T) {
	dir := t.TempDir()
	db, err := state.Open(t.Context(), dir)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	rec := &recorder{}
	srv := httptest.NewServer(rec)
	defer srv.Close()
	env := map[string]string{
		"AF_CONTROL_PLANE_TOKEN": "engine-token",
		"AF_CONTROL_PLANE_URL":   srv.URL,
	}
	bus := events.NewBus(clock.NewFake(time.Unix(1700000000, 0).UTC()))
	tel, err := Attach(t.Context(), bus, Options{
		StateDir: dir, EnvID: "shop-main-a1b2", Redactor: redact.New(), State: db,
		Getenv: func(k string) string { return env[k] },
	})
	require.NoError(t, err)
	held := heldSink{open: make(chan struct{})}
	bus.AddSink(held)

	bus.Info("shop-main-a1b2", events.ArtifactStored, "stored the recording")

	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, tel.FlushControlPlane(short), context.DeadlineExceeded,
		"the flush finished while an event emitted before it was still on the bus")

	close(held.open)
	flushed, cancelFlushed := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelFlushed()
	require.NoError(t, tel.FlushControlPlane(flushed))
	require.Contains(t, rec.all(), "artifact.stored", "the flush did not deliver the event it waited for")
	require.NoError(t, tel.Close(context.Background()))
}
