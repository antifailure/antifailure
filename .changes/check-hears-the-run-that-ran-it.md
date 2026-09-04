# fixed

The `Antifailure` check on this repository's own pull requests said "Nothing was
verified" on every one of them, and it was right. Two things were wrong and each
alone was enough for silence.

A GitHub App is delivered a `workflow_run` event for every workflow in a
repository. The control plane bound the first one it saw for a commit and let
that run's completion decide the check. One workflow per repository gets away
with that; this repository has seventeen. On commit `ada5644` the run that ended
the generation was `Security`, green fifty seconds in, while the job running
`af ci` was still building its database. The check was completed and amber
before the check had run. A workflow run now has no standing until it says which
run it is, by trading a workflow identity GitHub signed for a callback
credential, and that identity's `run_id` is GitHub's own claim rather than
anything a job asserts.

And the dogfood workflow never reported at all. It had no `id-token: write`, so
the runner set no identity variables and nothing could prove what the job was;
it named no control plane, so the engine's event sink did not try; it never
asked `af ci` for `--report-json`; and it posted to `/v1/pr/report` nowhere. It
wrote a comment and exited zero, which is the shape of failure this repository
names most often, with the product itself doing the naming on every pull request
and nobody reading it.

Both workflows now ask for the credential in their second step rather than
beside the report at the end, so the check reads as running while a runner is
working, a job that dies is a run the control plane can name and cancel, and a
dead run is reported in seconds rather than at the deadline.
