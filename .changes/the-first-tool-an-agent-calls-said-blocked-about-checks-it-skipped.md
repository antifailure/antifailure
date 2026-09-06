# fixed

Six things the product said, or failed to say, about itself, found by
evaluators using only the product.

`check_prerequisites` over MCP returned `BLOCKED` with "Packet filtering" and
"Corporate proxy" in its `blocking` list while both had result `skip` and a
remediation of "No action needed". Only a failed check is blocking now; a skip
is listed among the checks with its reason and decides nothing, so a Mac,
where packet filtering is always skipped, can be `ready`.

An MCP tool failing under a held branch lock returned `SAFETY_UNAVAILABLE` and
"the server log says why", a log no tool on the server can read, while
`af golden list` printed `AF-RUN-003` with the holder's process id and what to
do. The error now carries `cause`: the code, the message, the next step and
the link, the four lines the CLI prints, and the sentence pointing at the
server log is gone from every tool.

`af whoami`, `af provider list` and `af token list` printed a raw
`invalid character 'K'` decode error for a stored credential that did not
parse, with no code, no next step and no link. The credential store now
returns `AF-SEC-006`, which names the file or the keyring, keeps the decoder's
words as the detail, and says to sign in again with `af login`.

`af doctor` printed nothing for over two minutes and then all fifteen checks
at once, because a slow Docker daemon held back the checks that had already
answered. The checks now run at once and each line prints the moment its
check answers. The summary and the JSON form are unchanged.

`check_prerequisites` and `af doctor` said nothing about which webhook
providers could deliver into the environment, so a reviewer learned that
billing and the GitHub App were off only by sending an event, being refused
with `AF-NET-012`, and reading the application's startup log. A new check,
"Webhook delivery", reads the manifest and states up front which providers
have a `webhook_path`, which are off, and which have a signing secret in a
service but nowhere to deliver to.

`run_browser_workflows` over MCP omitted the "N requests the page could not
make, the first was ..." line `af test` prints under every workflow, so an
agent saw less than a person for the same run. Each workflow in the JSON
result now carries the count and the first such request.
