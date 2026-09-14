# changed

A manifest may declare two services listening on the same port. The
validator refused that pair with "both claim port", so a Next.js application
beside PostgREST, which both listen on 3000, was a manifest Antifailure would
not load, and `af init` refused to write one for a compose file that runs
them side by side. Neither runtime shares a port between services: the local
runtime publishes each service on a host port it allocates for that service,
and the Kubernetes runtime gives each its own Deployment, Service and
hostname. The port in the manifest is what the service listens on inside its
own container, and that is all it has ever been.

A runtime that did put two services into one network namespace would break
this, so the runtime conformance suite now checks it:
`Up_ServicesOnOnePortAreEachReachable` starts two web services on one
container port and requires each to answer with its own response at its own
reported address.
