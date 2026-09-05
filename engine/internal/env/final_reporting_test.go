package env

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

type finalReportSink struct {
	mu     sync.Mutex
	events []events.Event
}

func (*finalReportSink) Name() string { return "final-report-test" }
func (*finalReportSink) Close() error { return nil }
func (s *finalReportSink) Deliver(_ context.Context, e events.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}

// Exercise Test itself, including its runtime HTTP client, subprocess document,
// proxy log decoding and reporting session. No Docker resources are created.
// The shell substitutes only the browser runner, which already returned pass
// before the engine learned that the application used a synthesized response.
func finalReportingRun(t *testing.T, synthesized bool) (*TestReport, []events.Event) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in runner is a shell script")
	}
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			fmt.Fprint(w, `[{"Id":"web","State":"running","Labels":{"dev.antifailure.kind":"service","dev.antifailure.service":"web","dev.antifailure.service-kind":"web"}},{"Id":"forwarder","Labels":{"dev.antifailure.kind":"sidecar","dev.antifailure.service":"web"},"Ports":[{"PublicPort":46000}]}]`)
		case strings.HasSuffix(r.URL.Path, "/networks"):
			fmt.Fprint(w, `[]`)
		case strings.HasSuffix(r.URL.Path, "/logs"):
			fmt.Fprintf(w, "{\"event\":\"decision\",\"at\":\"2026-09-04T12:00:01Z\",\"host\":\"payments.example\",\"synthesized\":%t}\n", synthesized)
		case strings.HasSuffix(r.URL.Path, "/json"):
			fmt.Fprint(w, `{"Id":"proxy","Config":{"Tty":true}}`)
		default:
			t.Errorf("unexpected Docker request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	t.Cleanup(daemon.Close)
	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(daemon.URL, "http://"))
	t.Setenv("DOCKER_API_VERSION", "1.47")
	t.Setenv("DOCKER_TLS_VERIFY", "")
	root := t.TempDir()
	runner := filepath.Join(root, "runner.ts")
	if err := os.WriteFile(runner, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	document := `{"passed":1,"results":[{"workflow":"checkout","outcome":{"verdict":"pass"},"startedAt":"2026-09-04T12:00:00Z","finishedAt":"2026-09-04T12:00:02Z"}]}`
	if err := os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\ncat > /dev/null\nprintf '%s' '"+document+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	o, err := New(Options{
		Root: root, Branch: "final-report", Getenv: func(string) string { return "" },
		Manifest: &schema.Manifest{Name: "reporting", Workflows: []schema.Workflow{{Name: "checkout"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	sink := &finalReportSink{}
	o.AddSink(sink)
	report, err := o.Test(t.Context(), TestOptions{RunnerPath: runner})
	if err != nil {
		t.Fatal(err)
	}
	return report, sink.events
}

func TestReportingUsesTheFinalWorkflowVerdict(t *testing.T) {
	for _, synthesized := range []bool{true, false} {
		t.Run(fmt.Sprintf("synthesized=%t", synthesized), func(t *testing.T) {
			report, emitted := finalReportingRun(t, synthesized)
			want := "pass"
			if synthesized {
				want = "unverified"
			}
			for _, field := range []string{"value", "passed", "unverified", "state"} {
				t.Run(field, func(t *testing.T) {
					kind := events.AgentFinished
					var expected any = "complete"
					switch field {
					case "value":
						kind, expected = events.AgentVerdict, want
					case "passed":
						expected = report.Passed
					case "unverified":
						expected = report.Unverified
					}
					var values []any
					for _, e := range emitted {
						if e.Type == kind {
							values = append(values, e.Data[field])
						}
					}
					gotJSON, _ := json.Marshal(values)
					wantJSON, _ := json.Marshal([]any{expected})
					if string(gotJSON) != string(wantJSON) {
						t.Errorf("%s: got %s, want %s", field, gotJSON, wantJSON)
					}
				})
			}
		})
	}
}
