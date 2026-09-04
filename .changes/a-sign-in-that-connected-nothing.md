# fixed

A person who signed up, signed a terminal in with `af login`, and ran `af up`
watched an empty environments list forever, and every part of it looked
configured. `attachControlPlane` took a token from `AF_CONTROL_PLANE_TOKEN` or
from a GitHub Actions identity and from nowhere else, so the credential the
device grant so carefully protects was read by `af env pull` and by nothing
that reports a run. The CLI now hands the stored credential and the origin it
was issued by to the orchestrator, which passes both to telemetry, and `af up`,
`af test`, `af ci` and `af workload` report with it. The environment token still
wins, because exporting one is a decision and a credential on the machine is a
default. A machine nobody has signed in behaves exactly as it did before.

Nothing in the console had ever mentioned the command line. A new organization
landed on an environments list whose three empty cards each explained that
something appears when the engine reports one, and no screen anywhere said how
to get an engine, sign it in, or run it: the install command was on the
marketing site and in the documentation, which is where somebody who has not
signed up yet is. There is now a **Command line** screen carrying the install
command, the sign-in command, and the first two commands worth running, and both
empty states on the environments page lead to it.

The sign-in command on that screen names the control plane the console is being
served from, and says nothing when that is already the hosted instance. A plain
`af login` on a self hosted plane signs a terminal in to somewhere else and
stores a credential for an origin nothing that person runs will ever talk to.

`tokens.list` and `tokens.revoke` were written, permissioned and audited, and no
console called either of them, so a ninety day credential granted by `af login`
could be neither seen nor taken away from any screen in the product. Both are on
the new page, telling a terminal apart from an engine token because revoking
your own laptop and revoking a build machine are not the same act.

A login whose token could not be stored used to leave that token live. It is
minted the moment somebody approves, and on macOS the write that follows can be
refused: over ssh or under a launchd agent there is nobody to authorise the
keychain prompt. The command returned an error and left a ninety day credential
nobody held, nobody could see and nobody could revoke, with another one beside
it on every retry. It now revokes what it cannot keep, and says the token is
still live, and where to revoke it, when the revocation fails too.

The credential CI needs was the other half of the same silence. `POST /v1/tokens`
mints an engine token, it answers a bearer credential and has no cookie path at
all, and that is deliberate: a browser session that could mint would be a
credential factory behind a cookie. So no screen can reach it and none should,
which leaves telling somebody as the only thing a console can do, and nothing
did. The page now carries the two commands, says the scope has to be asked for
by name, and says first that a GitHub Actions job needs no token at all, so
nobody pastes a permanent secret into a repository to solve a problem the
identity exchange already solves.
