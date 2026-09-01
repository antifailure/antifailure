# fixed

Refreshing a screen in the console blanked it to a skeleton, and a refresh that
failed replaced correct data with a full page error.

`useApi`'s `reload()` reset the hook to `{status: "loading", data: null}`, which
is right when the dependencies change, because another environment's rows have
nothing to do with this one's, and wrong when the same question is being asked
again. The reader lost what they were reading for the length of the round trip,
and if the second answer failed they lost it permanently: `Loaded` renders the
error branch over the whole screen, so a correct table became "That did not
load" because a retry went unanswered.

Where it lands is the Plan page's plan change, which reloads immediately after
the mutation and is pressed by somebody who has just been told their quota
changed. It does not reproduce on a fast local control plane, which is why it
survived. `usePages` had the same reset, and all three of its callers reload
after a mutation.

A reload now keeps what is on screen, reports `refreshing` while it is in
flight, and puts a failure in `refreshError` instead of `error`. `Loaded`
renders that as a strip above the content saying the rows are the last answer,
with the same Try again, so every screen in the console gets the behaviour
without a per page change. A dependency change still resets, because then the
held data really does belong to a different question, and the two are told
apart by comparing the dependencies themselves rather than by trusting the
reload counter. Both hooks also carry a request sequence now, so of two
reloads in flight the older answer cannot overwrite the newer.

Measured against the built console with the control plane stubbed at 400ms,
twenty samples of a plan change on each side. Before: the table blanked during
the reload in 20 of 20, and with a failing reload the full page error appeared
in 20 of 20 and nothing was left on screen. After: no blank in 20 of 20, and
with a failing reload the table survived in 20 of 20 with the strip above it.
