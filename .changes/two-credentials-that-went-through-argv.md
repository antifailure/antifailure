# security

The production runbook told an operator to pass two GitHub credentials to
`az keyvault secret set` as `--value`.

`rotating-secrets.md` forbids that by name for every other credential on this
plane, and the reason is not stylistic: a value passed as an argument is in the
shell's history file and in the argument list of a running process, where `ps`
shows it to anybody else on the machine. The Azure page had all three of its
uses replaced when billing was documented. These two were in a different file
and were missed, so the project had one page saying to do it safely and another
saying to do it the way the first page forbids.

Both now use the same `afsecret` helper, which takes the value at a prompt with
the input hidden and writes it with `printf '%s'` so no trailing newline is
added.

The client id in the same block is deliberately NOT changed. It is not a
credential, and `keyvault.tf` says so where it explains what tfsec reports over
these secrets: an OAuth client id is in the address bar of every person who
signs in. Hiding it would teach the next reader that it is the same kind of
thing as the two beside it, which is the sort of quiet miscalibration that makes
somebody careless about the ones that matter.
