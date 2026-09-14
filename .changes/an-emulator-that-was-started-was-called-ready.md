# fixed

An emulator that had been started was called ready, and the application got a
502 from it.

`af up` started the emulator containers, then the sidecar, then the application.
The daemon reports a container started as soon as its first process runs, and the
server inside binds its port later: the Google emulators take between 17.7 and
51.5 seconds to accept their first connection, measured at first bind in
`guides/gcp.md`, and LocalStack spends its own seconds loading providers. So the
application started while the port was closed, its first call reached the
sidecar, the sidecar forwarded it to an address nothing was listening on, and the
application read `502 Bad Gateway` from its own SDK.

Nothing about that pointed at the cause. The engine had already printed
`emulator ready`. The 502 is the same status the sidecar returns for an emulator
the environment is not running at all, so the one message anybody had said the
opposite of what was wrong. And it only happened when the emulator was slow: ten
consecutive runs of the end to end test passed on a warm laptop while the same
test failed in CI.

`af up` now dials each emulator from inside the environment until it accepts a
connection, before any service is created. The dial has to come from in there,
because an emulator joins the inner network and nothing else, so it publishes no
port and the host has no route to it; the sidecar is the one container on that
network the engine can reach, and the probe is the sidecar's own binary run
inside it. Each emulator has three minutes, `AF_EMULATOR_READY_TIMEOUT` moves
that, and one that never binds stops the run with `AF-RUN-049` naming the
emulator, its address and its image. That environment is torn down rather than
left standing, because every call it would answer is the same misleading 502.
The progress line now says `started` where it said `ready`, and says `ready` once
the emulator has answered.

`af logs af-proxy` returns the sidecar's log. It returned nothing at all, with no
error, because the sidecar is not a service and every kind but service was
skipped. The one place a refusal or a 502 is written down was the one place
nobody could read, including the end to end test that prints it on failure: on a
real 502 it printed the word `sidecar:` followed by nothing. A request for every
service still leaves the sidecar out, because that output is the application's.
