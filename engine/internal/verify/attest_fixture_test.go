package verify_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/verify"
)

// The attestation the engine signs, pinned to bytes, so the one other reader
// of it can be held to the same bytes.
//
// ee/engine/compliance verifies attestations for SOC 2 and HIPAA evidence. It
// is a separate module and cannot import this package, so it mirrors the
// signed shape field for field, and the signature covers encoding/json's
// output of that shape: a field the mirror lacks is dropped on decode, the
// re-encoded bytes differ, and the signature fails. That happened four times
// over. Provenance, report.engine, report.unread and report.unruled were each
// added here, and the mirror learned none of them, so from 2026-09-01 the
// compliance report called every real attestation forged. Its own tests stayed
// green because their fixtures were written before any of the four existed.
//
// So the fixture below is signed by this package with a fixed key, with every
// field of the signed shape set, and checked in where the engine owns it. The
// compliance suite reads the same file and has to verify it. A field added
// here fails this test until the fixture carries it, and then fails the
// compliance suite until the mirror carries it.
//
// The fixture lives on the engine side on purpose. tools/editioncheck removes
// ee and runs this suite, and refuses a package that needs a file under ee.

var updateAttestation = flag.Bool("update-attestation", false,
	"rewrite testdata/attestation-signed.json from the signer")

const attestationFixture = "attestation-signed.json"

// fixedAttestation signs a report with every field of the signed shape set,
// under a key derived from a fixed seed. Ed25519 is deterministic, so the same
// inputs produce the same signature on every machine.
func fixedAttestation(t *testing.T) verify.Attestation {
	t.Helper()
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x2a}, ed25519.SeedSize))
	report := verify.Report{
		Scanner:     "antifailure/verify/2",
		Engine:      "postgres",
		StartedAt:   time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC),
		FinishedAt:  time.Date(2026, 9, 13, 12, 0, 4, 0, time.UTC),
		Tables:      2,
		Columns:     6,
		RowsSampled: 4003,
		SampleSize:  2000,
		Findings: []verify.Finding{{
			Schema: "public", Table: "users", Column: "email",
			Detector: "email", Example: "a***@example.com", Rows: 3,
		}},
		Skipped: []string{"public.events.payload: larger than the sample limit"},
		Unread: []verify.UnreadColumn{{
			Schema: "public", Table: "files", Column: "blob", Type: "bytea",
			Reason: "binary data is not read as text", Ruled: true,
		}},
		Unruled: []string{"public.users.plan"},
	}
	a, err := verify.Sign(report, "gv_20260913120000000000_c47a6bd6", "c47a6bd655fef26e",
		"gp1-0123456789abcdef0123", key)
	require.NoError(t, err)
	return a
}

func TestTheSignerStillProducesTheCheckedInAttestation(t *testing.T) {
	a := fixedAttestation(t)
	require.True(t, a.Verify(), "the fixed attestation does not verify against its own key")

	body, err := json.MarshalIndent(a, "", "  ")
	require.NoError(t, err)
	body = append(body, '\n')

	path := filepath.Join("testdata", attestationFixture)
	if *updateAttestation {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, body, 0o644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "run with -update-attestation to write the fixture")
	require.Equal(t, string(want), string(body),
		"the signed shape changed. Run this test with -update-attestation, then make "+
			"ee/engine/compliance mirror the change, or every compliance report will call "+
			"real attestations forged")
}

// TestTheFixtureSetsEveryFieldOfTheSignedShape is what makes the fixture
// complete rather than merely current. A field left at its zero value is
// omitted or looks absent, and a mirror that lacks it would still pass.
func TestTheFixtureSetsEveryFieldOfTheSignedShape(t *testing.T) {
	a := fixedAttestation(t)
	requireEveryField(t, "Attestation", reflect.ValueOf(a))
	requireEveryField(t, "Report", reflect.ValueOf(a.Report))
	require.NotEmpty(t, a.Report.Findings)
	requireEveryField(t, "Finding", reflect.ValueOf(a.Report.Findings[0]))
	require.NotEmpty(t, a.Report.Unread)
	requireEveryField(t, "UnreadColumn", reflect.ValueOf(a.Report.Unread[0]))
}

func requireEveryField(t *testing.T, name string, v reflect.Value) {
	t.Helper()
	for i := 0; i < v.NumField(); i++ {
		field := v.Type().Field(i)
		if !field.IsExported() {
			continue
		}
		require.False(t, v.Field(i).IsZero(),
			"%s.%s is zero in the fixed attestation, so the compliance mirror is never "+
				"checked against it. Set it in fixedAttestation and run with -update-attestation",
			name, field.Name)
	}
}
