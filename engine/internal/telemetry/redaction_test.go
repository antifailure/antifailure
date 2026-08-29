package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/state"
)

// The engine has five writers that can put an event somewhere a person will
// later read it: the local NDJSON log, the spool on disk, a span attribute, the
// bytes an OTLP collector receives, and the body of the request the control
// plane receives. Four of those had a test saying a connection string cannot
// reach them. This is the fifth, and it is the only one of the five that leaves
// the machine.
//
// It is written against the raw bytes on the wire rather than against a decoded
// payload on purpose. A decoded assertion checks the fields the test author
// thought of; the bytes are what the control plane actually stores, and they
// include the ones nobody remembered to look at.

// recorder is a control plane that keeps every byte it was sent.
type recorder struct {
	mu     sync.Mutex
	bodies []string
}

func (rec *recorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	rec.mu.Lock()
	rec.bodies = append(rec.bodies, string(body))
	rec.mu.Unlock()
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte(`{"accepted":1,"duplicates":0}`))
}

func (rec *recorder) all() string {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return strings.Join(rec.bodies, "\n")
}

func TestASecretInAnEventNeverReachesTheControlPlane(t *testing.T) {
	dir := t.TempDir()
	db, err := state.Open(t.Context(), dir)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// Assembled here rather than written down, so that no part of this file is
	// a string a scanner would have to be taught to ignore.
	password := "cp" + "-secret-" + "6d20fe"
	r := redact.New()
	r.Register(password)

	rec := &recorder{}
	srv := httptest.NewServer(rec)
	defer srv.Close()

	env := map[string]string{
		"AF_CONTROL_PLANE_TOKEN": "engine-token",
		"AF_CONTROL_PLANE_URL":   srv.URL,
	}

	bus := events.NewBus(clock.NewFake(time.Unix(1700000000, 0).UTC()))
	tel, err := Attach(t.Context(), bus, Options{
		StateDir: dir, EnvID: "shop-main-a1b2", Redactor: r, State: db,
		Getenv: func(k string) string { return env[k] },
	})
	require.NoError(t, err)

	// Three shapes, because they reach the wire by three different routes:
	// a registered secret in a field, an unregistered one that only the
	// connection string pattern catches, and one in the human message.
	bus.Info("shop-main-a1b2", events.DBBranched, "branched "+
		"postgres://app:"+password+"@db:5432/app",
		events.F("url", "postgres://app:"+password+"@db:5432/app"),
		events.F("also", "postgres://app:never-registered-either@db:5432/app"))
	require.NoError(t, tel.Close(context.Background()))

	sent := rec.all()
	require.NotEmpty(t, sent, "nothing was sent, so this test proved nothing")
	require.NotContains(t, sent, password)
	require.NotContains(t, sent, "never-registered-either")
	require.Contains(t, sent, "db:5432", "the host survives, or the event explains nothing")
}
