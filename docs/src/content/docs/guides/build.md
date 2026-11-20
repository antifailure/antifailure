---
title: Building services
description: How an image is produced for each service, and what to do when it will not build.
sidebar:
  order: 1
---

Each service in the manifest becomes an image. If the repository has a
Dockerfile, that is used. If it does not, a buildpack is detected from what is
there.

```yaml
services:
  - name: web
    kind: web
    command: npm start
    port: 3000
    migrate: npx prisma migrate deploy
    build:
      strategy: auto        # auto, dockerfile, or buildpack
      dockerfile: ./Dockerfile
      context: .
```

`strategy: auto` prefers a Dockerfile and falls back to a buildpack, which is
almost always what you want. The other two are for saying explicitly which one
should be used when both would work.

## What detection finds

`af init` reports what it decided and why:

```
web   node   package.json and pnpm-lock.yaml put this on Node 22 with pnpm.
```

The sentence is the useful part. If it says something you did not expect, the
detection is wrong and the manifest is where to correct it, by hand, once.

## No strategy could be detected

```
AF-BLD-010 No build strategy could be detected for worker.
```

Nothing in the service's directory said what it is: no `package.json`, no
`go.mod`, no `requirements.txt`, no `Dockerfile`. Either point `build.context`
at the right directory, or add a `Dockerfile` and set `strategy: dockerfile`.

Detection deliberately refuses to guess rather than picking the buildpack that
fits worst. A wrong guess produces an image that builds and then fails at run
time, which is a longer way to the same answer.

## Lockfiles

With a lockfile the install is frozen: `npm ci`, `pnpm install
--frozen-lockfile`, `yarn install --frozen-lockfile`. Without one it falls back
to `npm install` and says so, because the environment is then not running the
dependency versions production runs, and a result from it means less than it
appears to.

Commit a lockfile. It is the difference between an environment that reproduces
a bug and one that might.

## A package in a workspace

A service whose `path` is a package inside a pnpm, yarn, npm or bun workspace
is built as a member of that workspace, not as a project of its own. The
generated Dockerfile copies the whole repository in, runs the frozen install
once at the root, builds the package, and starts it from its own directory:

```
web   node   apps/web/package.json and the pnpm workspace at the root put this on Node 22, so the whole workspace installs from pnpm-lock.yaml and the service starts from apps/web.
```

The whole repository, because a workspace install reads files from all of it:
every member's `package.json`, the patch files named by
`patchedDependencies`, `.yarn`, and whatever a member's `postinstall` runs.
Copying only the package and the lockfile fails on the first of those, often
as `ERR_PNPM_PATCH_NOT_FOUND`.

The build runs through whatever orders the workspace, so a package that
imports another package's compiled output finds it built:

- with `turbo.json` and `turbo` in the root `package.json`:
  `turbo run build --filter=<name>...`
- with pnpm: `pnpm --filter <name>... run build`, the package and everything
  it depends on, in dependency order
- with npm or yarn: the package's own `build` script

A package with its own lockfile is treated as a project that happens to sit in
a subdirectory, and installs in that directory.

## What a generated build leaves out

A generated Dockerfile is Antifailure's file, so Antifailure decides what it
reads. Its build context never holds `.git` or any `node_modules`, whatever
`.dockerignore` says: dependencies are installed inside the image, for Linux,
and a `node_modules` copied from a Mac afterwards would replace them. A rule in
`.dockerignore` that re-includes something, such as `!vendor/node_modules`,
still wins. A Dockerfile you wrote keeps Docker's behaviour, where only your
`.dockerignore` decides.

Every build, generated or not, leaves out a git worktree checked out inside the
repository, such as `.worktrees/fix-login` or `.claude/worktrees/agent-1`. Each
is a second copy of the repository, and coding agents create one per task. The
build says which it left out:

```
web: left 2 git worktrees out of the build, checked out inside this repository: .claude/worktrees/agent-1, .worktrees/fix-login
```

A submodule is kept. Its `.git` points into `.git/modules`, not
`.git/worktrees`, and its source belongs to the build.

## Bringing up some of the services

```bash
af up --only web
```

`--only` brings up the named services and everything they need: their
`depends_on`, all the way down, and the service a datastore's `rebuild` runs
in. Name several with commas or by repeating the flag. It is the way to get the
services you care about running in a large manifest before the rest of it
builds, without editing the manifest.

## A build that fails

```
AF-BLD-001 The build for service web failed after 34s.
  Next: Read the build log above; the first error line names the step that
  failed.
```

The full log is printed on failure, always, even without `--verbose`. During a
successful build it is hidden, because a Docker build prints a line per
instruction and a line per layer and burying two useful lines under seventy is
not help.

## A context that is too large

```
AF-BLD-003 The build context for web is 1.8 GiB, above the 500 MiB limit.
AF-BLD-004 The build context for web holds more than 20000 files;
node_modules/.cache/x is where the count was reached.
```

Both mean the same thing: the context is carrying output as well as source. Add
a `.dockerignore`:

```
node_modules
dist
.next
coverage
*.log
```

The limits exist because sending a gigabyte to the daemon on every build makes
`af up` feel broken, and the usual cause is one directory nobody meant to
include. The error names the path where the count was reached, so you know
which one.

## Layer order

A generated Dockerfile installs dependencies before copying source, so editing
a file does not reinstall the dependency graph. If you write your own, do the
same: it is the difference between a two second rebuild and a two minute one.

The exception is a workspace member, described above. Its install needs the
whole repository, so it runs after the copy, and a source change reinstalls.
That is slower to rebuild and it is the version that installs.

## Builder choice

Antifailure uses Docker's BuildKit builder when the local daemon supports it.
On a daemon that requires a BuildKit session, it uses the Docker Buildx CLI if
available and loads the resulting image into the same daemon used to run the
environment. If Buildx is unavailable, the build continues with Docker's
legacy builder. The build log says when either fallback is used.

Set `DOCKER_BUILDKIT=0` to use the legacy builder deliberately. Antifailure
still checks its content digest before building, so an unchanged service image
is reused regardless of the builder.

Related: [detection](/docs/concepts/detection), [the local runtime](/docs/guides/local-runtime).
