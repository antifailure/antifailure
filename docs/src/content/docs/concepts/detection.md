---
title: Detection
description: How af init reads a repository, and what it does when it is not sure.
sidebar:
  order: 9
---

`af init` reads what is already in the repository and writes a manifest from it.
Every value it writes came from a file: a package manifest, a Dockerfile, a
compose file, a dependency list.

```sh
af init
```

It does not ask you to describe your application. Your application already
describes itself, in the files you use to run it.

## What it reads

| Source | What it yields |
| --- | --- |
| `package.json`, `go.mod`, `requirements.txt`, `Gemfile` | Language, version, start command, scripts |
| `Dockerfile`, `docker-compose.yml`, `Procfile` | Services, ports, commands, dependencies |
| Dependency lists | Third party APIs, which become egress rules |
| Migration directories | The migrate command |
| Cron and schedule files | Scheduled services |

The dependency list is the one that surprises people. A `stripe` dependency
produces an egress rule for `api.stripe.com` in sandbox mode, a `resend`
dependency produces one for `api.resend.com` in capture mode, and a `sentry`
dependency produces a block with a sentence saying why.

## What it says it is unsure about

```
Assumed
  database.present                         yes
  service.web.port                         3000

  These were not detected with confidence. Check them before you commit.
```

A guess presented as a fact is worse than a question. Anything inferred rather
than read is listed under **Assumed**, so the things worth a second look are
the short list rather than the whole file.

## When it cannot decide

```
AF-DET-001 More than one service could be the web service: web, api, frontend.
```

Rather than picking one, it says which candidates it found. Editing the
manifest once is faster than discovering next week that previews have been
building the wrong thing.

## Re-running it

`af init` writes the manifest once and does not regenerate it. Nothing rewrites
it behind your back, so an edit you make survives, and a later `af init` on a
repository that already has one tells you it is there rather than replacing it.

If the repository has changed enough to want a fresh look, delete the manifest
and run it again, or read the new one against the old with `git diff`.

Related: [the manifest reference](/docs/reference/manifest), [building](/docs/guides/build).
