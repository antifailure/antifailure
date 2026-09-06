# added

A dogfood workflow that signs in as two people in one browser, so the launch
night failure could have been caught before it shipped.

On launch night the founder was signed in to the console and to the operator
portal in the same browser, pressed Subscribe to team, and got a 403 naming a
header the Plan page has never heard of: the operator transport check was
running on a customer mutation because the operator cookie was present. The fix
is in the control plane. The question this answers is the one asked next:
would rehearsing the change against a twin have found it. It would not have,
because this repository's own manifest signed every persona in as one identity,
and a bug that needs two live sessions in one browser cannot appear to a
workflow that can only hold one.

The manifest gains an `operator-owner` persona, an operator of the platform who
is also a customer of it, which is what everybody on this team is. It is a
separate account in `admin_users`, signed in with a password at the portal, and
it is created by `deploy/docker/personas.mjs`, now run as the seed adapter's
command so the password the runner types and the hash the row stores are handed
out by one process and agree. The workflow
`an-operator-who-is-also-a-customer-can-start-checkout` signs in to the portal,
then to the console, then presses Subscribe to team, and expects the sentence
the Plan page shows once the mutation is past the transport gate, which the 403
can never produce. Reverting the fix makes the workflow fail on that 403, which
is the proof it can say no.

Two manifest features made it expressible, and both are documented and
validated. A workflow may name a list of `personas` rather than one, and the
runner signs in as each in turn in one browser so the sessions accumulate; the
last is the identity it acts as. A persona may name a `sign_in_path`, so the
runner finds the operator portal's form at `/admin` rather than typing an
operator's address into the console's own sign-in screen at the workflow's
start path.
