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
| `*.tf` files | The Terraform root modules, which become [`infrastructure.paths`](/docs/reference/manifest#infrastructure) |

The dependency list is the one that surprises people. A `stripe` dependency
produces an egress rule for `api.stripe.com` in sandbox mode, a `resend`
dependency produces one for `api.resend.com` in capture mode, and a `sentry`
dependency produces a block with a sentence saying why.

Terraform is the one source where finding the files is not the whole job.
Every directory holding a `.tf` file is a module and most of them are not root
modules, so detection reads the `module` blocks, takes out the directories
something calls with a local source, and drafts what is left: the units that
are planned and applied on their own. A repository that only publishes modules
gets no section and a sentence saying why, because "we found no infrastructure"
and "we found only building blocks" are different facts.

It never drafts `infrastructure.workspace` or `infrastructure.var_files`, and
it says so under its own heading. Which workspace holds production, and which
of `production.tfvars`, `staging.tfvars` and `dev.tfvars` describes it, is not
stated anywhere in a repository. A file name is not a fact, and this is the one
section of the manifest that describes production rather than the copy, so
nothing downstream could catch a wrong answer.

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

Every question has a default, so a run with nobody at the terminal still
finishes. A port with no evidence defaults per language: 3000 for node and
ruby, 8000 for python, 8080 for go. A start command with no evidence defaults
to the conventional one where the language has one, such as `npm start` or
`go run .`, and a service where nothing can be guessed is dropped from the
draft with a note rather than failing the command. When standard input is not
a terminal, `af init` behaves as `--non-interactive` does: it takes every
default and lists each one under **Assumed**, which is also what `af ci` does
when it drafts a manifest for a repository that has none.

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
