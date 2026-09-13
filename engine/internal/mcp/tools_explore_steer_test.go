package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/explore"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func TestExplore_TheSchemaBoundsTheSteering(t *testing.T) {
	t.Parallel()
	require.Nil(t, exploreArgs(t, `{"project_id":"p","persona":"viewer",`+
		`"start_path":"/settings/billing?tab=plan","viewport":"phone","budget":"5m",`+
		`"focus":"the plan and the invoices"}`))
	require.Nil(t, exploreArgs(t, `{"project_id":"p","viewport":"1280x720","budget":"8"}`))

	for _, tc := range []struct {
		name, body string
		code       FaultCode
	}{
		{"a persona name the manifest could never declare",
			`{"project_id":"p","persona":"Owner; drop"}`, FaultInvalidArgument},
		{"a start path that is a URL",
			`{"project_id":"p","start_path":"https://evil.example/"}`, FaultInvalidArgument},
		{"a viewport that is a word", `{"project_id":"p","viewport":"banana"}`, FaultInvalidArgument},
		{"a budget in a unit nobody measures in", `{"project_id":"p","budget":"5y"}`, FaultInvalidArgument},
		{"a focus that is a script",
			`{"project_id":"p","focus":"` + strings.Repeat("a", 501) + `"}`, FaultArgumentTooLarge},
	} {
		fault := exploreArgs(t, tc.body)
		require.NotNil(t, fault, "%s must be refused", tc.name)
		require.Equal(t, tc.code, fault.Code, tc.name)
	}
}

func TestExplore_SteeringTheSchemaAdmitsIsStillCheckedAndTheFieldIsNamed(t *testing.T) {
	t.Parallel()
	// Each of these matches the schema's pattern, so this check is the only
	// thing between it and a run that fails to start minutes later.
	for _, tc := range []struct {
		field string
		steer explore.Steering
	}{
		{"start_path", explore.Steering{StartPath: "//evil.example/billing"}},
		{"viewport", explore.Steering{Viewport: "100x100"}},
		{"budget", explore.Steering{Budget: "0"}},
	} {
		fault := steeringFault(tc.steer)
		require.NotNil(t, fault, tc.field)
		require.Equal(t, FaultInvalidArgument, fault.Code, tc.field)
		require.Equal(t, tc.field, fault.Field)
	}
	require.Nil(t, steeringFault(explore.Steering{StartPath: "/billing", Viewport: "phone", Budget: "8"}))
}

func TestExplore_APersonaTheManifestDoesNotDeclareIsTheCallersToFix(t *testing.T) {
	t.Parallel()
	// The only drive error whose words are repeated, because every one of them
	// is the engine's or the manifest's, and they are exactly what the caller
	// needs next: the names that would have worked.
	drive := func(context.Context, env.ExploreOptions) (*explore.Report, []string, error) {
		return nil, nil, aferrors.Coded(aferrors.AFAGT022, "persona", "admin", "personas", "owner, viewer")
	}
	h := newToolHarness(t)
	_, _, fault := runExploration(
		context.Background(), h.engine, drive, h.newRun(t, "explore_for_friction"), env.ExploreOptions{})
	require.NotNil(t, fault)
	require.Equal(t, FaultInvalidArgument, fault.Code)
	require.Equal(t, "persona", fault.Field)
	require.Contains(t, fault.Detail, "owner, viewer")
}

func TestExplore_TheResultSaysHowEachExplorationWasPointed(t *testing.T) {
	t.Parallel()
	x := exploration("billing", report.VerdictPass, true)
	x.Persona = "viewer"
	x.StartPath = "/settings/billing"
	x.Viewport = explore.Viewport{Name: "phone", Width: 390, Height: 844, Mobile: true}
	x.Focus = "invoices"

	doc := describeExploration(&explore.Report{Explorations: []explore.Exploration{x}}, []string{"billing"})
	require.Len(t, doc.Explorations, 1)
	got := doc.Explorations[0]
	require.Equal(t, "viewer", got.Persona)
	require.Equal(t, "/settings/billing", got.StartPath)
	require.Equal(t, "phone 390x844", got.Viewport)
	require.Equal(t, "invoices", got.Focus)
}

func TestExplore_TheCallsSteeringReachesTheRunAndAnUnusableOneStopsBeforeIt(t *testing.T) {
	t.Parallel()
	h := newToolHarness(t)
	calls := 0
	var got env.ExploreOptions
	drive := func(_ context.Context, opts env.ExploreOptions) (*explore.Report, []string, error) {
		calls++
		got = opts
		return &explore.Report{Explorations: []explore.Exploration{
			exploration("billing", report.VerdictPass, true),
		}}, []string{"billing"}, nil
	}
	tool := newExploreTool(h.project, h.engine, drive)

	_, fault := tool.Handler(context.Background(), &Call{}, map[string]any{
		"project_id": h.project.ID, "viewport": "100x100",
	})
	require.NotNil(t, fault, "a viewport the schema admits and no browser can open is refused as an argument")
	require.Equal(t, "viewport", fault.Field)

	_, fault = tool.Handler(context.Background(), &Call{}, map[string]any{
		"project_id": h.project.ID, "goals": []any{"billing"}, "seed": "s",
		"persona": "viewer", "start_path": "/settings/billing", "viewport": "phone",
		"budget": "5m", "focus": "invoices",
	})
	require.Nil(t, fault)
	h.engine.Wait()
	require.Equal(t, 1, calls, "the refused call never reached the drive, and the accepted one did")
	require.Equal(t, env.ExploreOptions{
		Only: []string{"billing"}, Seed: "s",
		Steer: explore.Steering{
			Persona: "viewer", StartPath: "/settings/billing", Viewport: "phone",
			Budget: "5m", Focus: "invoices",
		},
	}, got)
}

func TestExplore_TheServersOrchestratorIsGivenTheCallsSteering(t *testing.T) {
	t.Parallel()
	// The drive the server registers copies only the fields a call may set.
	// A steering left out of that copy would be accepted, validated, recorded
	// and then never used, so this asks the real orchestrator to refuse a
	// persona only the steering names.
	f := &orchestratorFactory{
		project: &Project{ID: "p", Root: t.TempDir(), Manifest: &schema.Manifest{
			Name:     "app",
			Personas: []schema.Persona{{Name: "owner"}},
			Explore: &schema.Explore{Enabled: true, Goals: []schema.Goal{{
				Name: "billing", Goal: "Download the latest invoice.", Seed: "billing",
			}}},
		}},
		cfg: Config{Getenv: func(string) string { return "" }},
	}
	_, _, err := f.driveExploration(context.Background(), env.ExploreOptions{
		Steer: explore.Steering{Persona: "admin"},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFAGT022))
}
