# fixed

An operator whose browser also held the customer session could not run any
operator mutation from the portal, and the first one it refused was Mark
reviewed on a job application.

The mirror of the night before. The customer transport check keyed on the
customer cookie being present rather than on the request naming a customer
procedure, so it ran on the portal's operator mutations, which carry the
operator token and nothing else, and refused each one with 403 "needs the
x-antifailure-csrf header from GET /auth/session". The operator check had
already been narrowed to `admin.*` requests; this narrows the customer check
to everything else. A batch that mixes both namespaces is checked both ways.

The suite gained the operator side of the both-cookies ordering and the
customer gate's own refusal and acceptance over HTTP, so switching either gate
off, or widening either back to cookie presence, turns it red.
