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

Related: [detection](/concepts/detection/), [the local runtime](/guides/local-runtime/).
