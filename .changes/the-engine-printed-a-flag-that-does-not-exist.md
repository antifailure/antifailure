# fixed

`af init` writes a README into `.antifailure` telling the reader that if they
delete the journal while an environment is running they should run
`af down --all`. There is no `--all`. That is the third instance of that exact
flag and the most durable, because it is written to a file on disk rather than
printed once. It now names `af env prune --older-than 0`, which inventories the
provider rather than reading the journal, so it can still find what the journal
no longer names.

Neither the catalog sweep nor the documentation sweep could see it, because it
is a Go string literal. A third sweep now reads every string constant the engine
and its error package hold, through `go/parser` rather than a pattern, and
checks the flags against the real command tree.

Scoped to flags, and the limit is recorded beside the assertion rather than in a
report. A bare invented command name cannot be told apart from prose in a string:
"run `af down` again", "`af golden refresh` has nothing to compile" and
"`af down` clears it" are ordinary sentences beginning with a real command, and a
rule that read the next word as a subcommand reported about a third of its
findings falsely when that was measured. A flag is unambiguous, because prose
does not contain double-dashed words, and the flags are where all three live
defects were.
