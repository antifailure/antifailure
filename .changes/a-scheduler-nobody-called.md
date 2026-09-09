# added

Multi runtime placement, which was a declared and licensed enterprise feature
with no reachable path to it.

`engine/internal/scheduler` was imported by exactly one file in the repository,
its own test. `scheduler.Plan` had zero production callers. `Run.Requires` was
filled by nothing, because `schema.Runtime` had no field that could carry a
placement requirement. The documentation described requirements matched against
runtime tags and an error code was reserved for the refusal. A licence gate in
front of any of it would have been an enforcement site that never ran.

`runtime.targets` declares the places an environment may go and the tags each
one offers, and `runtime.requires` is what a repository needs of one. The engine
calls `scheduler.Plan` to choose, and builds the runtime the chosen target
names. A requirement no target satisfies is refused with `AF-SCH-001`, which
names the requirement rather than saying no runtime is available: those are
different problems fixed by different people.

Placement is a pure function of the manifest. `af up`, `af status`, `af logs` and
`af down` each decide independently and have to agree, so a cluster's health is
deliberately not an input; a placement that varied with it would have `af status`
asking the wrong cluster and reporting that the environment does not exist.

Two consequences beyond the feature itself. A target's `region` tag fills
`extension.EnvironmentRequest.Region`, which had no writer anywhere, so the
enterprise policy hook's `allowed_regions` residency rule could be configured,
described back to the operator at startup, and never fire. And more than one
target is gated on an enterprise licence carrying `multi_runtime`, checked
through `edition.Permits` at the one point every path reaches a target, proved
by turning the entitlement off and observing the refusal rather than by reading
the check.
