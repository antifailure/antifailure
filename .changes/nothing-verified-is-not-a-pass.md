# fixed

A run in which no workflow reached a verdict about the application exited zero,
which told a pipeline the application had been checked and found fine when it
had never been driven. Both of this repository's own answers were that shape:
the control plane's check reported "6 workflows could not be carried through"
and went green on every run, and the whole example corpus reported "Nothing ran"
and went green too.

`af test` and `af ci` now exit `9` when no workflow reached a verdict, which is
a different code from the `8` a real failure exits with, so a pipeline reading
the number can tell "your change broke something" from "nothing was tested".
Individual verdicts are unchanged: one blocked workflow beside one that passed
is still a passing run, because a gap in the tooling must not read as a broken
application. A project with no workflows yet can set the new
`policy.workflows_unverified` to `warn` and have that choice recorded in its
manifest rather than assumed from silence.

A persona whose `login` is `none` no longer requires a users table. It never
signs in, so there is no account to create, and the engine refused it anyway for
having nowhere to create a user it would never use. `examples/go-api` sets
`login: none` because the service serves JSON and has no sign in page, and it
could not be run at all until now.

One outcome no longer carries two names. The planner's own explanation for a
page it cannot act on said the workflow was "reported as blocked" while the
verdict it produced was decided later and printed as `unverified`, so a single
report disagreed with itself about the same workflow.
