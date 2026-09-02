# fixed

Two entries in the v1.0.0 release notes described a repository that does not
exist. `tools/relnotes` publishes that section verbatim as the GitHub release
body, so both would have shipped as the product's own account of itself.

The first told an operator that the documentation runs the control plane image
as `latest`. No page does, and no page can: `tools/claimcheck` refuses any pin
that is not a `main-<sha>` or `cd-<sha>` tag, because a tag that names no commit
tells the gate nothing it can check without a network. Both self hosting
procedures pin `main-b53906a`. An operator following the sentence would have
pulled an image nothing had proved could complete the procedure beside it.

The second said the dispatch workflow template calls `af workload run` rather
than assembling flags in a shell case statement, and that a knob with no flag
behind it is now refused by name. True of the workload kinds and not of
`scenario` or `explore`, which are the two verbs the same entry singles out as
needing the new file. `examples/github-workflow.yml` still answers both with
steps of their own, so a duration or a scale sent to either is still dropped in
silence. The entry now says which half holds.

Reading the template for the second one turned up a defect of its own, fixed
here. `examples/github-workflow.yml` declared `down)` twice in one `case`, and
bash takes the first match, so the second arm was unreachable. It carried the
comment explaining that teardown exits 10 when something could not be removed
and that this has to fail the job, which is a justification attached to an arm
that never runs: it tells the next reader a behaviour is handled when nothing
there handles it. The reasoning is true of the arm that does run, because
`af workload teardown` returns `AF-RUN-030` when resources are still pending and
that code carries exit 10, so the comment moves onto the live arm and the dead
one is gone.

Neither release note was caught by anything, and the reason is one sentence:
`CHANGELOG.md` is in no gate's document list. `tools/claimcheck` reads
`README.md`, `CONTRIBUTING.md` and `SECURITY.md` for path claims and a fixed set
of site trees for sentence claims, and the file the release body is cut from is
in neither. Its sentence rules are a hand curated list of claims that had
already shipped false, so a novel one has no rule to fire.
