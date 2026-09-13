# fixed

The reaper's Docker test failed whenever another test tore down its own
environment while the sweep ran, and `editioncheck` reported that failure with
nothing but a package name.

`TestReaper_DestroysAnExpiredEnvironmentOnARealDaemon` required every
environment on the daemon before the sweep to still be there after it, and
required the plan over the whole daemon to name its own expired environment and
nothing else. When `go test ./internal/...` runs the env and local runtime
suites beside it, both are false for reasons that have nothing to do with the
reaper, so the edition boundary on #394 reported
`engine/internal/reaper` as COULD-NOT-LOOK: the whole suite failed it, and the
reaper alone passed it twice. With another environment starting and stopping
on the daemon every two seconds, the unchanged test failed five runs in twenty
with "the sweep removed af-churn-51327-2, which it was never asked to touch".
The same test also swept the whole daemon through the real teardown, so an
expired fixture belonging to another test would have been destroyed mid test,
and every run used the same container names, so two runs on one machine
collided.

The test now asserts only what one run can know. Its environments are named
under a prefix no other run shares. Everything the plan names must state a
lifetime that has passed, checked against the inventory without the
predicate. The sweep reaches the real teardown only for this run's own
environments, and the test requires that its expired environment is the only
one that did. Three new tests build the failing cases on purpose: a bystander
torn down in the middle of the sweep, another run's expired environment on the
daemon, and two runs at once.

`editioncheck` now prints what `go test` printed for each package it could not
decide, and for each package it finds depending on `ee`, under the package's
name. It takes that package's own block of output, so a neighbour's failure is
not printed as this one's, prints a dependent package's reproduction rather
than its first run, and cuts a block longer than 400 lines with a count of what
was cut.
