# fixed

`just generate` could not finish the job in one pass, and the tree it left
behind failed the generated file gate while every generator in it had just
reported success.

`docsembed` embeds every documentation page into the copy the MCP server
serves. Six of those pages are themselves generated: `errors.md`, the schema
reference, `cli.md`, `lint-findings.md`, `transforms.md` and the dashboard
guide. `docsembed` ran sixth, ahead of four of the generators that write them,
so one pass embedded the previous wording and a second pass was needed to
settle. It was invisible until one of those pages actually changed, which is
why it survived: the ordering is only wrong on the day somebody edits a schema.

`docsembed` now runs last, after every generator that writes under `docs`. The
rule is derived from the ledger that already records which generator owns which
path, so a seventh generated page extends it without anybody remembering to.
