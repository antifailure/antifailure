# added

A long run now shows how long it has been running and how to stop it. `af up`,
`af ci`, `af test` and everything else that builds an environment draw a status
line under the step in progress carrying the elapsed time for the run and for
the current step, and a reminder that the first Ctrl-C rolls back rather than
abandons.

The line is drawn only where there is a terminal to erase it on. Piped output,
a file, and a CI log receive exactly the bytes they received before, because
text output stays byte stable for the same input. Nothing spins and nothing
loops: the line is rewritten once a second because the number on it changed.
