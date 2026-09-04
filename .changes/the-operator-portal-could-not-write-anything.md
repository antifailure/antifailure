# fixed

Every write in the operator portal was refused, and had been since the portal
existed. Suspending an organization and resuming one were its only two
mutations; both were wired to buttons, both answered 403, and both looked
correct in review. They work now.

There were two independent causes, and the second is the one that would have
cost somebody a night.

The console sent no operator CSRF token. The comment above the code that sent
it argued at length that none was needed, because the operator cookie is
SameSite=Strict and a browser sends it on no cross-site request of any kind.
That reasoning is sound and it does not matter: the control plane refuses every
operator mutation that does not carry the header, and its test suite pins that
three ways. Two individually correct halves, and nobody ran the pair.

The origin check compared a browser's Origin header against AF_APP_BASE_URL as
text. Those are different things. An Origin is a scheme, a host and a port, with
no path and no trailing slash, ever; AF_APP_BASE_URL is a base URL. So the check
agreed only when the variable happened to be spelled exactly as an origin.
Setting it with a trailing slash was refused. Setting it to a URL with a path
was refused. Leaving it unset, which is what the shipped configuration does
deliberately because the address is allocated at run time, was refused. Three of
the four ordinary spellings turned every operator write into "This operator
request came from another site", which is a sentence that sends whoever reads it
looking for an attacker who was never there.

Nothing was loosened. A request from a genuinely different site is still refused
against every spelling, and an Origin header that is not a URL is now refused
rather than waved through. When no base URL is configured the check declines to
judge rather than refusing, because it has nothing to judge against, and the
token remains the thing that fails closed.
