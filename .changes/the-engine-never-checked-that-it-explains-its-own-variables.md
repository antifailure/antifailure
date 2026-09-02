# added

Every environment variable the engine names at a user is now checked against
the documentation.

`af license install` tells a paying customer to "Set AF_LICENSE_KEY and AF_ORG
where the engine runs", then points them at
antifailure.dev/docs/enterprise/licensing, and that page named neither. The
product asked for two things and sent the reader to the one page that should
have said what they are. `af doctor` had the same shape: it recommends
`AF_PORT_RANGE_START` to somebody whose ports are busy, and nothing documented
it. Both are fixed; this is what stops the next one.

The control plane has had this check since `config-docs.test.ts` was written,
and its page has never drifted. The engine, which is the half a customer runs on
their own machine, had nothing.

It parses rather than greps, and that is not fastidiousness. The first version
was line oriented and returned a clean zero over `AF_PORT_RANGE_START` while
looking straight at it, because `r.Remediation = fmt.Sprintf(` and the string
that names the variable sit on different lines. A pattern that cannot match
looks exactly like a pattern that found nothing. Reading an abstract syntax tree
removes the question: a string literal is an argument to a call or it is not,
however the source is wrapped. Six shapes are covered and each has a test that
watches it fail, including that wrapped one, plus one test in the other
direction so a scanner that matched everything would not pass.

Named at a user means printed, or in a `Short`, `Long`, `Remediation`,
`Example` or `Next` field, or in a flag's usage string, or anywhere in the error
catalogue. A variable that is only read is not named at anybody: seven of the
engine's variables are spoken and all seven are documented.

`tools/docs/variable-exemptions.tsv` follows the pattern
`figure-exemptions.tsv` established. A variable may be exempted by a row that
states a reason, because an exemption with no argument behind it cannot be told
apart from somebody silencing a finding they did not understand, and a row that
stops being needed is reported so the file cannot rot. The file is empty today,
which is a result rather than an oversight: it exists for the next variable, not
for a backlog.
