# added

`just gate` now refuses a `node_modules` that is not what its lockfile says,
through a new `tools/installcheck`, and every recipe that uses an installed tree
repairs a stale one instead of only creating an absent one.

The failure it exists for cost most of an evening. A week of `www` work was
verified with `www/node_modules` holding Next 15.5.23 against a lockfile pinning
16.3.3. Every build, every SEO assertion and a whole prose sweep ran against a
different Next major from the one CI uses, and every one of them reported
success in good faith. It also explains an inconsistency several people chased
separately: `next build` rewriting `www/tsconfig.json` is Next 16 behaviour and
a stale 15 install does not do it, so the same command dirtied one worktree and
not another. It was never flakiness. It was who had last run `npm ci`. The same
trap produced a bogus "Invalid config passed to starlight integration" in `docs`
and an `ERR_MODULE_NOT_FOUND` that read like somebody else's branch being broken.

It compares rather than installs. `npm ci` in every recipe that builds would be
correct and would make every local run pay for a full reinstall of a tree that
is almost always already right. This reads `package-lock.json` against
`node_modules/.package-lock.json`, which is npm's own record of what it
materialised, so it answers in milliseconds with no network and can run at the
front of `just gate` and inside recipes that install nothing.

Four situations, four answers. A drifted tree fails, because everything checked
against it answered about the wrong versions. A half installed tree fails for
the same reason, and so does one installed before the workspace it links into.
A workspace with no `node_modules` is reported and does not fail the gate, because it cannot have answered anything and every
recipe now installs what it uses; the recipes themselves do treat it as a
reason to install.

Workspaces are found rather than listed. There are eight lockfiles here and the
two places that named them by hand each named a different subset: `just deps`
installed two of the eight, so a fresh clone left six uninstalled and said
nothing. It now installs all of them, and derives the ORDER from the lockfiles: a
workspace that resolves dependencies out of another with `file:` links goes
last. `ee/web` is the only one today. `npm ci` there before `web` exists
succeeds and leaves a tree that does not work, and `npm run typecheck` then
reports five implicit-any errors inside `web/packages/db/src/schema.ts`, in a
file nobody touched, on a branch that is fine. That is now a fourth thing the
guard reports.
