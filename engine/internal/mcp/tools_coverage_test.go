package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/verify"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// What travels from the scan into the tools: the columns it could not read
// and the columns masking copied unchanged. Both used to exist only at the
// bottom of af mask plan, and every tool that described a golden said
// verified about one with 145 of them.

func coverageReport() verify.Report {
	return verify.Report{
		Scanner: "antifailure/verify/2", Tables: 4, Columns: 40, SampleSize: 2000, RowsSampled: 80000,
		Unread: []verify.UnreadColumn{{
			Schema: "public", Table: "provider_keys", Column: "ciphertext", Type: "bytea",
			Reason: "4 of 4 sampled values are binary rather than text and could not be read",
			Ruled:  true,
		}},
		Unruled: []string{"public.billing_customers.stripe_customer_id", "public.runs.kind"},
	}
}

func TestMaskVerify_CarriesWhatTheScanDidNotReadAndWhatMaskingLeftAlone(t *testing.T) {
	t.Parallel()
	out, fault := callMask(t, verifyReaders(coverageReport(), nil), args("question", "verify"))
	require.Nil(t, fault)

	doc := out.(*maskingVerifyDoc)
	require.Equal(t, VerdictPass, doc.Verdict, "a listed column with a rule is a note, not a failure")
	require.Equal(t, 1, doc.UnreadTotal)
	require.Equal(t, []string{"public.provider_keys.ciphertext: 4 of 4 sampled values are binary rather than text and could not be read"}, doc.Unread)
	require.Equal(t, 2, doc.UnruledTotal)
	require.Equal(t, []string{"public.billing_customers.stripe_customer_id", "public.runs.kind"}, doc.Unruled)
	require.Contains(t, doc.Summary, "2 columns were copied unchanged with no masking rule")
	require.Contains(t, doc.Summary, "1 column is not readable by the scanner")

	names := map[string]float64{}
	for _, m := range doc.Metrics {
		names[m.Name] = m.Value
	}
	require.Equal(t, 2.0, names["columns_copied_unchanged_with_no_rule"])
	require.Equal(t, 1.0, names["columns_not_readable_by_the_scanner"])
}

func TestMaskVerify_AnUnreadSecretWithNoRuleFails(t *testing.T) {
	t.Parallel()
	rep := coverageReport()
	rep.Findings = []verify.Finding{{
		Schema: "public", Table: "sso_connection_secrets", Column: "sp_private_key",
		Detector: verify.DetectorUnreadSensitive, Example: "bytea",
	}}
	out, fault := callMask(t, verifyReaders(rep, nil), args("question", "verify"))
	require.Nil(t, fault)

	doc := out.(*maskingVerifyDoc)
	require.Equal(t, VerdictFail, doc.Verdict)
	require.Len(t, doc.Findings, 1)
	require.Equal(t, verify.DetectorUnreadSensitive, doc.Findings[0].Detector)
}

func TestMaskPlan_SaysHowManyUnclassifiedColumnsShipUnchanged(t *testing.T) {
	t.Parallel()
	// Two outcomes share the unclassified list, and the summary used to say
	// every one of them was emptied. The copied ones are the list to answer
	// first and they get their own count.
	tables := []masking.Table{{
		Schema: "public", Name: "subscriptions", PrimaryKey: []string{"id"},
		Columns: []masking.ColumnInfo{
			{Name: "id", Type: "uuid"},
			{Name: "stripe_customer_id", Type: "text"},
			{Name: "memo", Type: "text", Nullable: true},
			{Name: "sealed", Type: "bytea"},
		},
	}}
	rules, err := masking.NewRuleSet(nil)
	require.NoError(t, err)
	plan := masking.BuildPlan(tables, rules.Assign(tables), "h")
	readers := maskingReaders{
		Plan: func(context.Context) (*env.PlanResult, error) {
			return &env.PlanResult{Plan: plan, RulesHash: "h", Source: "a test"}, nil
		},
	}
	out, fault := callMask(t, readers, args("question", "plan"))
	require.Nil(t, fault)

	doc := out.(*maskingPlanDoc)
	require.Equal(t, 3, doc.UnclassifiedTotal)
	require.Equal(t, 2, doc.CopiedUnchangedTotal)
	require.Contains(t, doc.Summary, "3 columns are covered by no rule: 1 emptied by the fail closed default")
	require.Contains(t, doc.Summary, "2 copied unchanged because the default could not empty them")
	names := map[string]float64{}
	for _, m := range doc.Metrics {
		names[m.Name] = m.Value
	}
	require.Equal(t, 2.0, names["columns_copied_unchanged_with_no_rule"])
}

func attestationJSON(t *testing.T, rep verify.Report) string {
	t.Helper()
	_, priv, err := verify.GenerateKey()
	require.NoError(t, err)
	att, err := verify.Sign(rep, "gv_1", "rules", mineProvenance, priv)
	require.NoError(t, err)
	body, err := json.Marshal(att)
	require.NoError(t, err)
	return string(body)
}

func TestInspectGoldens_ReadsTheCountsOutOfTheAttestation(t *testing.T) {
	t.Parallel()
	now := time.Now()
	tool := newInspectGoldensTool(testProject(t),
		goldensFrom(provider.GoldenVersion{
			ID: "gv_20260906101523_c47a6bd6", CreatedAt: now, Verified: true,
			Provenance: mineProvenance, Attestation: attestationJSON(t, coverageReport()),
		}),
		noPublished(), policyOf(3, 0))

	out := mustInvoke(t, tool, `{"project_id":"test-project"}`).(goldensResult)
	require.Len(t, out.Versions, 1)
	v := out.Versions[0]
	require.True(t, v.Attested)
	require.Equal(t, 2, v.CopiedUnchanged)
	require.Equal(t, 1, v.UnreadColumns)
	require.Equal(t, []string{"public.billing_customers.stripe_customer_id", "public.runs.kind"}, v.UnruledColumns)
}

func TestInspectGoldens_AnOlderAttestationSaysUnknownRatherThanZero(t *testing.T) {
	t.Parallel()
	// The first scanner recorded neither list. Zero and unknown are
	// different facts about a golden, and a listing that said 0 about one
	// made under rules that copied 145 columns would be the old lie in a new
	// column.
	old := verify.Report{Scanner: "antifailure/verify/1", Tables: 4, Columns: 40}
	tool := newInspectGoldensTool(testProject(t),
		goldensFrom(provider.GoldenVersion{
			ID: "gv_20260902063250_3a6bfe42", CreatedAt: time.Now(), Verified: true,
			Provenance: mineProvenance, Attestation: attestationJSON(t, old),
		}),
		noPublished(), policyOf(3, 0))

	out := mustInvoke(t, tool, `{"project_id":"test-project"}`).(goldensResult)
	require.Len(t, out.Versions, 1)
	require.False(t, out.Versions[0].Attested)
	require.Zero(t, out.Versions[0].CopiedUnchanged)
}

func TestPrepareGolden_TheBodyCarriesTheCounts(t *testing.T) {
	t.Parallel()
	body := goldenBody(goldenOutcome{Action: "refresh", Version: "gv_1", Verified: true, Report: coverageReport()})
	doc := body.Detail.(goldenDoc)
	require.Equal(t, 2, doc.UnruledColumns)
	require.Equal(t, 1, doc.UnreadColumns)
	require.Equal(t, []string{"public.billing_customers.stripe_customer_id", "public.runs.kind"}, doc.Unruled)
	require.Contains(t, body.Summary, "2 columns were copied unchanged with no masking rule, and 1 column was not readable by the scanner")
}
