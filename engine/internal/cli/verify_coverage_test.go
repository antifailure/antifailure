package cli

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/verify"
)

// The JSON every verifying command prints, and the error it exits with. Four
// commands used to build the document by hand and index Findings[0] on any
// unclean report, which on a report whose only problem is a skipped column
// is an index out of range.

func codeOf(t *testing.T, err error) aferrors.Code {
	t.Helper()
	var coded *aferrors.Error
	require.True(t, errors.As(err, &coded), "%v carries no code", err)
	return coded.Code()
}

func TestVerifyJSON_CarriesTheUnreadAndUnruledColumns(t *testing.T) {
	t.Parallel()
	doc := verifyJSON(verify.Report{
		Tables: 2, Columns: 9, RowsSampled: 40, SampleSize: 2000,
		Unread: []verify.UnreadColumn{{
			Schema: "public", Table: "provider_keys", Column: "ciphertext", Type: "bytea",
			Reason: "not readable by the scanner: bytea", Ruled: true,
		}},
		Unruled: []string{"public.runs.kind"},
	})
	require.True(t, doc.Clean)
	require.Equal(t, 1, doc.UnruledCount)
	require.Equal(t, []string{"public.runs.kind"}, doc.Unruled)
	require.Len(t, doc.Unread, 1)
	require.Equal(t, "public.provider_keys", doc.Unread[0].Table)
	require.Equal(t, "bytea", doc.Unread[0].Type)
	require.True(t, doc.Unread[0].Ruled)
}

func TestVerifyFailure_AnUnreadSecretGetsItsOwnCode(t *testing.T) {
	t.Parallel()
	err := verifyFailure(verify.Report{Findings: []verify.Finding{{
		Schema: "public", Table: "sso_connection_secrets", Column: "sp_private_key",
		Detector: verify.DetectorUnreadSensitive, Example: "bytea",
	}}})
	require.Error(t, err)
	require.Equal(t, aferrors.AFMSK013, codeOf(t, err))
	require.Contains(t, err.Error(), "public.sso_connection_secrets.sp_private_key (bytea)")
}

func TestVerifyFailure_AFindingKeepsTheDetectorCode(t *testing.T) {
	t.Parallel()
	err := verifyFailure(verify.Report{Findings: []verify.Finding{{
		Schema: "public", Table: "billing_customers", Column: "stripe_customer_id",
		Detector: "provider-identifier", Example: "cus*****Hib", Rows: 12,
	}}})
	require.Equal(t, aferrors.AFMSK002, codeOf(t, err))
}

func TestVerifyFailure_ASkipAloneDoesNotPanic(t *testing.T) {
	t.Parallel()
	err := verifyFailure(verify.Report{Skipped: []string{"public.blobs.payload: permission denied"}})
	require.Equal(t, aferrors.AFMSK011, codeOf(t, err))
	require.Contains(t, err.Error(), "public.blobs.payload")
	require.NoError(t, verifyFailure(verify.Report{}))
}
