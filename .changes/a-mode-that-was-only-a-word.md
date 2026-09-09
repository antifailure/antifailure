# added

Air gapped mode, which the licence had been selling as a word.

`air_gapped` was one of twelve features an enterprise licence can carry, and
grepping the repository for it returned two lines: the constant that declares
its own name and the entry in `AllFeatures` that lists it. Nothing read it. An
installation running with `air_gapped` in its licence checked GitHub for a new
release, exported telemetry, pulled the Postgres and ClickHouse images from
Docker Hub, built the sidecar image from a base image it fetched, asked a model
provider to invent HTTP responses for any rule in synth mode, and forwarded the
application's own traffic to the real internet for every rule in allow and
sandbox mode. It behaved exactly as an installation without the feature did.

`AF_AIR_GAPPED=1` now seals the process. Every outbound client in the engine and
the enterprise edition dials through one guard, which refuses any address the
operator did not name in `AF_AIR_GAPPED_ALLOW`, records the attempt, and refuses
a hostname before resolving it so the refusal does not leak the name over DNS.
Loopback and unix sockets are always permitted, because the sidecar, the local
Postgres and the Docker daemon are addressed there. A private range is not
permitted implicitly: an internal registry is reachable because it was named,
not because 10.0.0.0/8 looked harmless.

The variable set without a licence for the feature stops the binary rather than
starting it unsealed, because an installation that believes it is air gapped and
makes one call it did not expect is the failure the feature exists to prevent,
and the belief is the part that does the damage. Once sealed nothing unseals it,
including a licence that lapses overnight.

An environment whose egress rules would reach outside is refused before it is
created, naming every rule. `allow` forwards to the real host, `sandbox`
substitutes a test credential and still forwards to the real host, and `synth`
calls a model provider. `block`, `capture` and `mock` are answered inside the
environment and are untouched. Refused rather than quietly downgraded, because
an environment switched from `allow` to `block` behind the operator's back would
report that it tested a code path it never reached.

The deliverable is the measurement, not the flag. A test seals the guard and
performs a complete lifecycle on real Docker, bringing an environment up,
serving a request through it and tearing it down, then reads the ledger. It
asserts zero refusals AND, separately, that the readiness probe is in the
ledger, because zero refusals out of zero observations is not a measurement and
would read the same on a build where the guard was never on the path.
