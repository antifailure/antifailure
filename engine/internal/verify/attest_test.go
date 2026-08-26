package verify_test

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/verify"
)

func sampleReport() verify.Report {
	return verify.Report{
		Scanner: "antifailure/verify/1", Tables: 3, Columns: 11,
		RowsSampled: 2000, SampleSize: 2000,
		StartedAt:  time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		FinishedAt: time.Date(2026, 6, 1, 12, 0, 4, 0, time.UTC),
	}
}

func TestSign_ProducesSomethingThatVerifies(t *testing.T) {
	t.Parallel()
	_, priv, err := verify.GenerateKey()
	require.NoError(t, err)

	a, err := verify.Sign(sampleReport(), "gv_1", "rules-abc", priv)
	require.NoError(t, err)
	require.True(t, a.Verify())
	require.NotEmpty(t, a.Signature)
	require.NotEmpty(t, a.PublicKey)
}

func TestVerify_RejectsAChangedReport(t *testing.T) {
	t.Parallel()
	// The reason the attestation is signed at all. A report that could be
	// edited after the fact is a claim from the same process that did the
	// masking, which is the claim that matters least.
	_, priv, err := verify.GenerateKey()
	require.NoError(t, err)

	a, err := verify.Sign(sampleReport(), "gv_1", "rules-abc", priv)
	require.NoError(t, err)

	tampered := a
	tampered.Report.Tables = 99
	require.False(t, tampered.Verify(), "a changed row count must not verify")

	tampered = a
	tampered.Report.Findings = nil
	tampered.Report.Columns = 1
	require.False(t, tampered.Verify())

	tampered = a
	tampered.Golden = "gv_something_else"
	require.False(t, tampered.Verify(), "an attestation must not transfer to another golden")

	tampered = a
	tampered.RulesHash = "other-rules"
	require.False(t, tampered.Verify(),
		"a golden verified under one set of rules is not one verified under another")
}

func TestVerify_RejectsAFindingRemovedFromAReport(t *testing.T) {
	t.Parallel()
	// The specific edit somebody would actually make: delete the finding that
	// says an address survived, and claim the golden is clean.
	_, priv, err := verify.GenerateKey()
	require.NoError(t, err)

	report := sampleReport()
	report.Findings = []verify.Finding{{
		Schema: "public", Table: "customers", Column: "email",
		Detector: "email", Example: "ad****om", Rows: 200,
	}}
	a, err := verify.Sign(report, "gv_1", "rules-abc", priv)
	require.NoError(t, err)
	require.False(t, a.Report.Clean())
	require.True(t, a.Verify())

	tampered := a
	tampered.Report.Findings = nil
	require.True(t, tampered.Report.Clean(), "it now claims to be clean")
	require.False(t, tampered.Verify(), "and the signature says it was edited")
}

func TestVerify_RejectsAnotherKeysSignature(t *testing.T) {
	t.Parallel()
	_, priv, err := verify.GenerateKey()
	require.NoError(t, err)
	otherPub, otherPriv, err := verify.GenerateKey()
	require.NoError(t, err)

	a, err := verify.Sign(sampleReport(), "gv_1", "rules-abc", priv)
	require.NoError(t, err)

	// Swapping in another key's public half, which is what somebody who wanted
	// to forge one would try first.
	forged := a
	forged.PublicKey = base64.StdEncoding.EncodeToString(otherPub)
	require.False(t, forged.Verify())

	// And a signature from another key over the same document.
	resigned, err := verify.Sign(sampleReport(), "gv_1", "rules-abc", otherPriv)
	require.NoError(t, err)
	mixed := a
	mixed.Signature = resigned.Signature
	require.False(t, mixed.Verify())
}

func TestVerify_RejectsMalformedFields(t *testing.T) {
	t.Parallel()
	_, priv, err := verify.GenerateKey()
	require.NoError(t, err)
	a, err := verify.Sign(sampleReport(), "gv_1", "rules-abc", priv)
	require.NoError(t, err)

	for name, mutate := range map[string]func(*verify.Attestation){
		"no signature":     func(x *verify.Attestation) { x.Signature = "" },
		"no key":           func(x *verify.Attestation) { x.PublicKey = "" },
		"key not base64":   func(x *verify.Attestation) { x.PublicKey = "not base64!" },
		"sig not base64":   func(x *verify.Attestation) { x.Signature = "not base64!" },
		"key wrong length": func(x *verify.Attestation) { x.PublicKey = base64.StdEncoding.EncodeToString([]byte("short")) },
	} {
		t.Run(name, func(t *testing.T) {
			broken := a
			mutate(&broken)
			require.False(t, broken.Verify())
		})
	}
}

func TestReport_CleanMeansBranchable(t *testing.T) {
	t.Parallel()
	require.True(t, sampleReport().Clean())

	dirty := sampleReport()
	dirty.Findings = []verify.Finding{{Detector: "email"}}
	require.False(t, dirty.Clean())
}

func TestFinding_ReadsAsASentence(t *testing.T) {
	t.Parallel()
	f := verify.Finding{
		Schema: "public", Table: "customers", Column: "email",
		Detector: "email", Example: "ad****om", Rows: 200,
	}
	require.Equal(t,
		"public.customers.email holds email (200 of the sampled rows, for example ad****om)",
		f.String())
}
