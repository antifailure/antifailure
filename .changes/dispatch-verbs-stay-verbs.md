# fixed

The dispatch workflow's `command` input keeps its verbs. An earlier change
renamed them to the control plane's kind names to remove a second vocabulary,
and that would have broken every copy of the file already in the wild: GitHub
reads the trigger definition from your default branch and answers a dispatch
carrying an undeclared value with a 422, which is indistinguishable from the
file being missing. So the two values that work today would have started
failing in order to make two new ones work.

The list grows instead. `up`, `down`, `agents` and `load` work on an older copy
of the file; `scenario` and `explore` need this one. `af workload run` accepts
both spellings and its result says which kind a verb resolved to.

`down` is new and runs the real teardown with an acknowledgement of what was
removed and what is still standing.
