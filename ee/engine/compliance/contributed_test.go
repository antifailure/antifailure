// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package compliance

// The two sides of an allowance, kept in step.
//
// The community docs test allows `af compliance` to appear on an enterprise
// page, because the community command tree does not have it and by construction
// cannot: it lives in a module the community build has no import path to. An
// allowance like that is a hole, and a hole nobody checks is how a misspelled
// command ends up in the documentation for ever.
//
// So this is the other half. If the name ever changes here, the docs test's
// allowance stops matching what the binary contributes and this fails.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTheContributedCommandIsWhatTheDocsTestWasToldToExpect(t *testing.T) {
	// The list in engine/internal/cli/docexamples_test.go, copied deliberately
	// rather than shared: the two live in different modules and one of them
	// cannot import the other, which is the whole reason the allowance exists.
	const allowedByTheDocsTest = "compliance"

	require.Equal(t, allowedByTheDocsTest, Name,
		"the command was renamed and the community docs test still allows the old name, "+
			"so an enterprise page could now document a command nothing provides")

	contributed := Contributed(nil)
	require.True(t, strings.HasPrefix(contributed.Use, Name+" "),
		"the contributed command's Use does not start with its own name, so af --help "+
			"and the docs allowance would disagree about what it is called")
	require.NotEmpty(t, contributed.Short)
	require.NotEmpty(t, contributed.Long)
	require.NotNil(t, contributed.Run)
}

func TestTheCommandRefusesWithoutTheLicence(t *testing.T) {
	// The gate, at the command rather than at registration, so a licence that
	// lapses while a nightly job is running stops producing reports.
	var out, errs strings.Builder
	code := Command(context.Background(), []string{"soc2", "--org", "acme"}, Options{
		Stdout: &out, Stderr: &errs,
		Gather: func(context.Context, string, time.Time, time.Time) (Evidence, error) {
			t.Fatal("the evidence was read without a licence")
			return Evidence{}, nil
		},
	})
	require.Equal(t, exitConfig, code)
	require.Contains(t, errs.String(), "compliance_packs")
	// And it says nothing was lost, because that is the true and the useful
	// half: every setting and every record is still there and a licence turns
	// the report back on unchanged.
	require.Contains(t, errs.String(), "nothing has been lost")
	require.Empty(t, out.String(), "a refusal must not write a partial document to stdout")
}

func TestAnUnknownPackNamesTheOnesThatExist(t *testing.T) {
	var out, errs strings.Builder
	code := Command(licensed(), []string{"pci", "--org", "acme"}, Options{
		Stdout: &out, Stderr: &errs,
	})
	require.Equal(t, exitConfig, code)
	require.Contains(t, errs.String(), "hipaa")
	require.Contains(t, errs.String(), "soc2")
}

func TestThePackNameIsAcceptedInEitherPosition(t *testing.T) {
	// Go's flag package stops at the first argument that is not a flag, so
	// "af compliance soc2 --org acme", which is the obvious way to type it,
	// parses no flags at all unless the command handles it. That produced a
	// usage dump that read as if the command had been typed wrongly.
	for _, args := range [][]string{
		{"soc2", "--org", "acme"},
		{"--org", "acme", "soc2"},
	} {
		var out, errs strings.Builder
		code := Command(licensed(), args, Options{
			Stdout: &out, Stderr: &errs,
			Gather: func(_ context.Context, org string, _, _ time.Time) (Evidence, error) {
				require.Equal(t, "acme", org)
				return Evidence{Org: org}, nil
			},
		})
		require.Equal(t, exitOK, code, "%v was refused; stderr said %q", args, errs.String())
		require.Contains(t, out.String(), "SOC 2")
	}
}

func TestAFailingControlExitsWithItsOwnCode(t *testing.T) {
	// The code a nightly job watches. A broken audit chain has to stop a
	// pipeline without anybody parsing the document, and it has to be
	// distinguishable from a configuration mistake, which is why it is not 1.
	var out, errs strings.Builder
	code := Command(licensed(), []string{"soc2", "--org", "acme"}, Options{
		Stdout: &out, Stderr: &errs,
		Gather: func(_ context.Context, org string, _, _ time.Time) (Evidence, error) {
			return Evidence{Org: org, Audit: AuditEvidence{
				Entries: 3, ByAction: map[string]int{"environment.created": 3},
				Breaks: []ChainBreak{{Seq: 2, Kind: "altered", Detail: "changed after it was written"}},
			}}, nil
		},
	})
	require.Equal(t, exitControlNo, code)
	require.NotEqual(t, exitConfig, exitControlNo, "the two failures must be tellable apart")
	require.Contains(t, out.String(), "Something is wrong")
	require.Contains(t, out.String(), "seq 2")
}

func TestControlsThatAreMerelyNotEvidencedAreNotAFailure(t *testing.T) {
	// Most controls are not evidenced on the first day, and a command that
	// failed on that would be switched off within a week, taking the finding
	// that matters with it.
	var out, errs strings.Builder
	code := Command(licensed(), []string{"hipaa", "--org", "acme"}, Options{
		Stdout: &out, Stderr: &errs, Gather: func(_ context.Context, org string, _, _ time.Time) (Evidence, error) {
			return Evidence{Org: org}, nil
		},
	})
	require.Equal(t, exitOK, code)
	require.Contains(t, out.String(), "not evidenced")
	require.NotContains(t, out.String(), "Something is wrong")
}

func licensed() context.Context {
	return withFeatures(context.Background(), "compliance_packs")
}
