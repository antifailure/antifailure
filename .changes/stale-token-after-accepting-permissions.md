# fixed

Accepting new permissions on the GitHub App installation left the control plane
using the token it had already minted, which still carried the OLD scopes. An
installation token is cached for its full hour and GitHub invalidates the
outstanding ones the moment a grant changes, so the App went on refusing writes
it had just been granted. On 2026-08-31 an Actions write grant accepted at
00:38:54Z was answered with 403 until roughly 01:36Z, complaining about a
permission that was already in place.

`InstallationTokens.forget` had existed since the App client was written and had
no callers anywhere in the tree. An `installation` delivery now drops the cached
token before it handles anything, for every action rather than for
`new_permissions_accepted` alone: suspend, unsuspend and deleted each change
what a token is worth, and dropping one that did not need dropping costs a
single mint.
