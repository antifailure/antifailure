# added

Nothing read the responses the run rendered. A change could start returning a
column it used to withhold, compile a secret into a client bundle, or render a
social security number into a page, and the rehearsal drove the twin, captured
the pages, and looked at none of it. The secret shipped, green.

There is a sensitive-data leak family now. It reads the DOM and the response
bodies the run already captured against the sanitized twin and looks for two
things: a value planted in the twin that must never appear in output, which is
an exact match with no false positive, and a secret or a piece of personal data
by shape, which is what catches the leak nobody could have planted a value for.
A Stripe secret key or a private key reaching a response fails the merge; a
national id or a bank account number reaching one is reported. It reuses the
same detector lexicon that reads a masked database back, so a secret means the
same thing in both places, and it inherits that package's iron rule: a finding
names the stream and the kind and never the value, which stays inside the engine
against a copy of production.

A project gates it through `security.canary_leak.secret_in_response` and
`security.canary_leak.pii_in_response`, and a secret in a response carries a
security exit code from the release gate. The planted-value path is wired for
the day a seeder plants canaries in the twin; the shape path works today,
against the evidence the run already has.
