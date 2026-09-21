# fixed

A containment test read the application's log before the application had
written to it, and failed whoever pushed last.

`TestLogs_TheSidecarIsReadableByNameAndIsNotAService` ends with a control: a
request for every service must not include the sidecar, because the sidecar is
not a service and `af logs` is for reading the application. The control first
required that request to return something, so that an empty answer could not
pass as an absence of sidecar lines. It read it once, immediately after `Up`
returned.

`Up` returns as soon as a worker's container is running, and `waitReady` says
why in its own words: a worker is ready when it is running, and asking for more
would mean inventing a protocol the application does not speak. So at that
moment the application has printed nothing, and whether it has printed anything
by the time the control reads is decided by how long the two log reads before it
take on that machine. On a loaded daemon they cost longer than the application's
first request and the control passes. On a clean runner they do not. The whole
test took 0.85 seconds on the run that failed, against a pull request changing
three Terraform defaults, a Helm chart version and a changelog fragment, and no
Go at all.

The control now waits for the application's own end marker before reading, which
also hands it a complete log to examine rather than whatever had arrived. The
half above it needs no wait and that asymmetry is deliberate: `Up` cannot return
until the sidecar has been seen to announce itself in that very log.
