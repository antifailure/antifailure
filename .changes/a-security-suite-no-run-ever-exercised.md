# added

A security suite that no rehearsal ever ran.

The contract had shipped: a Family interface, a registry, the policy keys, the
exit codes, and read_security_findings. Nothing called Probe. A family could be
written, registered, and documented and still never run against a change,
because the one place a run assembles its findings, ci's finish, had a collector
for the migration, the egress, the masking, the load, and the cleanup and none
for security. The whole suite was capability that looked shipped and did
nothing, the shape this repository calls a dead, shippable gap.

af ci now routes the change through the family registry, runs each selected
family's Probe against the targets the diff touched while the twin is still up,
gates each on its edition feature at the probe call site, and folds the findings
into the same run.Findings the verdict, the exit code, the pull request comment,
and read_security_findings already carry. A family whose surface the change did
not touch is not run, an unlicensed one is skipped with a note that names the
feature rather than dropped, and the empty registry the spine ships produces no
finding and does no work at all. The reader families read what the run captured,
the egress and message logs, the browser evidence, and a base twin's counts,
through additive accessors on the family Input that report absent rather than
empty, so a reader that was handed nothing fails closed instead of passing.
