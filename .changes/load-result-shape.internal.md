# changed

The result tables now hold what the engine actually measures, corrected against
real runs rather than against reasoning about them.

A route measurement and a threshold verdict each carry the scenario that
produced them. Two scenarios in one run routinely send the same route and can
each declare an assertion of the same name, and two p95 values do not average
into a p95, so a key without the scenario refused the second row.

A browser result carries all five workflow counts rather than passed and failed.
A real run returned one unverified workflow and nothing else, because a persona
could not be created; with two counts a console renders that as a run with no
failures, which is the exit code zero over work that never happened, moved into
a table.

An exploration carries how many goals it had and how many it reached, rather
than one boolean for up to fifty goals.

A run stores the `af` command the engine reported and the digest of the manifest
it read. Stored rather than rebuilt: a command a console assembles from a form
drifts from the one that ran, and being the same one is the only reason to print
it. A result also keeps failures by reason rather than a total.
