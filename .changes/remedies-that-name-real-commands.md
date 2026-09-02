# fixed

An error remedy told the user to run a command that does not exist. AF-DB-004
said to run `af golden list` and then `af up --golden <version>`, and `af up`
has `--branch`, `--hud` and `--rebuild`. The catalog is what the engine prints,
so this was the product telling somebody, at the moment they were already stuck,
to run something that would fail. The remedy was also incoherent even had the
flag existed: it named a version the error had just said no longer exists. It
now says to look at what does exist and to make one if nothing does.

Two more of the same class in the documentation. `self-hosting/operations.md`
told an operator in bold, during an incident, not to run `af down --all`, which
is not a flag; the command that removes every environment on the machine is
`af env prune --older-than 0`. `guides/dashboard.md` said the dashboard draws
the same stream `af events` reads, and there is no `af events`; the stream is
the NDJSON log under `.antifailure/logs`.

`TestEveryCommandInTheDocsExists` existed for exactly this class and was green
over all three. Its pattern is anchored to the start of a line, which is true of
a command in a fenced block and false of one in a sentence, so every one of the
127 remedies on the generated errors reference was outside what it could see. It
reads inline code spans and quoted commands now, and a new sweep checks
`catalog.yaml` itself, so the next one fails where it is written rather than one
page downstream.
