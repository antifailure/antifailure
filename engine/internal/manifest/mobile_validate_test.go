package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The mobile application, which is the half of the phone surface that makes it
// reachable rather than merely nameable.
//
// THE DEFECT THESE EXIST FOR is the desktop one, one surface over, and it had
// shipped. `surface: ios` was accepted by the schema, passed by the validator
// and selected the iOS driver, and the runner refuses a phone run that does not
// name the application's identifier. Nothing in the manifest had a field for
// one, so every iOS run was refused in the runner, after an environment had
// been built. desktop_validate_test.go says what the pairing below is for.

const mobileApp = `mobile:
  id: com.example.ledger
  app: ./build/Ledger.app
`

const aPhoneWorkflow = `workflows:
  - name: read-the-journal-on-the-phone
    surface: ios
    description: Open the journal on the phone and check that it lists transfers.
    persona: alice
    expect: ["transfer.posted"]
`

// A workflow on a phone and nothing saying which application. Refused while the
// manifest is read, rather than after an environment has been paid for.
func TestParse_APhoneWorkflowWithNoApplicationIsRefused(t *testing.T) {
	t.Parallel()
	_, err := parse(t, withPersonas+aPhoneWorkflow)
	msg := messages(problems(t, err))
	require.Contains(t, msg, "A workflow drives a phone and no mobile application is declared.")
	require.Contains(t, msg, "Add a `mobile` block")
}

// The quieter direction: an application nothing drives is a block nobody
// reads, and a run that opened a browser instead would say nothing about it.
func TestParse_AMobileApplicationNoWorkflowDrivesIsRefused(t *testing.T) {
	t.Parallel()
	_, err := parse(t, withPersonas+mobileApp+`workflows:
  - name: checkout
    description: Buy one item and see the order confirmed on the screen.
    persona: alice
    expect: ["The order is confirmed."]
`)
	msg := messages(problems(t, err))
	require.Contains(t, msg, "A mobile application is declared and no workflow drives it.")
}

// The liveness arm: a manifest with no phone at all is caught by neither rule,
// so both refusals above cannot be satisfied by a validator refusing everything.
func TestParse_AManifestWithNoPhoneAtAllIsUntouched(t *testing.T) {
	t.Parallel()
	m := mustParse(t, withPersonas+`workflows:
  - name: checkout
    description: Buy one item and see the order confirmed on the screen.
    persona: alice
    expect: ["The order is confirmed."]
`)
	require.Nil(t, m.Mobile)
}

// The pairing, accepted, every field surviving under the name the engine reads.
func TestParse_AMobileApplicationAndItsWorkflowAreAccepted(t *testing.T) {
	t.Parallel()
	m := mustParse(t, withPersonas+mobileApp+aPhoneWorkflow)
	require.NotNil(t, m.Mobile)
	require.Equal(t, "com.example.ledger", m.Mobile.ID)
	require.Equal(t, "./build/Ledger.app", m.Mobile.App)
	require.Equal(t, schema.SurfaceIOS, m.Workflows[0].Surface)
}
