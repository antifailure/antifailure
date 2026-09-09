# fixed

The fidelity report said a wildcard capture was a faithful substitution, and
for half the hosts such a rule matches the sidecar refuses it.

`af fidelity` classified every capture rule the same way: `substituted,
recorded into the inbox and answered with the provider's documented success
shape`. That sentence is true for a host somebody named that this build has a
handler for. It was also printed for a rule covering a domain rather than
naming a host, which the sidecar answers with a 403 having recorded nothing
whenever it has no handler for whichever host arrives. A delivery path written
as `*.zapier.com` read in the report as captured into the inbox and posted
nowhere at run time, and the one document that exists to say what a copy does
not reproduce was the thing hiding it.

Calling such a rule refused would have been the same mistake pointing the other
way. The guard is `!known && !d.NamesHost()`, so a request under
`*.resend.com` IS captured and one under `*.zapier.com` is not, and neither
answer can be read off the rule. It is reported unmeasured with the reason,
which leaves the score and is named in the exclusions rather than counted as an
answer nobody checked. The mock branch of the same function already treats the
same rule shape that way.

Half of the same overstatement is left standing and named rather than papered
over. Whether a host somebody DID name is answered with the provider's own body
or with the generic empty object depends on the sidecar's handler list, which
lives in `package main` and cannot be imported by the report. This build has no
Zapier handler, so a named Zapier host is answered `{}` and a client checking
`status` reads that as a failure. Four new tests in the sidecar pin its actual
behaviour for a Zapier hook and a HubSpot contact write, including that one as
a stated limit, so a registry both sides can read has something to be checked
against when somebody builds it.

`af net explain` has the same blind spot from the other side: it gives the
identical CAPTURE verdict for both rules. Also not fixed here, for the same
reason, because a fifth copy of a host matching list is how the four this
repository already has drifted apart.
