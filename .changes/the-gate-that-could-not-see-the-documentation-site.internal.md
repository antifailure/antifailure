# fixed

`CLAUDE.md` said `tools/prosecheck` "enforces it across every tracked file".
It never has. The checker keeps two lists, and neither one named the
documentation site: `documents` held `docs/src/content/docs`, which is only the
part of `docs` that Astro renders, and `sources` held `www` and `console` and
not `docs` at all. So the ADRs, the RFCs, the design documents and the plan
notes sitting beside the content collection were read by nothing, and so was
every TypeScript file the documentation site is built from.

One violation was living in that gap on main. `docs/src/pages/llms-full.txt.ts`
assembles the plain text twin of the entire manual, and the sentence at the top
of it saying what the route serves carried an em dash. It is the only literal
one that was anywhere outside this checker's reach, which is the reason to fix
the instrument rather than the file.

Both lists now name `docs`. That found eighteen more defects, all in
`docs/plan/notes`, all of them the plain shape the rule exists for, and none of
them a case where the character was doing a job. So the widening cost nineteen
prose edits and bought no exemption list, which is the outcome the comment
above `banned` asks for before one gets built.

The double hyphen half of the rule stops here and that is a limit rather than
a todo, so it is written into `main.go` with the measurement behind it. `--` is
the SQL line comment and the POSIX end of options marker. Of the 1242 lines
outside this checker's reach that match the rule, 1120 are SQL comments, and a
further 3982 begin with the sequence in the first column where the rule cannot
see them at all. Roughly a hundred genuine defects sit in Go and TypeScript
comments that this instrument will never reach, and eight more lines could
never be changed even so: two ASCII table rules, two SQL injection payloads
whose entire point is that `--` opens a comment, a captured Postgres error
string, the Vale rule that names the sequence, and two of this checker's own
fixtures. Reaching them needs something that can tell a comment from a
statement in six languages, which is a parser.

Three new tests and two sharpened ones, each watched failing first. The two
that check the real repository now name the trees they expect to reach instead
of counting files, because a list that quietly stops covering one tree still
clears a threshold, and that is exactly how the plan notes stayed unread under
a green test. One test asserts the limit: an indented SQL comment in a
migration is not a defect. That one was written flush left first and PASSED
against the widening it exists to refuse, because a `--` in the first column
has no character before it to match, so it was testing column zero rather than
scope.
