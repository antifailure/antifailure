package provider

import (
	"context"
	"net/http"
)

// Emulator is a third party service answered inside an environment, as an
// application meets it.
//
// extension.Emulator DECLARES one: a name, the hosts it answers for, and a
// container pinned by digest. This is the running thing that declaration
// produces, and it is a separate interface because none of the promises the
// engine makes about an emulator can be asked of a declaration. A digest and a
// host list say what will be started. They say nothing about whether an
// unimplemented operation is refused, whether the state is enumerable, or
// whether the container can reach the internet, and those are the three things
// somebody is trusting when they read a green run.
//
// The subject is the emulator AS ROUTED, not the emulator binary. Every
// behaviour the conformance suite checks is about the whole path: the sidecar
// terminating TLS for the provider's own hostname, the rule matching, the
// forward, and the container answering. Testing the container alone would
// prove that LocalStack works, which Amazon's own users already know, and
// would prove nothing about the claim this product actually makes.
//
// Antifailure does not write emulators. Nothing here is a place to put one.
type Emulator interface {
	// Name is the emulator's name, as the registration and the egress rule
	// both spell it.
	Name() string

	// Hosts are the hostnames it answers for, such as s3.amazonaws.com.
	//
	// The same list the registration declares. It is on this interface too
	// because the suite sends requests to them, and a suite that took the
	// hosts from somewhere other than the thing under test could pass against
	// a host nobody routed.
	Hosts() []string

	// Capabilities says what to send and what to require, because the suite
	// cannot know either.
	Capabilities() EmulatorCaps

	// RoundTrip sends one request the way the application does: to the
	// provider's own hostname, over the route the environment provides, with
	// no endpoint override anywhere.
	//
	// That is the claim, so it is the transport. An implementation that
	// pointed this at the emulator's address directly would pass every
	// behaviour below and prove none of them.
	RoundTrip(ctx context.Context, req *http.Request) (*http.Response, error)

	// State enumerates what the emulator currently holds.
	//
	// Buckets, queues, tables, blobs: whatever the emulator's own listing
	// operations return. It exists because an emulator is the one thing in an
	// environment that holds state nothing else can see, and state nobody can
	// list is state nobody can reason about after a run.
	State(ctx context.Context) ([]EmulatorStateItem, error)

	// Reset discards everything State would list.
	//
	// It is here so that the suite can prove the teardown promise without
	// tearing an environment down, which would end the run. Teardown removes
	// the container and the state goes with it; this is the same promise
	// asked of a live emulator.
	Reset(ctx context.Context) error

	// Reach attempts an outbound connection FROM the emulator container to an
	// address outside the environment, and returns the error that stopped it.
	//
	// A nil return is a failure, and it is the one failure here that is a
	// security finding rather than a bug: an emulator with a route out is a
	// container running a third party image, holding a copy of the
	// application's requests, able to post them anywhere.
	Reach(ctx context.Context, address string) error

	// Close releases what the handle holds. It does not tear the environment
	// down.
	Close() error
}

// EmulatorCaps is what an emulator declares about itself so the suite knows
// what to send and what to require.
//
// Declared rather than discovered, for the same reason a database provider
// declares its capabilities: the alternative is a suite that guesses an
// operation name per cloud, and a guess that misses reads as a pass.
type EmulatorCaps struct {
	// Covered is a request the emulator implements: an operation inside the
	// surface its documentation claims. The suite requires that it is
	// answered and, separately, that it is NOT refused.
	Covered EmulatorProbe
	// Uncovered is a request outside that surface. The suite requires that it
	// is refused in the provider's own error shape rather than answered with
	// something plausible, because a silent wrong answer from an emulator is
	// worse than a refusal: it will be believed.
	Uncovered EmulatorProbe
	// ErrorContentType is what the provider's own error responses carry.
	// "text/xml" for AWS and Azure, "application/json" for Google.
	ErrorContentType string
	// ErrorCode is the string the provider's error body carries for an
	// operation that is not implemented, such as "NotImplemented" or
	// "FeatureNotSupportedByEmulator". The suite looks for it in the body of
	// the uncovered probe's response, and requires it ABSENT from the covered
	// one.
	ErrorCode string
	// LiveCredential is an Authorization header value built from a credential
	// that works against the real provider, for the tripwire behaviour.
	//
	// It is a TEST vector shaped like a live key and never a live key. AWS
	// publishes AKIAIOSFODNN7EXAMPLE for exactly this purpose, and the
	// detector recognises the shape rather than the account.
	LiveCredential string
}

// EmulatorProbe is one request the suite sends.
type EmulatorProbe struct {
	// Host is which of the emulator's hosts to send it to. Empty uses the
	// first one, which is wrong often enough that it is worth naming: a
	// virtual hosted bucket lives on a host of its own.
	Host string
	// Method is the HTTP method. Empty means GET.
	Method string
	// Path is the request path, beginning with a slash.
	Path string
	// Query is the raw query string, without the leading question mark. The
	// AWS query protocols put the operation here.
	Query string
	// Header are extra headers, such as the operation name a JSON protocol
	// puts in X-Amz-Target.
	Header http.Header
	// Body is the request body.
	Body []byte
	// Creates reports whether this probe leaves something behind that State
	// lists.
	//
	// Only meaningful on Covered, and false is a legitimate answer: a read
	// only operation is a perfectly good thing to be covered by, and a probe
	// that claimed to create something and did not would fail a behaviour
	// about the emulator rather than about itself.
	Creates bool
}

// EmulatorStateItem is one thing an emulator holds.
//
// Two fields and no more. The suite counts these and requires them gone after
// a reset; it never interprets them, because the vocabulary of a bucket, a
// queue and a Kafka topic have nothing in common and a suite that tried to
// unify them would be inventing a cloud.
type EmulatorStateItem struct {
	// Kind is the emulator's own word for what this is: "bucket", "queue",
	// "table".
	Kind string
	// Name identifies it within that kind.
	Name string
}
