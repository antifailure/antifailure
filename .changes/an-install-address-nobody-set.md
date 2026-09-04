# fixed

The "No organization yet" screen told people to install the GitHub App and then
recheck their membership, and on a control plane with no
`AF_GITHUB_APP_INSTALL_URL` set it offered neither action. Both buttons were
behind the same condition, so an unset address hid the membership recheck as
well, even though that action is an ordinary sign-in exchange that never needed
an installation address for anything. What was left was two paragraphs of
instructions and a Sign out button.

The recheck is now offered either way, and becomes the primary action when there
is nothing to install from. The copy changes with the address rather than
describing a button that is not there, because prose naming an action the page
cannot offer is the failure and not a symptom of one.

Leaving the address unset stays supported. A self-hosted control plane may grant
membership its own way and have no App of its own to point at, so refusing to
start over a screen it never shows would be wrong. What was missing is the
operator being told: the startup log now names the state either way, next to the
lines that already do this for the allowlist, the sealing key, model prices,
Stripe and the plan gate. The variable was unset on both control planes for
weeks precisely because nothing failed and nothing said so.

A value that is not a URL at all is still refused at startup and now says which
variable and which shape, instead of the bare `Invalid URL` that names neither.
