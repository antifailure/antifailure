package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// replicas, once it is read.
//
// The field spent one release refused outright, because nothing read it and a
// manifest asking for three instances got one in silence. Both runtimes honour
// it now, so the refusal is gone and what is left are the two cases a runtime
// cannot rescue: a count that is not a count, and a count on a service where
// running more than one of it is the bug rather than the point.

func TestParse_AcceptsAServiceAskingForThreeInstances(t *testing.T) {
	t.Parallel()
	// The value the whole field exists for. Three is what somebody writes to
	// reproduce a bug that only happens at more than one instance, and one
	// instance is what they used to get.
	m := mustParse(t, minimal+"    replicas: 3\n")
	require.Equal(t, 3, m.Services[0].Replicas)
}

func TestParse_LeavesAnUndeclaredCountAtZero(t *testing.T) {
	t.Parallel()
	// Zero rather than one, and deliberately. Normalization does not fill the
	// number in, so the normalized manifest of a document that says nothing
	// about instances is byte for byte the one it was before instance counts
	// existed: that document is the image cache key and the input a fidelity
	// report is built from. Zero is read as one at the point of use, in
	// provider.ServiceSpec.Instances, and in exactly one place.
	m := mustParse(t, minimal)
	require.Zero(t, m.Services[0].Replicas)
}

func TestParse_RefusesZeroInstances(t *testing.T) {
	t.Parallel()
	// Written zero and omitted key are different statements and normalization
	// is what would make them the same. An omitted key says the author never
	// thought about it, and one instance is the answer. A written zero says
	// they want none, which is a service to delete rather than one to start
	// quietly anyway.
	msg := messages(problems(t, mustFail(t, minimal+"    replicas: 0\n")))
	require.Contains(t, msg, `Service "web" asks for 0 instances.`)
	require.Contains(t, msg, "The count runs from 1 to 10")
}

func TestParse_RefusesMoreInstancesThanTheBound(t *testing.T) {
	t.Parallel()
	// An environment is a copy of production on one developer's machine.
	// Forty instances of a worker reproduces nothing and exhausts the machine,
	// and the runtime failure that follows looks like the change under test.
	msg := messages(problems(t, mustFail(t, minimal+"    replicas: 40\n")))
	require.Contains(t, msg, `Service "web" asks for 40 instances.`)
}

func TestParse_RefusesMoreThanOneInstanceOfACronService(t *testing.T) {
	t.Parallel()
	// A scheduled job is a side effect its author expects to happen once, and
	// three instances of it happen three times: three invoices, three charges,
	// three nightly emails. Reproducing that on purpose in a twin is useful.
	// Shipping it as the meaning of a manifest key is not.
	body := `
version: 1
name: shop
services:
  - name: nightly
    kind: cron
    schedule: "0 3 * * *"
    command: send-invoices
    replicas: 3
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `Service "nightly" is a cron service and asks for 3 instances.`)
	require.Contains(t, msg, "three instances send the nightly email three")
}

func TestParse_AcceptsOneInstanceOfACronService(t *testing.T) {
	t.Parallel()
	// The control on the refusal above. A cron service asking for the one
	// instance it was always going to get is not making a promise the engine
	// cannot keep, and a rule that refused it would be refusing the word
	// rather than the outcome.
	body := `
version: 1
name: shop
services:
  - name: nightly
    kind: cron
    schedule: "0 3 * * *"
    command: send-invoices
    replicas: 1
`
	m := mustParse(t, body)
	require.Equal(t, 1, m.Services[0].Replicas)
}
