# changed

`docs/plan/STATUS.md` quoted numbers that a tool computes and that nothing
checked, so they went stale silently and the file's own rows disagreed with
each other about the same fact. Sixteen were already wrong against the tree:
the gate count in two places, the analyzer count, the goleak package count,
the coverage denominator, the transform count, the runtime conformance count,
the CI job count, the documentation page count in two places, the spell and
vale file counts, the readability page count and mean, the HUD golden frame
count, the DDL lint rule count, and the api test count. Each is now the
property being claimed plus the command that prints the number. Measurements
of past runs are untouched, because a drill that took 13.9 seconds took 13.9
seconds forever.

`docs/plan/prod_guide.md` carried the same defect twice, and the second one
was not a stale number but a superseded plan. Its list of eleven lint rules
to build has been built in full, and its recommended fix for the unrouted
permissions exists and passes. Both sections are now marked as history with
the mapping a reader can check, rather than left reading as present tense.
