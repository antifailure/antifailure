# fixed

Creating a golden or an environment on Xata waits for the new branch to become
ready, and that wait can last five minutes. For all of it the engine printed
nothing, so a slow branch and a hung one looked the same, and the only way to
tell them apart was to wait and see which it was.

The wait now says what it is doing while it happens, published as
`engine.progress`: a line when a branch is first found not ready, naming the
branch and the status Xata reports for it, another every thirty seconds saying
how long it has been waiting and what the status is now, and a line when the
branch is ready saying how long that took. A branch that is ready on the first
check prints nothing, so the common case stays as quiet as it was.
