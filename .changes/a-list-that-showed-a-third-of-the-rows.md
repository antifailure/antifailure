# fixed

Runs, Environments and Audit showed the first page of a list and presented it
as the whole list.

All three routes paginate. `runs.recent` takes `before` and returns a
`nextCursor`, `environments.list` takes `cursor` and returns one, and
`audit.list` pages by `seq` under the name `before` and returns a bare array.
Each console page declared the cursor in its type and read none of it: the
word `nextCursor` appeared once per file, in the generic, and nowhere else.

So an organization with 200 runs saw 50, in a table that looked complete. That
is worse than a screen that looks broken, because the reader acts on it:
somebody checking whether a run happened, or whether an environment was torn
down, got a confident wrong answer.

Each list now pages, and its footer says which of the two things is true. It
renders in both states rather than hiding itself when there is no more, because
"All 24 runs." is the only place the screen ever says a list is complete.
