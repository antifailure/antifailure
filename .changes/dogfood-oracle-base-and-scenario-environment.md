# fixed

A pull request whose recorded base had fallen behind main failed the dogfood
check for reasons that had nothing to do with the change. The job compared
against `github.event.pull_request.base.sha`, which GitHub leaves where it was
when main moves, while the commit the job checks out is rebuilt on the new tip.
With two commits in the clone the recorded base was not one of them, so
`af oracle` refused with AF-ORC-003. The load scenarios then failed with
AF-LOD-010, because they had been sent at a candidate the refused oracle never
brought up. The job now takes the base from the checked out merge commit's
first parent, for both the oracle and the staging database it shapes, and the
scenarios bring their own environment up, saying so when they cannot rather
than failing a second time.
