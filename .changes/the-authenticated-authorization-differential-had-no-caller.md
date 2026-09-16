# security

The authorization family could catch a cross tenant leak and never ran the code
that does.

The authz family shipped the whole authenticated differential: AssessDetailed
compares a candidate reach against a base twin, fires idor, cross_tenant and
privilege_escalation when a persona reached content it should not have, and
suppresses a pre existing exposure so a pull request that merely touched an open
endpoint is not false red. The engine even carried the contract the readings
cross on, security.RawObservation, and the accessor a family reads them through.
But nothing called it. Probe collected only the anonymous reach and assessed
that alone, so in.Observations() had no reader: the horizontal, vertical and
escalation classes were a fully built feature that decided nothing, the exact
shape of dead code that passes a compiler and a glance and fails the user.

Probe now builds a candidate snapshot from the runner's observations and a base
snapshot from the base twin when one was built, and runs the differential over
them, keeping the anonymous reach it always had. BuildSnapshot is the adapter:
it maps each bounded observation into one authorization probe, decides the class
from who reached whose object, reads the outcome by whether the victim's planted
canary came back rather than by the status code, and arms the liveness from
whether the object was seeded so a refusal proves a boundary held rather than
that an id was invented. Absent observations are not measured, which fails
closed as inconclusive rather than reading as a clean pass, and a golden with no
canary cannot prove the content detector, so a leak it cannot trust goes
inconclusive rather than silently absent. The collector folds the per persona
readings every exploration recorded into the candidate the family reads, and the
runner to engine boundary carries the reading field for field.

The runner's population of those readings, the browser response capture and the
per object ownership and canary seeding, is the remaining producer; until it
lands the path is live and correct and reports the authenticated classes absent
rather than falsely clean.
