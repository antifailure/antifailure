# fixed

A run holding a workflow verdict the engine could not read reported the whole
run as passed and exited zero, while the table beside that line printed the same
workflow as unverified. An unreadable verdict is now blocked, which is what an
outcome that says something about us rather than about the change has always
meant here. The runner, the engine and the control plane declared those five
words in five places and nothing compared them; a test now reads the runner's
union and the control plane's enum and fails when either drifts.
