package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A mode on a port nothing can read inside.
//
// Everything outside HTTP reaches an environment as an opaque byte stream. The
// sidecar decides it on the server name in the TLS handshake and forwards it
// without ever seeing a request, so four of the six modes have nothing to work
// with: capture cannot record a message it cannot parse, mock cannot choose a
// fixture for a request it never sees, synth cannot describe one to a model,
// and sandbox cannot find the credential it exists to replace.
//
// The failure this refusal prevents is not a confusing log line. It is the
// shape L0.4 fixed for replicas and L4.5 then made real: a field accepted,
// silently ignored, and a manifest that says one thing while the environment
// does another. On this path the ignored version of sandbox is the worst of
// the four, because forwarding without replacing puts the application's own
// credential on the wire to the real provider, which is worse than allow and
// worse than block.

func streamRule(mode string) string {
	return minimal + `
egress:
  default: block
  rules:
    - host: af.servicebus.windows.net:5671
      mode: ` + mode + `
      credential: AF_SANDBOX
      fixtures: ./fixtures
`
}

func TestParse_RefusesCaptureOnAPortItCannotReadInside(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t, mustFail(t, streamRule("capture"))))
	require.Contains(t, msg, "port 5671 carries AMQP over TLS")
	require.Contains(t, msg, "Capture has to understand a message before it can record one.")
}

func TestParse_RefusesMockOnAPortItCannotReadInside(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t, mustFail(t, streamRule("mock"))))
	require.Contains(t, msg, "port 5671 carries AMQP over TLS")
	require.Contains(t, msg, "Mock has to understand a request")
}

func TestParse_RefusesSynthOnAPortItCannotReadInside(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t, mustFail(t, streamRule("synth"))))
	require.Contains(t, msg, "port 5671 carries AMQP over TLS")
	require.Contains(t, msg, "Synth has to describe a request to a model")
}

// Sandbox gets its own assertion and its own sentence, because the reason it
// is refused is different from the other three.
//
// The other three cannot produce an answer. Sandbox can: it can forward. What
// it cannot do is the one thing it was written for, and a rule that says
// "replace the credential" while the credential goes out untouched is a
// containment failure that reads in every report as a successful sandbox call.
func TestParse_RefusesSandboxOnAPortWhereTheCredentialCannotBeReplaced(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t, mustFail(t, streamRule("sandbox"))))
	require.Contains(t, msg,
		"Sandbox has to find the credential in the request before it can replace it")
	require.Contains(t, msg,
		"would send the application's own credential to the real provider")
}

// The control. Block and allow are the two modes this path honours, and a
// manifest that uses them on the same port must load.
//
// Without this, the suite above would pass against a validator that refused
// every rule naming port 5671, which would leave AMQP exactly as unusable as
// it was before and make the refusal a regression wearing a test.
func TestParse_AcceptsAllowOnAPortDecidedByItsHandshake(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal+`
egress:
  default: block
  rules:
    - host: af.servicebus.windows.net:5671
      mode: allow
`)
	require.Equal(t, "af.servicebus.windows.net:5671", m.Egress.Rules[0].Host)
}

func TestParse_AcceptsBlockOnAPortDecidedByItsHandshake(t *testing.T) {
	t.Parallel()
	// The default is block here, as it is in the control above, because a
	// manifest defaulting to allow is refused by a rule of its own that has
	// nothing to do with byte streams. Writing allow made this test assert
	// that older refusal instead of the one it is named for.
	m := mustParse(t, minimal+`
egress:
  default: block
  rules:
    - host: af.servicebus.windows.net:5671
      mode: block
`)
	require.Equal(t, "block", string(m.Egress.Rules[0].Mode))
}

// The second control, and the one that stops the refusal being a rule about
// the word "capture".
//
// A capture rule on an HTTP port is the ordinary case and has to keep working.
// If this went red the refusal would be matching on the mode rather than on
// the port, and every mocked provider in every manifest would stop loading.
func TestParse_AcceptsCaptureOnAnHTTPPort(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal+`
egress:
  default: block
  rules:
    - host: api.stripe.com
      mode: capture
`)
	require.Equal(t, "capture", string(m.Egress.Rules[0].Mode))
}
