package env

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The seam between the CLI and telemetry, exercised rather than read.
//
// The credential af login stored travels CLI -> env.Options -> telemetry.Options
// -> the sink's bearer, and three of those four hops are one struct field being
// copied into another. A field copy is exactly the kind of change that looks
// done in a diff and is missing in the product, so this runs the real code:
// openReporting is the reporting-only session `af test` opens, it is the one
// path that attaches telemetry without needing Docker, and what it produces is
// asserted at an HTTP server rather than at a variable.

// ingest is a control plane that records the bearer of every batch it takes.
type ingest struct {
	mu      sync.Mutex
	bearers []string
	ids     []string
}

func (i *ingest) seen() ([]string, []string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]string(nil), i.bearers...), append([]string(nil), i.ids...)
}

func (i *ingest) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var batch struct {
		Events []struct {
			ID string `json:"id"`
		} `json:"events"`
	}
	_ = json.Unmarshal(body, &batch)

	i.mu.Lock()
	i.bearers = append(i.bearers, r.Header.Get("authorization"))
	for _, e := range batch.Events {
		i.ids = append(i.ids, e.ID)
	}
	i.mu.Unlock()

	w.Header().Set("content-type", "application/json")
	_, _ = w.Write([]byte(`{"accepted":` + itoa(len(batch.Events)) + `}`))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func reportingOrchestrator(t *testing.T, url, token string) *Orchestrator {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, StateDir), 0o755))
	return &Orchestrator{
		opts: Options{
			Root:              root,
			Manifest:          &schema.Manifest{Version: 1},
			Branch:            "main",
			Clock:             clock.NewFake(time.Unix(1700000000, 0).UTC()),
			Redactor:          redact.New(),
			Getenv:            func(string) string { return "" },
			Progress:          func(string) {},
			ControlPlaneURL:   url,
			ControlPlaneToken: token,
		},
		envID:    "shop-main-a1b2",
		progress: func(string) {},
	}
}

func TestReportingSessionUsesTheCredentialAfLoginStored(t *testing.T) {
	plane := &ingest{}
	srv := httptest.NewServer(plane)
	t.Cleanup(srv.Close)

	o := reportingOrchestrator(t, srv.URL, "afu_from_af_login")
	s := o.openReporting(t.Context())
	require.NotNil(t, s, "the reporting session opened")

	s.bus.Info("shop-main-a1b2", events.EnvReady, "the environment is ready")
	s.close()

	bearers, ids := plane.seen()
	require.NotEmpty(t, ids, "the event reached the control plane")
	require.Equal(t, []string{"Bearer afu_from_af_login"}, bearers,
		"and it was authenticated with the credential the CLI passed in")
}

// Nothing signed in reports to nobody, which is what every laptop with no
// account does and what this change must not have altered.
func TestReportingSessionWithNoCredentialSendsNothing(t *testing.T) {
	plane := &ingest{}
	srv := httptest.NewServer(plane)
	t.Cleanup(srv.Close)

	o := reportingOrchestrator(t, srv.URL, "")
	s := o.openReporting(t.Context())
	require.NotNil(t, s)

	s.bus.Info("shop-main-a1b2", events.EnvReady, "the environment is ready")
	s.close()

	bearers, ids := plane.seen()
	require.Empty(t, ids)
	require.Empty(t, bearers)
}
