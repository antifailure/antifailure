# fixed

A manifest with no `database.source_url_env` got an environment that looked
exactly like one on a masked copy of production. The migrations built the
schema, the workflows ran against no rows, the report said which passed, and
nothing anywhere said the database was empty. A golden built from nothing and
a golden masked from production produced the same progress lines, the same
events and the same green comment.

`af up` now prints one sentence when the golden it branches holds no production
data, and `af ci` writes the same sentence in bold before the workflow table:
this ran on an empty database, the migrations built the schema, no production
data was masked or branched, set `database.source_url_env` and add the secret.
The answer comes from the provenance the golden was selected by, so a golden
reused from an earlier run answers for what it holds rather than for what the
manifest says today. The report's JSON carries it as `empty_source`, and the
`golden.refreshing` event carries it as a field.

A golden with a seed command is not called empty, because the seed put rows in
it and the sentence would be false.
