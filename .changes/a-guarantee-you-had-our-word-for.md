# added

The exit condition for a twin with two stores is a sentence said to a customer
about their own environment: a masked, verified, attested clone of your Postgres
and your ClickHouse, with the same person masked identically in both, and a
report that tells you what it did not reproduce.

Six of those seven clauses had a command behind them. `af mask plan`, `af mask
verify`, `af golden verify`, `af fidelity`, `af invariants` and `af insights`
all exist and are documented. The clause about one person masking to one person
across both stores did not. `masking.CrossStoreCheck` had been written, was
tested well, and had **zero production callers**: every caller was a test or a
benchmark, the phrase cross store appeared nowhere in the command reference, and
no tool mentioned it.

So the guarantee was true in our continuous integration, on our fixtures, and on
a customer's stack it was something they had our word for. That is the sentence
this product exists to refuse to say. Determinism across stores is a property of
the masking construction, so their twin almost certainly had it, and almost
certainly is exactly the standard being rejected here.

`af mask crossstore` is the surface. It reads each declared datastore's catalog,
assigns the one rules file to all of them, finds every identifier that appears
in more than one store, masks probe values through each side, and reports the
share that come out identical.

**It reads schemas and no rows.** That is not an implementation detail, it is
what makes the command safe to point at production: the probe values it masks
are its own, so what it needs from a store is the catalog. The report says how
many tables and columns it read and that it read no rows, as a field rather
than as a promise in the description.

And the promise is enforced by something that can contradict it. A live test
runs the check against a real ClickHouse and then reads the server's OWN
`system.query_log` back, failing if any statement the check sent selected from
a data table. A sentence saying no rows are read is something anybody can
write. A query log the server keeps is not.

Every store it could not read is named with the reason, every store that names
no `source_url_env` is named as never read at all, and a run that reached one
store reports that it proved nothing rather than reporting a hundred percent of
one. The two ways of not passing get two different exit codes, because a pair
that disagreed is a statement about the data and a run that compared nothing is
a statement about what could be reached, and telling somebody their stores
disagree when the truth is that nobody looked would be its own defect.

Datastores gain `source_url_env`, the name of the variable holding a read only
connection string for that store. The primary takes it from
`database.source_url_env` and does not repeat it. A connection string pasted
into that field is refused, and the refusal does not print it back.

The fidelity report gains the line where two stores are present. Nothing having
compared them is unmeasured, which keeps it out of the score in both directions,
carries the sentence saying how to check, and does not invent a finding about an
environment nobody looked at. Verified identical is reproduced. A pair that
disagreed is absent with the pair named, which is the line the whole thing is
for: one identity masked into two people is a twin that is confidently wrong,
and every report built on it is plausible.

**A project that lists `datastores` in `fidelity.require` and holds two stores
will see that requirement go from met to unmeasurable** until each store names a
`source_url_env`. That is not a regression, it is the point: `require` means
every component of the dimension was measured and reproduced, and the dimension
now carries a component that nothing had measured. Reporting it as met would be
the report asserting a guarantee that nothing checked. Unmeasurable is a
distinct outcome from broken and is reported as one, so nothing fails as though
the stores had been compared and disagreed.

The same question is the fourth question `inspect_data_masking` answers, so an
agent can ask it and get a verdict of PASS, FAIL or INCONCLUSIVE rather than a
percentage it has to interpret.

Two stores agreeing while a third declared store could not be read is not a
pass either, and it carries the coverage code rather than the disagreement one.
Nobody looked at the third store, and telling somebody their stores disagree
when that is the truth would be the same conflation one level up. A store that
names no `source_url_env` is deliberately not counted there: nobody asked for
it to be compared, a cache holds no identity to compare, and requiring one
would make a pass impossible for every realistic manifest. It is named in the
report instead of becoming a verdict.

The catalog readers are the ones that already ship. The ClickHouse half is
`clickhouse.Catalog`, exported from the provider that refreshes, masks and
branches the store, and it is scoped to the database the connection string
names. A second reader written for this check would have been a second opinion
about what a type means and whether a column can be written to, kept in step
with the first by nobody, and the failure mode of a stale one is a check
comparing a plan that will never run.
