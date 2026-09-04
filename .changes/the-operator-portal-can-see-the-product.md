# added

The operator portal can see the product: twins, runs, branches and safe state.

Five sections of the platform portal are now built against real tables rather
than reserved. Production Twins lists every environment on the installation,
with the one filter that is a finding: past the lifetime it was created with,
not torn down, and costing somebody money for a branch nobody is looking at.
Branches groups those environments by the branch that made them and surfaces the
same cost from the other side, a live twin on a branch whose pull request closed
a fortnight ago. Safe State reads the golden data versions and the masking rules
a scan proposed, and leads with the unconfirmed ones, which are the columns a
scanner believes hold personal data on a copy somebody is running tests against.

Runs and Jobs is the page an operator opens during an incident, so it is built
for one sequence: find the run, see why it failed, see what it touched. The
three run families are a filter rather than one merged table, because an agent
run has verdicts and artifacts, a load run has percentiles and a lease, and a
pull request check has a head commit; a table true of all three would have to
drop the columns somebody came for. Every list is keyset paged and says how many
rows it is showing, and the failures filter is applied by the query rather than
after the page, because filtering a cut page eventually returns an empty page
with a cursor behind it, which reads as the end of a list that has more in it.

A run that finished is not a run that passed, and the pages say so. Every run
carries a standing beside its own state: a load run whose state is `succeeded`
can carry a failing verdict, and an agent run that reached `complete` having
reported no verdict at all did nothing and found nothing. The second is shown as
its own answer rather than as a pass, which is the exit-code-zero-over-nothing
defect this repository has already shipped once.

Experiments and Feature Flags is a flags page, and it says so at the top. Flags
are complete: state, rollout, targets, the internal-only bit, and a kill with a
reason attached, plus the column that matters most during an incident, whether
anything in the build reads the flag at all. Experiments are not built. There is
no experiment table, no variant, no assignment, no exposure log and no results,
and a rollout percent is a share of traffic rather than an experiment because
nothing records which subject fell on which side. The page names that absence
and the four things that would end it, rather than drawing a dashboard whose
every number would be invented.

Safe State names its absences the same way. There is no record of a customer's
live database, no snapshot ledger and no restore history anywhere in the schema,
so the page cannot say which database was cloned, when a twin was restored, or
how old the copy is. It says which table each answer would need instead of
leaving somebody to search for a panel that was never built.
