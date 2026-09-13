package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/explore"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func TestPrintExplorations_EachResultSaysHowItWasPointed(t *testing.T) {
	var buf bytes.Buffer
	e := &Env{Out: NewOutput(&buf, &buf), Getenv: func(string) string { return "" }}

	x := explore.Exploration{
		Name: "billing", Persona: "viewer", StartPath: "/settings/billing",
		Viewport: explore.Viewport{Name: "phone", Width: 390, Height: 844, Mobile: true},
	}
	x.Outcome.Verdict = "pass"
	x.Outcome.Detail = "Explored 1 pages in 1 steps."
	printExplorations(e, &explore.Report{Explorations: []explore.Exploration{x}})
	require.Contains(t, strings.Join(strings.Fields(buf.String()), " "),
		"as viewer from /settings/billing on phone 390x844")

	// A runner that predates the fields reports none of them, and a line
	// calling that run signed out would be false about a run that signed in.
	buf.Reset()
	old := explore.Exploration{Name: "billing"}
	old.Outcome.Verdict = "pass"
	printExplorations(e, &explore.Report{Explorations: []explore.Exploration{old}})
	require.NotContains(t, buf.String(), "signed out")
}

func TestEmitWorkflows_ACompiledWorkflowRunsAsThePersonaThatExplored(t *testing.T) {
	var buf bytes.Buffer
	e := &Env{Out: NewOutput(&buf, &buf), Getenv: func(string) string { return "" }}
	o, err := env.New(env.Options{
		Root: t.TempDir(), Branch: "main", Clock: clock.New(), Redactor: redact.New(),
		Manifest: &schema.Manifest{Name: "app", Explore: &schema.Explore{Enabled: true, Goals: []schema.Goal{{
			Name: "billing", Goal: "Download the latest invoice.", Persona: "owner",
		}}}},
	})
	require.NoError(t, err)

	// A steered run explored as somebody the goal does not name. A workflow
	// compiled from it naming the goal's persona would replay the path as the
	// wrong account, which is a different path.
	x := explore.Exploration{Name: "billing", Goal: "Download the latest invoice.", Persona: "viewer"}
	x.Outcome.Verdict = "pass"
	x.Journey = []explore.Move{{Kind: "goto", URL: "http://127.0.0.1:1/settings/billing"}}
	require.NoError(t, emitWorkflows(e, o, &explore.Report{Explorations: []explore.Exploration{x}}))
	require.Contains(t, buf.String(), "persona: viewer")
	require.NotContains(t, buf.String(), "persona: owner")

	// A runner that did not say falls back to the goal's own persona.
	buf.Reset()
	x.Persona = ""
	require.NoError(t, emitWorkflows(e, o, &explore.Report{Explorations: []explore.Exploration{x}}))
	require.Contains(t, buf.String(), "persona: owner")
}
