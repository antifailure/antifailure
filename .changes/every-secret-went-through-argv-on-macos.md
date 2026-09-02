# security

On macOS every secret this product stores went through a child process's argv,
where any other user on the machine could read it in `ps`. A control plane
bearer token was read that way on this project's own machine, by accident, by
somebody looking at something else. `af login`, `af model set`, `af secret set`
and `af provider` all reach it.

The product had already decided this matters. `cli/model.go` says in capitals
that the key is never an argument, because a secret on a command line is in the
shell history, is visible in `ps`, and is in any recording of the terminal.
`af model set` refuses a `--key` flag and reads without echo for exactly those
reasons, and then handed the key to a function that put it on a command line one
process deeper.

Both halves of the comment that justified it were false, and each was settled by
running something rather than by reading. It said the value goes through a flag
"because security takes it that way": `security` reads the value from stdin when
`-w` is given no argument, prompting twice, and the value round-trips byte for
byte. It said the exposure was acceptable because "writing is only done by
'af secret set' on a workstation, never in CI": `af login` and `af model set` are
both first-run workstation commands and both reach that line.

The value now goes in on stdin. A value containing a line break is refused and
pointed at the encrypted local store, because the prompt protocol is line based,
which matches what the Windows implementation does with a PEM key too long for
its blob limit. Every call to `security` is now bounded by a timeout: without
one a keychain that blocks rather than failing has no upper bound, and the
fallback to a file cannot fire, because a hang is not an error.

Linux was already correct and Windows starts no child process at all, so macOS
was the only affected platform.
