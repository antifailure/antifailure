# added

The manifest's own `invariants` are now evaluated against the recovered
database, as an arm of the chaos verdict.

Until this, the durability proof asserted only over a schema of the engine's
own, for the reason `engine/internal/pgcrash/workload.go` gives: asserting that
a table somebody else is writing did not change, while they are writing it, is
a claim about a moving target. That reason expires the moment the writers stop
and the database answers a query again. Meanwhile `af ci` ran the invariants
early and the chaos run last, so the rules a project states about its own data
were never once evaluated against a database that had just been crashed.

Each invariant is asked twice, before the fault and after the recovery, because
one answer cannot be read on its own. An invariant that held before and does
not hold after is `chaos.invariant.broken_by_fault` and it fails. One that did
not hold before either is `chaos.invariant.already_violated`, reported and
attributed to nothing, because the run inherited it broken. One that could not
be asked on either side is `chaos.invariant.unevaluated`, which a database that
never came back is the loudest case of. A project that declares no invariants
sees no change at all.
