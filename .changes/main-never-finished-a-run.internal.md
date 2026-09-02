# fixed

A run on main is never cancelled by the next merge, so every commit on main
reaches a verdict.

`ci.yml`, `security.yml`, `crosslint.yml` and `keyring.yml` all carried
`cancel-in-progress: true` keyed on the ref, which is right for a branch and
wrong for main. On 2026-09-02 six pull requests landed inside the length of one
run and each cancelled the one before it. Main went from 09:07 to past 12:30
without a single COMPLETED run.

Cancelled is not red. It is also not a verdict, and the difference matters
because `release.yml` has no CI gate of its own: a tag on an unverified commit
publishes binaries. The only control was somebody holding merges by hand long
enough for one run to finish, which is what actually happened, and a control
that depends on a person noticing is not a control.

Branches keep cancelling, because there the saving is real and nothing reads
the superseded answer. Tags are excluded for the same reason main is.
