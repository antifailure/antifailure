# security

`SECURITY.md` stops claiming three things the evidence does not support. The
credential scan runs on every pull request and every push to `main`, not on
every push. The SPDX bill of materials and the cosign signing are written and
have never run, because both existing tags predate the steps that do them. And
the reproducibility gap it named is now half closed: `just reproducible` builds
twice and compares, though it is not a CI job and it compares a local build.

The disclosure section says what happens when a target is missed, where to
escalate after a week of silence, what a reporter can expect for a finding below
high, our 90 day default for publication, and that there is no bug bounty.

`docs/plan/STATUS.md` loses two claims that were not true. G1 listed Biome as an
enforced linter; it is not installed and not run, and no TypeScript here is
linted or formatted by anything. The row-level security row said that disabling
one policy makes the suite name the table and the row count, which describes an
experiment nothing in the repository repeats.
