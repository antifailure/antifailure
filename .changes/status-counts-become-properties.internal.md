# changed

`docs/plan/STATUS.md` quoted numbers that a tool computes and that nothing
checked, so they went stale silently and the file's own rows disagreed with
each other about the same fact. Fourteen were already wrong against the tree:
the gate count in two places, the analyzer count, the goleak package count,
the coverage denominator, the transform count, the runtime conformance count,
the CI job count, the documentation page count in two places, the spell and
vale file counts, the readability page count and mean, and the HUD golden
frame count. Each is now the property being claimed plus the command that
prints the number. Measurements of past runs are untouched, because a drill
that took 13.9 seconds took 13.9 seconds forever.
