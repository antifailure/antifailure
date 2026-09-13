# fixed

A draft pull request's dogfood check always failed, and marking the pull
request ready never replaced the failure. The workflow ran on GitHub's default
pull request events, which are opened, synchronize and reopened. The control
plane starts no check on a draft and opens one when the pull request is marked
ready, so the draft's run did twenty minutes of work, was refused with HTTP 409
because no check was waiting on its commit, and went red; and marking it ready
started the check and no run to answer it, so the red stayed until somebody
pushed again. The workflow now runs when a pull request is marked ready for
review, skips the check on a draft and says so in its comment instead of
reporting a failure, and cancels a run that is still going when a pull request
is sent back to draft.
