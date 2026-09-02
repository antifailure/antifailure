# security

The run report could not say whether a sandbox credential was actually
replaced.

A sandbox rule substitutes a sandbox credential on the way out, but only when a
value was configured for the name the rule refers to. When none was, the
sidecar forwarded whatever the application sent, and in every column the report
showed, that request was identical to a working sandbox call: allowed, mode
sandbox, the rule named, a normal status. So `af ci` could say "4 requests
allowed" and could not say whether those four carried a sandbox credential or a
live one.

The report now says. It states the substituted count either way, because a line
that only appears when something is wrong teaches a reader nothing by its
absence, and it names the hosts that a request reached carrying the
application's own credential.

The counts were already in the decision log and had been all along. Nothing
read them, which is the more general shape: a safety property whose only
evidence is a number nobody computes is a safety property nobody can check.
