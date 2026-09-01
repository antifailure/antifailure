# added

A hosted control plane can require a plan before it does any work.
`AF_HOSTED_REQUIRED_PLAN=enterprise` refuses every operational procedure until
Stripe grants that plan, while leaving authentication, sign-out and billing
reachable, because billing is the path that resolves the refusal. It is unset
everywhere except Antifailure's own hosted service, so self-hosting is
unchanged. Setting it while billing is off stops the process at startup, since
that combination refuses every request and offers no way to pay.

The gate is enforced in shared tRPC middleware rather than per page, and on the
three entrances that do not pass through it: engine ingestion, engine
environment reads, and the provider and model proxy calls a terminal makes.
`billing.set`, which exists so a self-hoster can change their own quota, is
refused wherever Stripe or the gate is configured, because on a paying
deployment it is a caller writing their own entitlement.

The Plan page now calls the subscription routes that already existed, and draws
the states nobody builds: never subscribed, subscribed, no invoices yet, a
failed call, a cancellation already scheduled, a refresh in flight, and a member
who may look but not buy. `AF_GITHUB_APP_INSTALL_URL` gives a signed-in person
with no organization the two actions that resolve it themselves rather than a
sentence telling them to wait for somebody else.

# fixed

Three ways an organization could exist that nobody could ever enter, each of
them rendering the empty state that means "nobody has installed the App" to
somebody whose App is installed.

Signing in and installing the App are two events with no guaranteed order, and
only one order worked. Sign-in reads the installation table on its way through,
so installing first was fine; signing in first arrived after the only writer of
membership had already run, and nothing reconsidered it. The flow the product
recommends produces exactly that order. The installation delivery now adopts its
own sender when a user row for that GitHub id already exists, through the same
membership writer sign-in uses, and rotates the session they are holding so the
tab left open on the empty state resolves itself. A session already inside an
organization is never moved.

An App installed on a personal account created an organization keyed on the
holder's login, and `/user/orgs` never returns your own account, so sign-in
asked about every organization except that one. GitHub also has no membership
record to consult for a personal account, so the holder would have arrived as a
plain member of their own tenant with nobody holding `billing.manage`.

`/user/orgs` was read one page deep, and it defaults to thirty. That list
decides which organizations somebody may enter, so truncating it withheld the
tenant they came for rather than shortening something they read.
