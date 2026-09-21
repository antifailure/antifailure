# added

Fault injection was reachable from `af chaos` and from nowhere else, and the
gate that was written for exactly this caught it within hours.

`inject_declared_faults` puts it on the MCP server. An agent asks for the
manifest's declared faults to be injected into the environment running for its
branch, one at a time, each undone before the next begins, and reads back what
the system did about each one: whether the fault landed, the evidence the
injector recorded at the instant it acted, whether the undo ran, and, around a
fault aimed at the database, whether every commit the client was told was
committed is still there and whether anything is there that no client ever
wrote.

The result carries `held` and `verified` separately, and the second is not the
negation of the first. Held says nothing was found to be wrong. Verified says
the run established what it set out to. A run that is held and not verified has
not passed, it has not looked, and it is reported `INCONCLUSIVE`. A fault that
was applied and changed nothing is refused rather than reported as survived.

There is no argument that chooses which faults run, aims one somewhere else,
makes one gentler, or turns the durability proof off. A caller that could weaken
a fault could make the check easier on itself, which is the one thing this whole
surface promises it cannot do.

The decision behind `held` and `verified` moved to `engine/internal/gate`, where
the migration evaluator already lives and for the same reason: it was in
`engine/internal/cli`, the MCP package cannot import that, and the alternative
was a second implementation of the split this feature rests on. Three callers
read one answer now.

This is the second capability the surface gate has named. `af load sql` was the
first, and it had been reachable from one surface for as long as it existed;
this one was reachable from one surface for six hours, because two pull requests
were each green against a main that did not yet have both of them.
