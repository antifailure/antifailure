# fixed

The `edition boundary` check reported a failure it had not tested. The step
deleted `ee`, ran the whole community suite, and called anything that came back
red an edition violation. On the night of 2026-09-08 that was 13 of the 25 open
pull requests, and not one of them was an edition violation: nine distinct
engine test failures accounted for all thirteen, every one of them reproducible
with `ee` present, and the `engine` job was red on all thirteen for the same
reason. Thirteen branches were sent to look for an enterprise import that was
never there.

The gate now runs the packages it saw fail a second time with `ee` back. One
that fails both ways is the engine job's finding and is reported as such; one
that passes with `ee` and fails without it is the violation, and is still
refused; one that cannot be told apart from a flake is reported as undecided
rather than as either.
