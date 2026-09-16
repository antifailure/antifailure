package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/report"
)

// finishedRunWithFindings stores one finished run whose result carries a mix of
// security and non-security findings, so the projection can be tested against a
// real row rather than a mock.
func finishedRunWithFindings(t *testing.T, store *Store) string {
	t.Helper()
	ctx := context.Background()
	run, _, fault := store.Submit(ctx, "cli", "repo", "rehearse", "", map[string]any{"n": "1"})
	require.Nil(t, fault)
	result := storedResult{
		Tool:    "rehearse",
		Summary: "the change was rehearsed",
		Findings: FindingPage{
			Total: 4, Shown: 4,
			Items: []Finding{
				{Rule: "security.authz.idor", Level: "fail", Title: "reached across a tenant boundary",
					Detail: "GET /api/orders/{id} answered 200 as another tenant", Fix: "check ownership",
					Where: "GET /api/orders/{id}", Count: 1},
				{Rule: "security.headers.missing_hsts", Level: "warn", Title: "no HSTS header",
					Where: "GET /", Count: 1},
				{Rule: "security.canary_leak.pii_in_response", Level: "ignore", Title: "silenced by policy",
					Where: "GET /profile"},
				{Rule: "migration_lint", Level: "warn", Title: "a column was added without a default"},
			},
		},
	}
	require.NoError(t, store.Finish(ctx, run.ID, report.VerdictFail, result))
	return run.ID
}

func callReadSecurity(t *testing.T, store *Store, args map[string]any) map[string]any {
	t.Helper()
	tool := newReadSecurityFindingsTool(&Project{ID: "repo"}, store)
	out, fault := tool.Handler(context.Background(), &Call{Caller: "cli", Project: "repo"}, args)
	require.Nil(t, fault, "the projection should not fault")
	doc, ok := out.(map[string]any)
	require.True(t, ok, "the projection is a document")
	return doc
}

func TestReadSecurityFindings_ProjectsOnlySecurityAndDropsIgnored(t *testing.T) {
	store, _ := newStore(t)
	id := finishedRunWithFindings(t, store)

	doc := callReadSecurity(t, store, map[string]any{"project_id": "repo", "run_id": id})

	require.Equal(t, "security_findings", doc["kind"])
	findings := doc["findings"].([]securityFinding)
	// The migration finding is not security; the ignored security finding is
	// dropped; two security findings remain.
	require.Len(t, findings, 2)
	rules := []string{findings[0].Rule, findings[1].Rule}
	require.ElementsMatch(t, []string{"security.authz.idor", "security.headers.missing_hsts"}, rules)
	// Worst first: the fail comes before the warn.
	require.Equal(t, "security.authz.idor", findings[0].Rule, "findings are worst first")
	require.Equal(t, "authz", findings[0].Family, "the family is read from the rule")

	totals := doc["totals"].(map[string]int)
	require.Equal(t, 1, totals["fail"])
	require.Equal(t, 1, totals["warn"])
}

func TestReadSecurityFindings_HonorsTheDataBoundary(t *testing.T) {
	store, _ := newStore(t)
	id := finishedRunWithFindings(t, store)
	doc := callReadSecurity(t, store, map[string]any{"project_id": "repo", "run_id": id})

	require.Contains(t, doc, "boundary_note")
	// No field carries a body, a response or a row: the projection is location
	// and description only.
	for _, forbidden := range []string{"body", "response", "responses", "row", "rows", "dom", "screenshot"} {
		require.NotContains(t, doc, forbidden, "the projection must never carry %q", forbidden)
	}
	for _, f := range doc["findings"].([]securityFinding) {
		require.Equal(t, f.Where, f.TargetRef, "target_ref mirrors the location, not a value")
	}
}

func TestReadSecurityFindings_FiltersByFamilyAndLevel(t *testing.T) {
	store, _ := newStore(t)
	id := finishedRunWithFindings(t, store)

	only := callReadSecurity(t, store, map[string]any{
		"project_id": "repo", "run_id": id, "family": "authz",
	})
	f := only["findings"].([]securityFinding)
	require.Len(t, f, 1)
	require.Equal(t, "security.authz.idor", f[0].Rule)

	warns := callReadSecurity(t, store, map[string]any{
		"project_id": "repo", "run_id": id, "level": "warn",
	})
	fw := warns["findings"].([]securityFinding)
	require.Len(t, fw, 1)
	require.Equal(t, "security.headers.missing_hsts", fw[0].Rule)
}

func TestReadSecurityFindings_DefaultsToTheLatestFinishedRun(t *testing.T) {
	store, _ := newStore(t)
	_ = finishedRunWithFindings(t, store)

	doc := callReadSecurity(t, store, map[string]any{"project_id": "repo"})
	require.Equal(t, "security_findings", doc["kind"])
	require.Len(t, doc["findings"].([]securityFinding), 2, "with no run_id it reads the latest finished run")
}

func TestReadSecurityFindings_NoFinishedRunIsRefusedNotEmpty(t *testing.T) {
	store, _ := newStore(t)
	tool := newReadSecurityFindingsTool(&Project{ID: "repo"}, store)
	_, fault := tool.Handler(context.Background(), &Call{Caller: "cli", Project: "repo"},
		map[string]any{"project_id": "repo"})
	require.NotNil(t, fault, "a caller who has run nothing is told so, not handed a clean empty page")
	require.Equal(t, FaultRunNotFound, fault.Code)
}

func TestReadSecurityFindings_RequiresTheProjectAssertion(t *testing.T) {
	store, _ := newStore(t)
	tool := newReadSecurityFindingsTool(&Project{ID: "repo"}, store)
	_, fault := tool.Handler(context.Background(), &Call{Caller: "cli", Project: "repo"},
		map[string]any{})
	require.NotNil(t, fault, "project_id is required on every tool")
}
