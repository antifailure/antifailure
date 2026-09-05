package cli

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/load"
	"github.com/antifailure/antifailure/engine/internal/report"
)

func TestLoadSmokeRealFailureThenRecovery(t *testing.T) {
	for _, tc := range []struct {
		code    int
		verdict string
	}{
		{http.StatusInternalServerError, report.VerdictFail},
		{http.StatusOK, report.VerdictPass},
	} {
		t.Run(tc.verdict, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tc.code) }))
			defer server.Close()
			shape, _ := load.ShapeFromSafeRoutes([]string{"GET /runs"})
			res, err := load.Run(context.Background(), load.Options{BaseURL: server.URL, Shape: shape, Scale: 100, Duration: 100 * time.Millisecond, Seed: 1})
			require.NoError(t, err)
			run := report.Run{Workflows: []report.Workflow{{Name: "read", Verdict: "pass"}}}
			run.Load = loadReport(res, nil, 0, 0.02, &run)
			if finding := loadFinding(run.Load, report.Policy{LoadRegression: report.LevelFail}); finding != nil {
				run.Findings = append(run.Findings, *finding)
			}
			require.Equal(t, tc.verdict, run.Verdict())
			require.Contains(t, run.Markdown(), "GET /runs:")
		})
	}
}

func TestLoadSmokeReportCarriesMeasurements(t *testing.T) {
	run := report.Run{}
	l := loadReport(&load.Result{Sent: 4, Source: "safe_routes", Routes: []load.RouteResult{{Route: "GET /runs", Sent: 4, Errors: 1}}}, nil, 0, 0.02, &run)
	require.Equal(t, "safe_routes", l.Source)
	require.Equal(t, []report.LoadRoute{{Route: "GET /runs", Sent: 4, Errors: 1}}, l.Routes)
	run.Load = l
	require.Contains(t, run.Markdown(), "Traffic source: safe_routes.")
	require.Contains(t, run.Markdown(), "GET /runs: 4 requests, 1 errors.")
}

func TestIncompleteLoadPreservesPartialMeasurements(t *testing.T) {
	l := incompleteLoadReport(&load.Result{Sent: 4}, nil, errors.New("interrupted"))
	require.Equal(t, 4, l.Sent)
	require.Equal(t, "interrupted", l.Unavailable)
}

func TestIncompleteLoadWithoutAResult(t *testing.T) {
	l := incompleteLoadReport(nil, nil, errors.New("no safe route"))
	require.Equal(t, "no safe route", l.Unavailable)
}

func TestLoadSmokeMissingMeasurementsAreInconclusive(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result *load.Result
		p95    float64
	}{
		{"zero requests", &load.Result{}, 0},
		{"no baseline", &load.Result{Sent: 1, Routes: []load.RouteResult{{Route: "GET /runs", Sent: 1}}}, 0.2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := report.Run{Workflows: []report.Workflow{{Name: "read", Verdict: "pass"}}}
			run.Load = loadReport(tc.result, nil, tc.p95, 0.02, &run)
			require.Equal(t, report.VerdictBlocked, run.Verdict())
			require.Contains(t, run.Markdown(), "Load was inconclusive:")
		})
	}
}
