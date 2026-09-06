# fixed

An operator who was also a customer could not run any customer mutation from
the console, and the first one it refused was Subscribe to team.

One browser, two cookies: the operator session from the portal and the
product session from the console. The console sends the product token with a
product mutation, which is all it can know about. The operator transport
check keyed on the operator cookie being PRESENT rather than on the request
being an operator request, so it ran on every mutation that browser made and
refused each one with 403 "needs the x-antifailure-admin-csrf header", naming
a header the Plan page has never heard of. The product session had already
been checked against the product token a few lines above.

The operator cookie is the credential for exactly one namespace, `admin.*`,
and the check now applies to a request that names a procedure in it and to
nothing else. A batch with one operator procedure in it is an operator
request as a whole. The suite that guards this gate gained the two orderings
it was missing: both cookies on a customer mutation pass on the customer
token alone, and both cookies on an operator mutation are still refused
without the operator token.
