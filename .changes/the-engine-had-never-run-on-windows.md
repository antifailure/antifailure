# fixed

The engine compiled for Windows and had never been run there, and running it
found a product that broke in ordinary use.

Every replay and incident write failed with "Access is denied", because the
store synced its directory after each write and Windows refuses that. `af init`
named a service at the root of a Windows checkout after the whole path, so
`C:\Users\me\shop` became `cusersmeshop`, and asked every question about it
under that name. The fork gate read the base branch's policy through a path git
could not find, so on Windows, and for any manifest below the top of a
repository, the base branch's `fork_policy` went unread and `label` was used
instead, which meant a base branch that said `never` could still be overridden
by a maintainer adding the label. The doctor told every Windows user their
state directory was readable by others, because Windows reports every
directory as mode 0777, and told them to run `chmod`.

Files that promised to be readable only by their owner were exactly as readable
as their folder on Windows, where a 0600 mode means nothing and the folder's
access list decides. The credential `af login` writes when there is no
keyring, the encrypted secret store, the state database and the lock now carry
an access list naming only the current user, set as the file is created.

An archive unpacked for a baseline comparison now refuses the names that mean
something else on Windows than on Linux: a drive relative path such as `C:x`,
which names a place on drive C rather than in the checkout, a device name such
as `NUL` or `COM1`, which opens a device in every directory, and a path
starting with a slash, which Linux already refused. A repository with a
symbolic link no longer fails the comparison on a machine that cannot create
one; the link is written as a file holding its target, which is what Git for
Windows itself does.

`database.seed` and persona seed commands run in the `sh` that ships with Git
for Windows, found through git rather than through PATH, and `af doctor` says
where they will run or what to install. pg_dump installed by the PostgreSQL
installer is found under Program Files. The terminal image query is no longer
written on Windows, where its answer could not be read back and was left in the
keyboard buffer; set `AF_IMAGES` to choose instead.

A new required check runs the engine's tests on Windows on every pull request.
