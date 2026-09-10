# fixed

A precondition that read "the daemon did not answer" as "the image is absent".

Two checks in the air gapped runtime tests asked whether the daemon holds an
image by inspecting it and treating `err == nil` as present. Every other
outcome took the same branch, so a genuine absence and a daemon too busy to
answer became one answer, and the answer was the reassuring one.

It went wrong exactly where it was load bearing. One of those checks exists to
refuse to run a test unless a sidecar image is ABSENT, because the code under
test returns early when the image is present and would never reach the refusal
being measured. On a daemon carrying 98 running containers the inspect came
back with something that was not a not found, the check read it as absence and
continued, and the assertion failed nine lines later saying an error was
expected. The precondition existed to stop precisely that run and waved it
through, because it could not tell the two errors apart.

Absence is now only ever a not found, through `dockerutil.ImagePresent`, and
anything else is returned to the caller. The first check fails the test saying
the daemon could not answer, so the run reports NOT RUN rather than passing.
The second guarded a skip, which is the quieter failure of the two, since a
daemon that could not answer would have reported the test as not applicable and
a skip reads like a pass in most summary views.

The same test also now REMOVES the image when it finds one, rather than only
refusing to run. Restoring a source file does not remove what the broken code
built, so a mutation run that deleted the refusal left the image behind and
every later run failed for a reason that had nothing to do with the code.

This is a defect in a check and not in the air gap. The production path makes
the same two valued read and fails SAFE: when its inspect errors it falls
through to the refusal and the build is refused.
