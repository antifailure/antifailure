# added

Opening a pull request check in the console showed how many workflows passed
and nothing about what the run found.

Everything a run gathers past the counts, the findings and their fixes, the
migration statements and the locks they take, the invariants and the rows that
broke them, the access probe readings, the load percentiles, reaches the control
plane through the pull request callback rather than the events stream, so none
of it is projected into the runs and verdicts tables the run page read. It lands
whole in the generation, as the report the engine already rendered for a person,
and until now nothing read it back. A check that said one workflow failed linked
to a page that said no run had reported.

The run page reads that report now, at /runs?pr=<n>, and shows it in full under
a scannable strip of the counts: the findings and their fixes, the migration
rehearsal, the invariants, the load, the reproductions folded one click away.
Nothing the run measured is dropped between the engine and the person who has to
act on it, and it needs no run row to exist, so a repository reports here the
first time its check runs.
