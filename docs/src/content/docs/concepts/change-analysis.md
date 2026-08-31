---
title: Change analysis
description: What a pull request touches, which checks exercise it, and what reading a diff cannot tell you.
sidebar:
  order: 5
---

Every check in this product costs something: a branch of a golden, a build, a
browser, a few minutes of a runner. Running all of it on a change to a README
is waste, and running the default on a change that adds a column and edits the
billing service is not enough attention.

`af change` reads the diff and says which checks will exercise what it touched.
It names the file and the rule behind every line of it, so the reasoning can be
argued with.

```
af change                          against the base branch this job names
af change --base origin/main       against a ref you choose
af change --diff pr.patch          against a diff you already have
```

```
4 files changed, touching the schema, the api service and an outbound host.
5 checks will run, and 1 more is selected and not configured.

  run   environment  api/billing.ts: the manifest declares the service api at the repository root
  run   migration    migrations/20260824_add_billing_status.sql: the path is inside a migrations directory
  run   invariants   migrations/20260824_add_billing_status.sql: the path is inside a migrations directory
  run   workflows    api/billing.ts: the manifest declares the service api at the repository root
  gap   load         load is off in the manifest, and af ci generates it only with --load
  run   egress       api/billing.ts: an added line names api.stripe.com, which the manifest routes to mode mock
  skip  masking      nothing this change touches is exercised by it
```

## What it will not tell you

It does not say whether a change is safe, and it does not grade it. There is no
score and no risk word in the output, because both would be a judgement made
from a file listing, and this product's whole argument is that judgement comes
from running the thing.

What it produces is one shape of sentence: this file is X, and X is exercised
by check Y. Every conclusion carries the path that produced it and the name of
the rule that fired, so a wrong classification can be found and corrected
rather than argued with.

## A path nothing recognises runs everything

If any changed path matches no rule, every check is selected. The same is true
of a diff too large to classify and of a diff with no files in it, which is
either an empty change or the wrong base ref, and nothing here can tell those
apart.

This is a deliberate asymmetry. A path wrongly classified as documentation
skips work that should have happened and nobody finds out; a path wrongly
treated as unknown costs a run that was not needed and is visible in the
report. Only one of those two mistakes is discoverable.

The consequence to expect: a repository with an unusual layout will select
everything until its manifest says otherwise, and that is the intended
behaviour rather than a bug to file.

## The checks and what selects them

| Surface | What it is | Selects |
| --- | --- | --- |
| `schema` | a migration directory, a `.sql` file, a schema a migration tool reads | environment, migration, invariants, load |
| `service` | a file under a path a service in the manifest declares | environment, workflows |
| `code` | application source | environment, workflows, load |
| `asset` | something the application serves: a stylesheet, an image, a template | environment, workflows |
| `build` | a Dockerfile, a compose file, a build configuration | environment |
| `dependency` | a package manifest or a lockfile | environment, egress |
| `config` | configuration the application reads | environment, workflows |
| `manifest` | `antifailure.yaml` itself | environment, egress |
| `masking` | the masking rules file the manifest names | masking |
| `egress` | an outbound host named in an added line | egress |
| `infrastructure` | infrastructure as code | nothing |
| `pipeline` | continuous integration configuration | nothing |
| `test` | your own test suite | nothing |
| `docs` | prose | nothing |

The four surfaces that select nothing are not oversights. The environment is
built from `antifailure.yaml` rather than from your Terraform, nothing in a run
reads your workflow files, and this product runs the workflows the manifest
declares rather than your test suite.

## Selected is not the same as available

A check is reported twice: whether this change selects it, and whether the
manifest configures it at all. The interesting line is the one that is both
selected and unavailable, because it means something changed and nothing is
going to look at it.

```
gap   invariants   the manifest declares no invariants, so nothing is asked of the data after the workflows
```

A report that showed only "invariants: not run" would read the same whether the
change did not need them or whether nobody ever wrote any.

## Outbound hosts

An added line naming an `http` or `https` URL is checked against the egress
policy, using the same code that decides real traffic in the sidecar. So a
pull request that starts calling something new says so before the run:

```
egress hooks.slack.com: an added line names hooks.slack.com, which no egress
rule matches, so the default of block applies
```

Only added lines are read, and only in source, configuration and the manifest.
A URL in a README is a link and not a call.

## Teaching it your layout

The built in rules cover the conventions most projects use. A repository that
puts something somewhere they do not predict declares it:

```yaml
change:
  rules:
    - path: packages/*/src/**
      surface: code
    - path: ops/**
      surface: infrastructure
      note: the deployment scripts, which no environment runs
```

A single star does not cross a slash and a double star does. The longest
matching pattern wins, so order does not decide and appending a rule cannot
silently change what an existing one does.

Three things a rule cannot do. It cannot assign `service`, `manifest`,
`masking` or `egress`, which come from declarations already in the manifest and
would be a second answer to disagree with the first. It cannot turn a check
off, because a rule says what a path is and the engine decides what that
implies. And it cannot match every path: a catch all would classify everything
and the fail safe above would never fire again, so the manifest refuses one.

```
change.rules[0].path: The change rule pattern "**" matches every path.
```

## In a pull request check

Inside a GitHub Actions job, `af change` writes one output per check, so a
later step can skip work this change does not need:

```yaml
- id: change
  run: af change

- name: The full check
  if: steps.change.outputs.environment == 'true'
  run: af ci
```

The value is the check being both selected by the change and configured in the
manifest, because a step asking whether to do work needs both. `selected` holds
the same list as a comma separated string.

## What a diff cannot see

Stated in the report itself, on every run, because a report that implies
coverage it does not have is worse than no report:

- It reads paths and added lines. It does not run the program, so a one line
  change to a configuration default can change behaviour nothing here can see,
  and a thousand line refactor that changes nothing will still select every
  check its files touch.
- A caller left behind in a file the diff does not touch is invisible. The
  build is what finds that.
- Columns a migration adds do not exist in the golden yet, so nothing has
  checked whether they will need a masking rule once they carry production
  data. The masking check reads the golden, not the diff.
- A rename is classified by the new path, so moving a file between categories
  changes the classification without changing a line of code.
- A binary file has no added lines to read.
- The workflow agents drive a browser, so a change to a `worker` or a `cron`
  service is exercised only where the application's own interface reaches it,
  and a diff cannot say whether it does.

A check that is not selected was not run. That is a statement about what was
exercised, not a finding that the untouched parts are correct.

## Errors

`AF-DET-010` is the common one, and it is almost always a shallow checkout: a
job cloned one commit deep shares no history with its base branch, so there is
no merge base to diff against. `fetch-depth: 0` fixes it.

`AF-DET-011` means the file passed to `--diff` is not git's unified format.
Produce it with `git diff --unified=0 base...head`.
