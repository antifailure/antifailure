package provider

// Readiness is what a runtime can say about a service answering.
//
// THE FAILURE THIS EXISTS FOR. Readiness was a boolean, and a boolean has no
// value for "I could not check". So the local runtime returned true for any
// service with no published port as soon as its container existed, with the
// reasoning written in the code: a worker is ready when it is running, and
// asking for more would mean inventing a protocol the application does not
// speak. That reasoning is right about a worker and wrong about the way a
// compose file is written, where a service publishes no port because only other
// services reach it. Of the fifty services in the three published stacks this
// product was measured against, forty one publish no port. Every one of them
// would have been reported ready while it was still starting, and the ones that
// were going to exit on a blocked outbound call at startup would have been
// reported ready right up until they exited. Forty one green ticks and nothing
// serving is worse than a failure, because a failure sends somebody to look.
//
// Three values, and the middle one is the whole reason for the type.
type Readiness string

const (
	// ReadinessProved means a check ran and passed: an HTTP probe answered, a
	// port accepted a connection, or a command inside the container exited
	// zero. This is the only value that is a promise.
	ReadinessProved Readiness = "proved"
	// ReadinessUnproved means the service is running and there was nothing to
	// check. No published port, no health command: the runtime confirmed it
	// started and stayed started, and that is the whole of what it knows.
	//
	// It is NOT a failure. An environment whose workers are unproved came up.
	// What it is not is evidence, and the difference is what a reader needs
	// before trusting the environment for something.
	ReadinessUnproved Readiness = "unproved"
	// ReadinessFailed means a check ran and did not pass, or the container is
	// no longer running.
	ReadinessFailed Readiness = "failed"
)

// String makes Readiness printable without a cast at every call site.
func (r Readiness) String() string {
	if r == "" {
		// An empty value comes from a runtime or a recorded environment that
		// predates the three way answer. Reading it as unproved rather than as
		// proved is the conservative direction: the alternative is to report a
		// promise nothing made.
		return string(ReadinessUnproved)
	}
	return string(r)
}

// Proved reports whether readiness was established rather than assumed.
func (r Readiness) Proved() bool { return r == ReadinessProved }
