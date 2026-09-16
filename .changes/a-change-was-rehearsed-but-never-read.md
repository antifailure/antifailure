# added

A static code reviewer that reads the change's added lines for the correctness
defects a diff introduces.

Every lane the product had rehearsed a change against a twin: it built the
environment, ran the workflows, drove the security families, and reported what
the change DID when it ran. Nothing read what the change WAS. A correctness bug
that no workflow happened to exercise reached no verdict at all: an off-by-one
on the last element, a nil dereference on a path the diff just added, an error
that is checked and then dropped, a boundary the new code does not hold, a new
function nothing calls, a value decoded as the wrong shape, an ordering hazard.
These are the bugs a senior reviewer catches by reading a pull request, and the
product could not read one.

`af ci` now runs the reviewer as one more collector beside migration, egress,
masking, load, cleanup and the security families. It reads the diff and not the
twin, so it runs before the environment is brought up and is worth having even
on a run whose environment never started. It sends the change's added lines,
with the line numbers they carry in the new file, to the model the user already
configured, and maps what comes back into ordinary findings that ride the same
verdict, exit code and pull request comment as everything else. A finding points
at a line the author just wrote.

It is advisory by default. The reviewer is an LLM reading a diff and its
findings are probabilistic, so a `review` finding defaults to warn rather than
fail: it advises without blocking a merge on a model's say-so, and a project
that trusts it raises `policy.review` to fail. The level comes from the manifest,
never from the collector.

It is honest about absence. With no model key configured the reviewer is skipped
and the run says so, in the words a person can act on, rather than reporting a
clean pass it never earned. A docs-only or configuration-only change touches no
code surface, so no reviewer runs, no model is called, and nothing is spent. A
change the router cannot read, a diff the reviewer cannot fetch, a model that
could not be reached, and a model that answered with something unreadable are
all recorded as notes and produce no finding, because a gap in our tooling is a
fact about us and must never redden somebody's build.

The call carries the user's own source to the user's own provider with the
user's own key, the same BYOK trust model `af model test` uses, and nothing
Antifailure hosts is involved. It goes through the air gap guard under its own
site, so an air gapped installation refuses it at the dial rather than reaching
a provider it was sealed away from, and the ledger attributes the attempt to the
code reviewer by name.
