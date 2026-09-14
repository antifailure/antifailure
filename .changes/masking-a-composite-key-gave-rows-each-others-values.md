# fixed

Masking a table whose primary key has more than one column gave its rows each
other's masked values.

The masking run addressed a row by the first column of its primary key alone,
compared as text. For a key such as `(tenant_id, id)`, the update that wrote one
row's masked values matched every row of that tenant, and the next chunk resumed
after the tenant, so its other rows were never masked on their own. On a table
of three tenants with seven contacts each, 18 of the 21 rows ended up holding
another row's masked email. No row kept its original value, and golden
verification reported nothing, because every value it sampled was a valid fake
address. Every join on those columns found the wrong person, and a masked column
with a unique constraint would have stopped the run halfway through the table.

Rows are now read, paged and rewritten by the whole primary key, compared as the
table stores it, so the key's index serves every statement. On 200,000 rows a
chunk read went from 78.5 milliseconds to 3.7, and a row update from 13
milliseconds to 2.7. Before the change, the first 20,000 row chunk of that table
had not committed after 45 minutes. A table with no primary key is now updated
through a TID scan rather than a scan of the whole table for each row. ClickHouse
masking was not affected, because it rebuilds a table through a shadow copy
rather than rewriting rows one at a time.

A column marked `preserve` was written back over itself. The masking statement
set every preserved column to the value it already held, the primary key
included, so every row got a new version and every index on those columns a new
entry, for no change. A table whose every column was preserved was rewritten in
full. Preserved columns are now recorded as reviewed and found safe, shown that
way in `af mask plan` and its JSON, and never written, and a table with nothing
else gets no statement at all. Verification still counts them as covered by a
rule. The same reading refused two plans outright, a `preserve` rule on a
generated column and a `preserve` rule on a ClickHouse table with no sorting
key, and neither is refused now.

A policy that requires a column to be masked, such as the enterprise
`required_masked_columns` policy, now refuses a plan whose only rule for that
column is `preserve`. A preserved column holds exactly what production held, and
it was listed among the masked columns the policy was given, so such a plan used
to be approved. If a golden refresh that passed before is now refused naming a
column, give that column a masking transform, or ask an administrator to narrow
the policy so it no longer requires it.

Pressing Control C during `af golden refresh` printed `AF-MSK-010 Masking could
not run: masking: writing public.customers: unexpected EOF` and exited as a
configuration error. An interrupted run now reports AF-MSK-016 and exits with
status 9. It names the table it was rewriting and how many rows were written and
committed, and says the chunk in flight was rolled back. Running a refresh again
is safe, because it starts from a fresh copy of the source. `af mask apply` says
its branch is now partly masked and has to be recreated with `af down` and then
`af up` before masking again, because masking a value that is already masked
changes it. The local state
directory's README no longer says an interrupted masking run resumes.
