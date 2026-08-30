# added

A run can now come back `warn`: a real finding about the change that does not
fail the check. The engine had five verdicts and none of them was the middle
one the product has always described, so anything the environment noticed
either failed the build or was printed and forgotten. A new `policy` block in
the manifest decides which class of finding warns and which fails, with
`ignore` for the ones a project does not want. `blocked` keeps its meaning
exactly: the runner could not evaluate this, it exits zero, and it never counts
against the change.
