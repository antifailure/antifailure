# fixed

The deploy's traffic shift is tried again when Azure refuses it for a reason
that will pass, and refused immediately when it will not.

On 2026-09-06 a staging deploy printed `The containerapp 'afcp-app' does not
exist in group 'af-cp-centralus'` while shifting traffic. The app existed. The
same script had already run three commands against the same name and the same
group seconds earlier, all of them successfully, and the app answered a hand
query moments later. That is a transient ARM lookup failure reported in the
words of a permanent one, and it was the one Azure call in the deploy with no
way to try again: the bootstrap job is polled sixty times, the revision
readiness is polled sixty times, every deactivate tolerates its own failure, and
the traffic shift was a single call under `set -euo pipefail`.

Staging recovers from that on its own, because the next merge deploys and takes
traffic. A production tag has no next merge. The healthy revision sits at zero
percent, production keeps serving the previous release, and the only signal is a
red check nobody is watching at that hour.

The shift now retries with backoff. It does not decide transient from permanent
by reading the message, because the message is identical when the app name is
genuinely wrong: it asks the app for its own name between attempts, waits only
while the answer is yes, and stops on the first attempt when the answer is no,
so a mistyped app name fails in seconds rather than after two and a half
minutes of waiting. Throttling and gateway errors name themselves and are
retried without the extra read. Anything the classifier does not recognise is
not retried, because a loop that treats every unfamiliar failure as weather is a
hang with extra steps.

When the retries are exhausted the deploy says which revision is still serving
and that the commit is not live, rather than exiting on the raw Azure message.
It does not roll anything back, because traffic never moved and a rollback that
puts traffic where it already is reports something that did not happen. What is
serving is read back rather than assumed, so a call that errors after the
weights are already written is still put through the public health gate instead
of being reported as a deploy that never happened.

The rollback's own traffic shift is retried the same way. It was a single call
too, at the worst possible moment, and a transient failure there ended the run
on a raw error with the unhealthy revision still serving.

`docs/plan/notes/cd.md` said "any failure after step 2 puts traffic back on the
revision that was serving". That was never true in either direction. A failure
at the shift leaves traffic where it already was, and a failure in the
maintenance job update or the connection budget check deliberately leaves the
healthy release serving. Both end states are now written down, next to the
other three.

Reading what is currently serving no longer kills the deploy when Azure refuses
the read, for the same reason: a blip on a read should not end a release before
it starts. The cost of that tolerance lands on the reap, which compares each
active revision against the one it must keep, and an empty name matches nothing.
It now refuses to reap at all when it does not know which revision the deploy
superseded, rather than deactivating the one thing a rollback would need.
