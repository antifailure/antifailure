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

**If you are following the release note that asks you to accept new App
permissions and a check still fails immediately afterwards, and you are running
a control plane older than this fix, the cause is the cached token rather than
the grant. Wait for the hour to lapse or restart the control plane, and retry.**
That sentence is here because the instruction to accept permissions and the fix
for what happens next do not have to ship together, and a note that is only
correct under a sequencing assumption goes wrong quietly. Delete it at tag time
if both landed in the same release.
