# added

The event stream now keeps the shape it promised, and something checks.

The release notes listed the stream's set of types as explicitly not stable,
with the reason "types are added as features land". That says what happens when
the catalog grows and nothing at all about what happens when it shrinks, which
is the only direction that breaks anybody.

Nothing was stopping it shrinking. `schemas/events.v1.json` is generated from
the Go type and the catalog, so deleting a type or a field from the envelope
regenerates cleanly: the diff is green, every test passes, and the consumer
filtering on that type finds out on their next upgrade, receiving nothing and
unable to tell that from a quiet system.

`engine/internal/events/stream.register.json` records the fifty five types and
the eight envelope fields version 1 promised, and `just eventcheck` refuses to
let any of them go. A type that is gone, a field that is gone, a field whose
type changed, a required field that became optional, and a closed set that lost
a value are each a failure naming what went and what it costs.

It also closes a direction nothing was watching. A type declared with no entry
in `typeDocs` is absent from `AllTypes`, and every existing check walks
`AllTypes`, so an event could be emitted while being missing from the schema,
from the reference page and from every check at once. That now fails the build.

One published claim was wrong and is fixed. The description in
`schemas/events.v1.json` said the envelope was identical across the engine, the
runner and the control plane. It is not: four of the eight names differ on the
control plane's side and two have no counterpart there at all. The comment on
the Go type had said so for a while; the published artifact, which is the copy
somebody would build against, still carried the claim.

The `data` object is deliberately outside the promise, and the reference now
says so. It is the type specific payload, and its keys move with the code that
writes them.
