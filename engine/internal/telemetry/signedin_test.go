package telemetry

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// What af login is for, tested where it was missing.
//
// THE FAILURE THESE EXIST TO PREVENT. `af login` runs the device grant, a
// person approves a terminal in their browser, and the token goes into the
// operating system's keyring. Nothing then read it: `attachControlPlane` took a
// token from AF_CONTROL_PLANE_TOKEN or from a GitHub Actions identity and from
// nowhere else, so somebody who signed in, ran `af up`, and opened the console
// saw an empty environments list forever. Every part looked configured. No
// event was ever sent.
//
// It is tested here rather than in the cli package because here is where the
// question is answerable end to end: the assertion is that the event arrives at
// a control plane carrying that bearer, not that a field was copied from one
// struct into another.

// The one this fixes.
func TestASignedInTerminalReportsWithTheCredentialAfLoginStored(t *testing.T) {
	r := &runner{value: "signed.workflow.identity"}
	h := &hosted{issued: "minted-for-this-job"}

	// No AF_CONTROL_PLANE_TOKEN, and no GitHub Actions either: a laptop.
	run := runSignedIn(t, r, h, map[string]string{}, "afu_from_af_login")

	require.Len(t, run.log, 1, "the local log still gets it")
	require.Equal(t, []string{run.log[0].ID}, run.plane.ingested(),
		"the event reached the control plane, which is the whole point of signing in")
	// The bearer rather than merely the arrival: an ingestion endpoint that
	// ignored the header would let a broken credential pass this test.
	require.Equal(t, []string{"Bearer afu_from_af_login"}, run.plane.bearers())
	require.Equal(t, 0, run.runner.calls(),
		"and no workflow identity was minted, there being a credential already")
}

// The environment still wins, which keeps CI and every deliberate override
// working. A token somebody exported is a decision; a credential on the machine
// is a default, and a default does not get to overrule a decision.
func TestTheEnvironmentTokenWinsOverTheStoredCredential(t *testing.T) {
	r := &runner{value: "signed.workflow.identity"}
	h := &hosted{issued: "minted-for-this-job"}

	run := runSignedIn(t, r, h, map[string]string{
		"AF_CONTROL_PLANE_TOKEN": "from-the-environment",
	}, "afu_from_af_login")

	require.Equal(t, []string{"Bearer from-the-environment"}, run.plane.bearers())
}

// And the stored credential wins over the workflow identity, which only
// arises on a self hosted runner somebody has also signed in on. Either would
// work; using the one a person chose means the run is attributed to them.
func TestTheStoredCredentialWinsOverTheWorkflowIdentity(t *testing.T) {
	r := &runner{value: "signed.workflow.identity"}
	h := &hosted{issued: "minted-for-this-job"}

	run := runSignedIn(t, r, h, map[string]string{
		identityURLEnv:   "present",
		identityTokenEnv: "the-runners-request-token",
	}, "afu_from_af_login")

	require.Equal(t, []string{"Bearer afu_from_af_login"}, run.plane.bearers())
	require.Equal(t, 0, run.plane.exchangeCount(), "nothing was exchanged")
	require.Equal(t, 0, run.runner.calls())
}

// A machine nobody has signed in behaves exactly as it did before this existed.
// Worth a test of its own: the change is a new source of credentials, and the
// way a new source breaks things is by producing one where there was none.
func TestNoCredentialAnywhereStillReportsToNobodyAndDoesNotFail(t *testing.T) {
	r := &runner{value: "signed.workflow.identity"}
	h := &hosted{issued: "minted-for-this-job"}

	run := runSignedIn(t, r, h, map[string]string{}, "")

	require.Empty(t, run.plane.ingested(), "no credential, no report")
	require.Empty(t, run.plane.bearers())
	require.Len(t, run.log, 1, "and the run is unaffected and still logged locally")
	require.Equal(t, 0, run.runner.calls())
}
