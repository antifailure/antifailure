# added

The side_effect security family counts the dangerous external effects a change
makes, a PaymentIntent, an email, an outbound webhook, and reports the ones this
change added over the base branch: "the base branch made 1 and this change made
3". It read a base twin's effects to do that, and it handled a missing base twin
correctly, leaving the comparison unmade rather than reading an absent base as a
base of zero. But `af ci` never built the base twin, so the missing-base path was
the only path there ever was. The increase rule was fully built, correctly
consuming a baseline it was never handed, and it sat dormant on every run.

`af ci` now brings a second environment up from the base revision when the
change routes a family that reads a baseline and there is a change to compare. It
is the oracle's baseline mechanism reused: the base is resolved as the merge base
with the base branch, its images are built from a git archive of that revision,
and it is pinned to the candidate's own golden so both sides branch one database
and a difference is the code's rather than two databases'. It runs the same
exploration and the same workflows the candidate ran, so the two effect counts
are of the same drive.

The base twin is stamped ephemeral before it comes up and torn down by a
deferred teardown after, so a crashed run's base environment is collected by the
reaper within the run's budget rather than living the manifest's day, and a base
env left standing is named with the exact `af down --branch` command to remove
it. It fails closed: a base that is the same commit, that will not come up, whose
workflows did not run, or whose effect logs could not be read is a note and no
comparison, never a base of zero that would read as effects the change did not
make. A change that routes no baseline reader builds no second environment.
