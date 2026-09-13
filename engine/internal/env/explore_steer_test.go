package env

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/explore"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func steeringManifest() *schema.Manifest {
	return &schema.Manifest{
		Name:     "app",
		Personas: []schema.Persona{{Name: "owner"}, {Name: "viewer"}},
		Explore: &schema.Explore{Enabled: true, Goals: []schema.Goal{{
			Name: "billing", Goal: "Download the latest invoice.", Persona: "owner",
			Seed: "billing", StartPath: "/", Budget: &schema.Budget{Steps: 40, Duration: "2m"},
		}}},
	}
}

func TestGoalDocs_TheManifestIsTheDefaultAndTheCallOverridesItFieldByField(t *testing.T) {
	o := orchestratorFor(t, t.TempDir(), steeringManifest())

	plain, err := explore.Steering{}.Resolve(o.personaNames())
	require.NoError(t, err)
	docs := o.goalDocs(ExploreOptions{}, plain)
	require.Len(t, docs, 1)
	require.Equal(t, goalDoc{
		Name: "billing", Goal: "Download the latest invoice.", Persona: "owner",
		Seed: "billing", StartPath: "/", MaxSteps: 40, MaxMs: 120_000,
	}, docs[0], "a call that steers nothing sends the manifest's goal unchanged, its time budget included")

	steer := explore.Steering{
		Persona: "viewer", StartPath: "/settings/billing", Viewport: "phone",
		Budget: "5m", Focus: "invoices",
	}
	resolved, err := steer.Resolve(o.personaNames())
	require.NoError(t, err)
	d := o.goalDocs(ExploreOptions{Steer: steer}, resolved)[0]
	require.Equal(t, "viewer", d.Persona)
	require.Equal(t, "/settings/billing", d.StartPath)
	require.Equal(t, &explore.Viewport{Name: "phone", Width: 390, Height: 844, Mobile: true}, d.Viewport)
	require.Equal(t, 40, d.MaxSteps, "a time budget is added beside the goal's steps, not in place of them")
	require.Equal(t, int64(300_000), d.MaxMs, "the call's time budget replaces the goal's two minutes")
	require.Equal(t, "invoices", d.Focus)
	require.Equal(t,
		"--persona viewer --start /settings/billing --viewport phone --budget 5m0s --focus invoices",
		d.Steered)

	steps, err := explore.Steering{Budget: "8"}.Resolve(nil)
	require.NoError(t, err)
	require.Equal(t, 8, o.goalDocs(ExploreOptions{}, steps)[0].MaxSteps,
		"a step budget from the call replaces the goal's")
}

func TestExplore_AnUndeclaredPersonaIsRefusedBeforeTheEnvironmentIsAsked(t *testing.T) {
	o := orchestratorFor(t, t.TempDir(), steeringManifest())
	_, err := o.Explore(context.Background(), ExploreOptions{Steer: explore.Steering{Persona: "admin"}})
	require.Error(t, err)
	var coded *aferrors.Error
	require.True(t, errors.As(err, &coded), err.Error())
	require.Equal(t, aferrors.AFAGT022, coded.Code(), err.Error())
	require.Contains(t, coded.Message(), "owner, viewer")
}

// requireRunner finds the real runner, refusing rather than skipping when
// AF_REQUIRE_RUNNER is set, for the reason runnerdocument_test.go gives: a
// check that goes quiet when a tool is missing reads as a pass.
func requireRunner(t *testing.T) string {
	t.Helper()
	missing := func(why string) {
		if os.Getenv("AF_REQUIRE_RUNNER") != "" {
			t.Fatalf("AF_REQUIRE_RUNNER is set and %s", why)
		}
		t.Skip(why + "; set AF_REQUIRE_RUNNER to make this a failure")
	}
	if _, err := exec.LookPath("node"); err != nil {
		missing("node is not on PATH")
	}
	runner, err := filepath.Abs(filepath.Join("..", "..", "..", "runner", "src", "main.ts"))
	require.NoError(t, err)
	if _, err := os.Stat(runner); err != nil {
		missing("the runner source is not in this tree")
	}
	modules := filepath.Join(filepath.Dir(filepath.Dir(runner)), "node_modules")
	if _, err := os.Stat(modules); err != nil {
		missing("runner/node_modules is absent; run npm ci in runner")
	}
	return runner
}

// devicePage names its only control after the browser that opened it, so the
// device an exploration ran on is a fact in its journey.
const devicePage = `<!doctype html><html><head>
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%[1]s</title></head><body><h1>%[1]s</h1>
<button type="button" id="report">Report</button>
<script>
document.getElementById('report').textContent = ['Report',
  navigator.maxTouchPoints > 0 ? 'touch' : 'mouse',
  String(window.innerWidth),
  /Mobile/.test(navigator.userAgent) ? 'phone' : 'desktop'].join(' ');
</script></body></html>`

// The steering is only real if the document this engine writes makes the
// browser do it. Each half has its own tests, and the defect this repository
// already shipped once, af explore dying on a null list, lived precisely in
// the document between two halves that each passed. So this sends the engine's
// own goal documents to the real runner and reads the result back through the
// engine's own decoder.
func TestExplore_TheSteeringTheEngineSendsIsTheSteeringTheBrowserRuns(t *testing.T) {
	runner := requireRunner(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprintf(w, devicePage, html.EscapeString(r.URL.Path))
	}))
	defer srv.Close()

	o := orchestratorFor(t, t.TempDir(), steeringManifest())
	steer := explore.Steering{Persona: "viewer", StartPath: "/settings/billing", Viewport: "phone", Budget: "1"}
	resolved, err := steer.Resolve(o.personaNames())
	require.NoError(t, err)

	out, err := o.invokeRunner(context.Background(), runner, jobDocument{
		BaseURL:   srv.URL,
		Artifacts: filepath.Join(t.TempDir(), "artifacts"),
		Goals:     o.goalDocs(ExploreOptions{Steer: steer}, resolved),
		Personas: []personaDoc{
			{Name: "owner", Email: "owner@example.test", Login: "none"},
			{Name: "viewer", Email: "viewer@example.test", Login: "none"},
		},
		WorkDir:  o.opts.Root,
		Headless: true,
	})
	require.NoError(t, err)
	rep, err := decodeExplorationReport(out)
	require.NoError(t, err)
	require.Len(t, rep.Explorations, 1)
	x := rep.Explorations[0]
	require.Equal(t, "explored", x.Outcome.Cause, x.Outcome.Detail)

	require.Equal(t, "viewer", x.Persona)
	require.Equal(t, "/settings/billing", x.StartPath)
	require.NotEmpty(t, x.Visited)
	require.Equal(t, srv.URL+"/settings/billing", x.Visited[0])
	require.Equal(t, explore.Viewport{Name: "phone", Width: 390, Height: 844, Mobile: true}, x.Viewport)

	var pressed string
	for _, m := range x.Journey {
		if m.Kind == "click" {
			pressed = m.Control
			break
		}
	}
	require.Equal(t, "Report touch 390 phone", pressed,
		"the page saw a touch screen 390 wide with a phone's user agent")
	require.Contains(t, strings.Join(x.Outcome.Reproduction, "\n"),
		"--seed billing --persona viewer --start /settings/billing --viewport phone --budget 1")
}
