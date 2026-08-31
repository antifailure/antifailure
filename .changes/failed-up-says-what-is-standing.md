# fixed

`af up` no longer panics when it fails before it has anything to report. Every
failure inside the session open, which is the state directory, the branch lock
and the journal, returned a nil result that the failure path then read a field
off, so a Go stack trace replaced the error that had just been diagnosed and
the exit code a script reads.

A failed `af up` also now says what it left standing and how to remove it. The
environment is deliberately not torn down on failure, so that there is something
to look at rather than destroyed evidence, but nothing said so: the next step on
each of the forty eight codes `af up` can exit with points at the failure, so
somebody was told to read a log while containers and a database branch sat
there, without yet knowing `af down` exists. The count is read from the
inventory, so a run that created nothing stays silent.
