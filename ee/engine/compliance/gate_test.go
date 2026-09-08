// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package compliance

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/license"
)

// The licence gate on the compliance command, observed refusing.
//
// WHY THIS FILE EXISTS. compliance_packs was one of three features counted as
// having an enforcement site on 2026-09-08, and the count was read out of the
// source: command.go asks feature.Enabled and the registry says so. Nothing
// called Command at all, with or without a licence, so nothing had ever watched
// it refuse and nothing had ever watched it permit. A site that looks like a
// gate and a site that refuses are different claims.
//
// The observable effect rather than the exit code, and that is the assertion
// that matters. A gate that printed the refusal and carried on would return the
// same 3 for a dozen other reasons, and would still have read the customer's
// audit chain, attestations and access records to produce a report it then
// threw away. So the test asserts that Gather was NEVER CALLED, which is a
// statement about what the machine did rather than about what it said.

// gatherCounter records whether the evidence was read, which is the thing a
// refusal has to prevent.
type gatherCounter struct{ calls int }

func (g *gatherCounter) gather(_ context.Context, org string, from, to time.Time) (Evidence, error) {
	g.calls++
	return Evidence{Org: org, From: from, To: to, GeneratedAt: to}, nil
}

func TestTheCommandRefusesWithoutTheLicenceAndReadsNothing(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	gather := &gatherCounter{}

	// A licence that bought other things, rather than no licence at all. An
	// administrator holding a valid enterprise licence would otherwise assume
	// it covers this, and "no licence" and "a licence without this feature"
	// reach the gate down different paths.
	ctx := withFeatures(context.Background(), license.FeatureSSO)
	code := Command(ctx, []string{"soc2", "--org", "acme"}, Options{
		Stdout: &stdout, Stderr: &stderr, Gather: gather.gather,
	})

	require.Equal(t, 3, code, "a refused report did not exit with the configuration code")
	require.Equal(t, 0, gather.calls,
		"the evidence was gathered for a report that was refused, so the refusal is a message "+
			"printed over work that was done anyway")
	require.Empty(t, stdout.String(), "a refused report still wrote a document")
	require.Contains(t, stderr.String(), "compliance_packs")
	// The sentence that stops a refusal being read as data loss. Everything the
	// report would have read is still recorded, and saying so is the difference
	// between somebody renewing a licence and somebody opening an incident.
	require.Contains(t, strings.ToLower(stderr.String()), "nothing has been lost")
}

func TestTheCommandRunsWithTheLicence(t *testing.T) {
	t.Parallel()
	// The other half, and without it the test above is satisfied by a command
	// that refuses everybody. This is the same invocation with one thing
	// changed, which is the only shape that proves a gate rather than a wall.
	var stdout, stderr bytes.Buffer
	gather := &gatherCounter{}

	ctx := withFeatures(context.Background(), license.FeatureCompliance)
	code := Command(ctx, []string{"soc2", "--org", "acme"}, Options{
		Stdout: &stdout, Stderr: &stderr, Gather: gather.gather,
	})

	require.Equal(t, 1, gather.calls, "the evidence was not read for a licensed report")
	require.NotEmpty(t, stdout.String(), "a licensed report produced no document")
	require.NotContains(t, stderr.String(), "not licensed")
	// NOT the configuration code, which is the one a licence refusal returns.
	//
	// The exact code here is the pack's verdict on an empty evidence set and
	// not this test's business: it is 0 when the controls pass and 6 when they
	// do not, and pinning either would make this test fail the next time a pack
	// gains a control. What matters is that it is not the code that means the
	// command refused before it started.
	require.NotEqual(t, 3, code, "a licensed report was still refused as a configuration problem")
}

// TestTheGateIsAskedPerInvocationRatherThanAtRegistration pins the reason the
// check is inside Command.
//
// A licence can lapse while a process is running. The same command object
// answering differently on two calls is what stops an installation that started
// under a valid licence keeping the feature until somebody restarts it.
func TestTheGateIsAskedPerInvocationRatherThanAtRegistration(t *testing.T) {
	t.Parallel()
	gather := &gatherCounter{}
	opts := func(out, errOut *bytes.Buffer) Options {
		return Options{Stdout: out, Stderr: errOut, Gather: gather.gather}
	}

	var out1, err1 bytes.Buffer
	Command(withFeatures(context.Background(), license.FeatureCompliance),
		[]string{"soc2", "--org", "acme"}, opts(&out1, &err1))
	require.Equal(t, 1, gather.calls)

	var out2, err2 bytes.Buffer
	code := Command(withFeatures(context.Background(), license.FeatureSSO),
		[]string{"soc2", "--org", "acme"}, opts(&out2, &err2))
	require.Equal(t, 3, code)
	require.Equal(t, 1, gather.calls, "the second call read the evidence after the licence lapsed")
}
