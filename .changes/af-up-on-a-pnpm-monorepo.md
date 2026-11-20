# fixed

`af up` could not bring up a service in a pnpm workspace. A design partner ran
it twice on their monorepo and both runs stopped on the first service.

The generated Dockerfile copied the package's `package.json` and the root
lockfile into one directory, so pnpm saw a workspace with its members and its
patch files missing and stopped on `ERR_PNPM_PATCH_NOT_FOUND`. It also ran the
root's build script and started the process from the root. A workspace member
is now built as one: the whole repository goes in, the frozen install runs at
the root, the build runs through turbo or pnpm's dependency filter so the
packages it imports are built first, and the service starts from its own
directory.

A service that built would then have exited at startup. corepack cached pnpm
under root during the build, and the service started as `node`, found no copy,
and went to registry.npmjs.org for one through a sealed egress. pnpm and yarn
now live in a shared `COREPACK_HOME` that the install fills and the runtime
reads. This applied to every generated pnpm and yarn build, not only to
workspaces.

The build context for a generated Dockerfile no longer carries `.git` or any
`node_modules`, and no build carries a git worktree checked out inside the
repository. The partner's first run was refused at the 2 GiB limit because of
76 GB of agent worktrees under `.worktrees` and `.claude/worktrees`.

`af up --only <service>` brings up the named services and what they depend on,
so a large manifest no longer has to be edited before the two services anybody
wanted can run.

`af status` said "Nothing is running" while the database of an `af up` that
failed in its build was still up. It now says the run left something standing
and that `af down` removes it, and the JSON carries the count as `standing`.
The report on a failed `af up` counts the database too.

`af up` without `af init` left `.antifailure/` showing as untracked in `git
status`. The directory now carries its own `.gitignore`.
