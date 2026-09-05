package cli

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/explore"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func TestCIWritesExplorationEvidenceIntoItsActualJSONReport(t *testing.T) {
	x := explore.Exploration{Name: "find-evidence", Seed: "repeatable", Visited: []string{"/runs"}}
	x.Evidence.Trace = "explore.trace.zip"
	run := report.Run{Exploration: &report.Exploration{Declared: []string{x.Name}, Results: []explore.Exploration{x}}}
	for _, needle := range []string{`"Declared":["find-evidence"]`, `"seed":"repeatable"`, `"trace":"explore.trace.zip"`} {
		t.Run(needle, func(t *testing.T) {
			body, err := json.Marshal(writeJSONReport(t, run))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), needle) {
				t.Fatalf("missing %s from %s", needle, body)
			}
		})
	}
}

// The defect this guards is a capability with no caller. Declaring the field
// and calling the browser are separate steps and each one alone looks done.
func TestConfiguredGoalsAreDeclaredInTheReport(t *testing.T) {
	for _, tc := range []struct {
		name     string
		manifest *schema.Manifest
		declared []string
		want     bool
	}{
		{"nil", nil, nil, false},
		{"absent", &schema.Manifest{}, nil, false},
		{"disabled", &schema.Manifest{Explore: &schema.Explore{Goals: []schema.Goal{{Name: "a"}}}}, nil, false},
		{"enabled", &schema.Manifest{Explore: &schema.Explore{Enabled: true, Goals: []schema.Goal{{Name: "a"}, {Name: "b"}}}}, []string{"a", "b"}, true},
		{"enabled with no goal", &schema.Manifest{Explore: &schema.Explore{Enabled: true}}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := declaredExploration(tc.manifest)
			t.Run("configured", func(t *testing.T) {
				if (x != nil) != tc.want {
					t.Fatalf("exploration field is %v, want configured=%v", x, tc.want)
				}
			})
			t.Run("goals", func(t *testing.T) {
				if x == nil {
					return
				}
				if !reflect.DeepEqual(x.Declared, tc.declared) {
					t.Fatalf("declared %v, want %v", x.Declared, tc.declared)
				}
			})
		})
	}
}

type fakeExplorer struct {
	called int
	opts   env.ExploreOptions
	report *explore.Report
	err    error
}

func (f *fakeExplorer) Explore(_ context.Context, opts env.ExploreOptions) (*explore.Report, error) {
	f.called++
	f.opts = opts
	return f.report, f.err
}

func TestConfiguredGoalsActuallyReachTheBrowser(t *testing.T) {
	observed := explore.Exploration{Name: "goal", Visited: []string{"/runs"}}
	observed.Outcome.Verdict = "pass"
	f := &fakeExplorer{report: &explore.Report{Explorations: []explore.Exploration{observed}}}
	into := &report.Exploration{Declared: []string{"goal"}}
	exploreConfigured(context.Background(), f, env.ExploreOptions{RunnerPath: "runner.ts"}, into)
	t.Run("called", func(t *testing.T) {
		if f.called != 1 {
			t.Fatalf("Explore was called %d times", f.called)
		}
	})
	t.Run("runner", func(t *testing.T) {
		if f.opts.RunnerPath != "runner.ts" {
			t.Fatalf("runner path %q did not reach the explorer", f.opts.RunnerPath)
		}
	})
	t.Run("results", func(t *testing.T) {
		if len(into.Results) != 1 || into.Results[0].Name != "goal" {
			t.Fatalf("observations did not reach the report: %v", into.Results)
		}
	})
	t.Run("available", func(t *testing.T) {
		if into.Unavailable != "" {
			t.Fatalf("a completed exploration was recorded unavailable: %q", into.Unavailable)
		}
	})
}

func TestARefusedExplorationIsRecordedRatherThanReturned(t *testing.T) {
	partial := explore.Exploration{Name: "goal"}
	f := &fakeExplorer{report: &explore.Report{Explorations: []explore.Exploration{partial}}, err: errors.New("no browser")}
	into := &report.Exploration{Declared: []string{"goal"}}
	exploreConfigured(context.Background(), f, env.ExploreOptions{}, into)
	t.Run("unavailable", func(t *testing.T) {
		if into.Unavailable != "no browser" {
			t.Fatalf("refusal was not recorded: %q", into.Unavailable)
		}
	})
	t.Run("partial evidence kept", func(t *testing.T) {
		if len(into.Results) != 1 {
			t.Fatalf("evidence observed before the refusal was discarded: %v", into.Results)
		}
	})
}
