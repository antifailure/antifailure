---
title: GitHub
description: An environment per pull request, and the two ways to run it.
sidebar:
  order: 13
---

```yaml
github:
  mode: actions        # or app, or off
  comment: true
  fork_policy: label
  teardown_on: [close, merge, ttl]
```

`comment` and `fork_policy` are read and acted on by the engine. `mode` and
`teardown_on` are printed by `af explain` and read by nothing, which is not an
oversight and is worth knowing before you set one: see
[the manifest reference](/docs/reference/manifest#github) for what happens
instead, and why the hosted control plane cannot read your manifest.

## Two ways to run it

**Without a control plane.** Everything happens inside the workflow. No server,
nothing to host. `af ci` brings the environment up, runs the agents, writes the
report and tears down, and the workflow's last step posts that report as one
comment which it edits in place. The environment lives for the length of the
job.

**With one.** The workflow does exactly the same work, and then tells the
control plane what happened. The control plane publishes a **check run** the
repository can require, maintains the comment itself, and owns the parts a
workflow cannot do: stopping a run when the pull request closes, noticing a run
that never reported, and keeping the history.

Which one you get is decided by one repository variable, `AF_CONTROL_PLANE`.
Set it to the control plane's address and the two extra steps in the example
workflow run; leave it unset and they are skipped and the workflow comments for
itself. There is no mode to configure and nothing to keep in step.

It is a **variable** on your repository rather than a secret, because it is an
address and not a credential, and it is read by your workflow rather than by the
control plane. Do not confuse it with `AF_CONTROL_PLANE_TOKEN`, which is one
word longer and a different thing entirely: an engine token, for `af` talking to
a control plane from a terminal. Nothing here needs one.

That last sentence is a claim about the code rather than a wish, and this is
what makes it true. A workflow talks to a control plane twice, and neither call
carries a stored credential:

- **The engine**, while the run is happening, reporting the events that say an
  environment is coming up, is ready, or has been torn down.
- **The report step**, at the end, publishing what the run concluded.

Both trade the same thing for a short-lived credential: the workflow identity
GitHub signs for a job with `id-token: write`. That is the one permission the
example workflow declares for this, and it is the whole of the setup. The
credentials each call gets back are scoped and expire on their own, so there is
nothing to rotate and nothing to leak, and a fork's pull request cannot obtain
either, because GitHub does not mint an identity for one.

If you set `AF_CONTROL_PLANE_TOKEN` anyway, the engine uses it and does not ask
for an identity. That is the path for a self-hosted engine that is not running
in GitHub Actions, and it stays supported.

## The reusable workflow and the action

The file in a customer's repository is about thirty lines, and the reason is
that it does almost nothing itself. Its one job calls a reusable workflow in
this repository, and that workflow calls the action:

```yaml
jobs:
  check:
    uses: antifailure/antifailure/.github/workflows/check.yml@v1
    secrets: inherit
    with:
      dispatch: ${{ toJSON(inputs) }}
      control-plane: ${{ vars.AF_CONTROL_PLANE }}
```

**`.github/workflows/check.yml`** is the reusable workflow. It runs on the
caller's event with the caller's `github` context, so the fork label gate and
the concurrency group read the caller's pull request, which is where they
belong. It checks out with `fetch-depth: 0`, because `af change` diffs against
the merge base, and then calls the action with the caller's secrets as one JSON
value. It exists as a workflow rather than only as an action because of that
last part: a composite action cannot read a caller's secrets, and
`secrets: inherit` is only available to a reusable workflow.

**`action.yml`** is the action, `antifailure/antifailure@v1`. It installs `af`,
installs the agent runner when the command needs a browser, works out what the
change touches, runs the command, tells a control plane what happened when
there is one, and leaves the comment otherwise. Every input reaches a script
through `env:` rather than through an expression inside a `run:` block, so an
input carrying a quote cannot become a command.

The `secrets` input is the part worth understanding. The reusable workflow
passes `toJSON(secrets)`, which is every secret the caller has. The action does
not export them all. `af change` reports the variables the manifest names,
`database.source_url_env` among them, and the action exports exactly those,
by name, out of the JSON. Anything the caller already set through `env:` is
left alone. The JSON is dropped before the engine starts, so it reaches no
build and no container, and GitHub masks the values in the log either way. A
repository whose manifest names `PRODUCTION_DATABASE_URL` therefore needs a
secret of that name and nothing in its workflow file mentions it.

**`v1`** is a moving tag. The release workflow moves it to every final release
`v1.x.y` after the release is published, and never to a prerelease, so a
customer's file names the major version once and follows the releases without
a line to change. The `version` input pins the `af` binary the action installs,
and is separate from the tag: the tag chooses the workflow and the action, the
input chooses the engine. Both inputs, every output, and the case for calling
the action directly are on [the action reference](/docs/reference/action).

## The check

One check run per commit, named **Antifailure**, so a branch protection rule can
require it. The name is stable on purpose: changing it would silently
un-require the check on every repository that named it.

| The check says | GitHub's conclusion | Merges behind a required check? |
| --- | --- | --- |
| Every check passed | `success` | yes |
| A check failed | `failure` | no |
| Blocked before anything could be checked | `action_required` | no |
| Nothing was verified | `action_required` | no |
| Nothing was verified: the run never reported back | `timed_out` | no |
| Superseded by a newer commit | `cancelled` | no |
| Waiting for a runner / Building the environment | not concluded | not yet |

**Blocked and nothing-was-verified are not passes.** The temptation is GitHub's
`neutral`, which reads as "nothing to say", and `neutral` PASSES a required
check. A pull request whose agents never ran would then merge behind a green
tick, which is the failure this product exists to make impossible: `af test`
exits zero on `unverified`, so a green job means the job exited, not that
anything was checked.

GitHub's conclusion vocabulary is smaller than ours, so two of ours share
`action_required`. They stay apart in the check's title, which is the first line
anybody reads, and in the comment.

## Which of your workflow runs is the check

A GitHub App is delivered a `workflow_run` event for **every** workflow in your
repository, not only the one that runs Antifailure. A repository with one
workflow never notices. A repository with seventeen does, and this one did: a
lint job that finishes green in fifty seconds looks, from the outside, exactly
like the check finishing without reporting, and the check on this repository's
own pull requests read "Nothing was verified" for its entire life because a
security scan kept crossing the line first.

So a workflow run has no standing here until it says which run it is, and it
says so by asking for a callback credential:

```
POST /v1/pr/callback-token
Authorization: Bearer <the workflow identity GitHub signed>
{"head_sha": "<the commit>"}
```

The `run_id` inside that identity is GitHub's own claim about the job, not
something the workflow asserts, so no job can introduce itself as another one.
From then on that run is the check: its completion decides the verdict when it
did not report, and cancelling it is how an environment gets torn down.

**Ask for it before the work, not beside the report.** The example workflow does
this in its second step, and the three things it buys are all lost by asking at
the end:

- the check reads "Building the environment and running the agents" for the
  minutes that is true, rather than "Waiting for a runner" while one is working;
- a job that dies halfway is a run this control plane can name and cancel, which
  is its only route into the runtime holding your environment;
- a run that dies is reported in seconds rather than at the deadline.

A commit where no run ever introduces itself is not passed and not failed. It
sits until the deadline and then reads "Nothing was verified: the run never
reported back", which is `timed_out` and true: nothing came back at all.

## One comment, about one commit

The comment's first line carries the commit it is about. That is not decoration:
somebody pushes while a check is running, the first run is cancelled, the
cancellation finishes after the second run started, and without the fence the
comment ends up reporting a commit that is no longer the head with nothing to
say so. A result that is stale in a way the reader cannot detect is worse than
no result.

So a run whose commit is no longer the head updates its own check, which is
correct because that check belongs to that commit, and does not touch the
comment.

## A fork never reaches a secret

A pull request from a fork runs code somebody outside your organisation wrote.
Two independent things keep it away from your credentials, and neither is
sufficient alone.

GitHub withholds your repository's secrets and the workflow identity token from
a pull request job running on a fork. That is GitHub's rule and it needs nothing
from you.

And the control plane issues no callback credential for a fork's commit until a
maintainer adds the `antifailure:allow` label. **The approval is for that exact
commit.** The next push withdraws it, because a maintainer approved code they
read and the next push is code nobody read. The check on an unapproved fork
commit says so, with the label to add.

**What the approval does and does not buy, said plainly.** GitHub's own rule is
that a `pull_request` job on a fork gets a read-only token, no secrets, and
therefore no workflow identity to exchange, so a fork's own job cannot report a
result to a control plane whatever anybody grants it. The label is what makes
the control plane willing to ACCEPT a result for that commit; the result still
has to come from a run that can prove itself, which means a maintainer starting
one from the console or from the Actions tab against the base repository.

Without a control plane there is nothing for the job to report to, so none of
this arises: the workflow runs on the fork's pull request, `af ci` does its
work, and the comment step posts the report with the `pull-requests: write` the
job already has. The fork still gets no secrets, which is GitHub's doing and not
this product's.

That GitHub rule is documented rather than observed here. Establishing it would
mean opening a fork pull request against this repository, which is a public
action nobody has approved, so it is stated as GitHub's documented behaviour and
not as something this project has watched happen.

## Forks

```yaml
  fork_policy: label     # never, label, or always
```

The section above is what GitHub and the control plane do on their own. This is
the part your manifest decides, and it is enforced by the engine on the machine
running the job.

`label` is the default and the right one: nothing runs until a maintainer adds
the `antifailure:allow` label, which is a person deciding. `never` refuses forks
whatever anybody labels. `always` runs everything, and is only reasonable for a
repository where every contributor already has write access.

### Where it is enforced

`af ci`, `af up`, `af test` and `af load run` all refuse, before an environment
is named and before the Docker daemon is touched. The refusal is `AF-GH-003`,
and `af ci` writes a report saying the check did not run rather than exiting
non zero, because a fork waiting on a maintainer is not a finding about the
change and `never` would otherwise leave every fork pull request permanently
red.

It applies to `pull_request` and to `pull_request_target`. The second one
matters most: it hands the base repository's secrets to a job checking out a
stranger's code, on purpose, which is exactly the configuration this exists for.

This is the gate that works on a self-hosted runner, where GitHub's own rule
buys you nothing: the Docker daemon, the registry login and the network are
already on the machine, and self-hosted is the ordinary shape here because an
environment needs a daemon and a golden.

### The policy is read from the base branch

Your manifest is in your repository, so on a fork pull request the checked out
`antifailure.yaml` is the fork's copy. Reading the policy from there would let
anybody add `fork_policy: always` to their own pull request and walk through the
gate, so the policy is read from the base branch instead, which is the only copy
a contributor cannot edit.

Two consequences worth knowing before they surprise you. Changing the policy
takes effect when the change lands on the base branch, not when it is proposed.
And a checkout that does not carry the base branch cannot be read, so the gate
falls back to `label` and says so in the report; the workflow template checks out
with `fetch-depth: 0`, which is also what `af change` needs.

### The workflow has to be woken by the label

Adding a label is an event, and a workflow that does not subscribe to it will
not run again when a maintainer approves. The template lists it:

```yaml
on:
  pull_request:
    types: [opened, synchronize, reopened, ready_for_review, labeled, unlabeled]
```

Without `labeled`, the approval is real and nothing acts on it until the next
push.

The control plane's own gate in front of this one is not configurable: it
applies `label` behaviour to every repository, because it never reads your
manifest, so it cannot honour `never` or `always`.

## Sending events with no token at all

A workflow that reports to a control plane needs a credential, and the obvious
one is wrong. A repository secret holding an engine token is readable by every
workflow in the repository, has to be created by a person before anything works,
and never expires, so it is the single thing most likely to still be valid a
year after whoever pasted it has left.

So the job proves who it is instead. GitHub Actions can mint a short lived
OpenID Connect token for a job, signed by GitHub, and the control plane
exchanges it for an engine token that expires in fifteen minutes.

```yaml
permissions:
  id-token: write        # without this GitHub mints nothing
  contents: read
```

```bash
# The identity, from the runner. ACTIONS_ID_TOKEN_REQUEST_* are set by the
# runner only when id-token: write is granted.
identity=$(curl -sS -H "Authorization: bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" \
  "$ACTIONS_ID_TOKEN_REQUEST_URL&audience=antifailure-control-plane" | jq -r .value)

# The exchange.
curl -sS -X POST "$AF_CONTROL_PLANE/v1/auth/github-oidc" \
  -H 'content-type: application/json' \
  -d "{\"token\": \"$identity\"}"
# {"token": "aft_...", "expires_at": "...", "org_id": "...", "repository": "owner/name"}
```

The audience is `antifailure-control-plane` and it is not optional. GitHub's
default audience is your organisation's URL, which every workflow of every
repository in the organisation gets by asking for nothing, so a token minted for
something else entirely would be a valid credential here. Naming an audience
makes the token useless anywhere else and makes a token minted elsewhere useless
here.

### The claim, which usually makes itself

Access to an organization comes from a claim on the repository, not from the
token. Most customers never make one by hand: when a repository has no claim and
exactly one organization has the Antifailure GitHub App installed on its owner,
the claim is created on the first exchange and recorded as having come from the
installation.

**Why a claim exists at all**, because this is the part that looks like
friction and is not. A GitHub identity token says, truthfully and with a
signature nobody can fake, "this job runs in repository R". It says nothing
about who R belongs to. Anybody with a GitHub account can create a repository,
put `id-token: write` in a workflow, and mint a genuine, correctly signed token
naming it. A control plane that read that claim and looked up "the organisation
for that repository's owner" would have verified a stranger's signature
perfectly and then let them write into whichever tenant the lookup landed on.

So the claim is what grants and the token only identifies. What the installation
changes is who makes the claim, not whether one is needed: an installation is
GitHub telling this control plane you control the account, checked against a
signature when it was delivered, which is the same evidence a manual claim is
measured against with one step fewer.

**What is refused** is a repository with no claim AND no installation to stand
in for one, with `"reason": "no_binding"`. A repository whose owner nobody has
installed the App on reaches nobody. So does one whose owner two organisations
have installed on, because choosing between them would decide which tenant your
events land in by the order rows come back, and that is refused rather than
guessed at.

One repository can be claimed by one organisation. A second claim is refused
with `"reason": "already_claimed"`.

**Claiming by hand** is for a repository the App is not installed on, or one you
want claimed before its first run. An owner or admin does it once:

```bash
curl -sS -X POST "$AF_CONTROL_PLANE/v1/oidc/bindings" \
  -H "authorization: Bearer $AF_CONTROL_PLANE_TOKEN" \
  -H 'content-type: application/json' \
  -d '{"repository": "your-org/your-repo"}'
```

Revoking a claim stops new exchanges **and kills the credentials that claim
already issued**, which is what makes it a revocation rather than a note:

```bash
curl -sS -X DELETE "$AF_CONTROL_PLANE/v1/oidc/bindings/your-org/your-repo" \
  -H "authorization: Bearer $AF_CONTROL_PLANE_TOKEN"
# {"revoked": true, "repository": "your-org/your-repo", "tokensRevoked": 1}
```

A fork gets none of this. GitHub does not grant `id-token: write` to a pull
request job running on a fork, so there is no identity to exchange, and the fork
case is closed by GitHub's own rules rather than by this control plane
remembering to check.

## Teardown, and what "torn down" means

An environment that outlives its pull request is the leak this product exists to
prevent, so teardown is asked for when the pull request closes or merges, when a
newer commit supersedes the run, and when a check times out.

**The only route this control plane has into the machine holding your
environment is asking GitHub to cancel the run.** It holds no cluster
credential, no kubeconfig and no address, by design, and `af ci` tears the
environment down on every exit including a cancelled one. So teardown is:
cancel, then come back and check, and it is not finished until GitHub says the
run reached a terminal state.

The console reports the state it is actually in, and none of them is a guess:

| Teardown | What it means |
| --- | --- |
| nothing to remove | no environment was ever reported for this commit |
| asked for | recorded, not confirmed |
| in progress | a cancel has been sent and the run has not stopped yet |
| done | the runtime confirmed it. The environment is gone |
| gave up | there was no route to it. Says so, and names `af down` |

That last row is the honest one. An environment with no live workflow run behind
it is one nothing here can reach, and reporting it torn down would be the same
lie the console used to tell: the button set a column and nothing anywhere read
it, so the page said the environment was gone while the containers kept running.

**`teardown_on` is accepted and read by nothing.** Teardown happens whatever you
put there, and there is no combination of its three values that turns it off. In
a workflow `af ci` tears down before it writes the report, including on a failed
job and including on a cancelled one, and the runner goes away at the end of the
job regardless. The `ttl` outcome is real and is configured somewhere else: the
ceiling on how long an environment may live is
[`runtime.max_ttl`](/docs/reference/manifest), and that one is read. `af explain`
says so against the setting, so the manifest and the command agree.

## What the App must be granted

[Standing up production](/docs/self-hosting/production#9-create-the-production-github-app)
carries the permission and event lists, with what each one is for and why the
rest are refused. It is one list rather than two so that they cannot drift.

The one worth knowing here: the console's controls need **Actions: write**, and
declaring it on the App is not the same as holding it. Widening an existing
App's permissions asks every installation to accept the new grant and changes
nothing until somebody does, so the App's settings page can read Actions: write
while every installation of it still refuses a dispatch.

GitHub does not name the state it refuses in, so the console works it out and
says which of these it is:

| What GitHub answers | What it can mean |
| --- | --- |
| `403 Resource not accessible by integration` | The installation holds no Actions write, **or** the App was never given that repository. |
| `404 Not Found` | There is no workflow file of that name on the default branch, **or** no repository of that name this App can see. |
| `422` | The branch does not exist, the workflow declares no `workflow_dispatch` trigger, or it does not declare the inputs the console sends. |

A missing permission is checked before the workflow file is looked for, so a
403 hides whether the file is even there: granting the permission can reveal a
second thing to fix.

## The pull request the App opens

Installing the App on a repository that has no workflow file is enough to get
one. The control plane records a setup row for each repository an installation
covers, and a sweeper works through them: it looks for
`.github/workflows/antifailure.yml` on the default branch, and when the file is
there the row is marked present and nothing else happens. When it is not, the
sweeper creates a branch called `antifailure/setup` from the default branch,
writes the file there, and opens a pull request titled **Check every pull
request with Antifailure**. An existing branch of that name is reused rather
than refused.

The pull request's body says what will happen once it is merged, that nothing
runs until then, what the fork policy does, the optional secrets by name, and
the one repository variable the hosted control plane needs. The webhook that
records the installation makes no GitHub call itself; the sweeper does the
work, so a burst of installations cannot time out a webhook delivery.

Writing a file needs **Contents: Read and write** on the App. An installation
that granted only read cannot have a branch created for it, and the row is
marked as needing permission with the remedy in one sentence, rather than
retried until it fails. Widening an existing App's permission asks every
installation to accept the new grant, which
[Standing up production](/docs/self-hosting/production#9-create-the-production-github-app)
walks through. Five failed attempts of any other kind mark the row failed with
the last error kept.

The console shows every state. The environments page and the empty
organization shell carry a "Getting connected" list with one line per
repository, its state, and a link to the pull request when there is one, so an
installation that is waiting on a merge or a permission is visible rather than
silently absent.

## Starting a run from the console

The console's **Create environment**, **Run agents**, **Run load**, **Run
workload** and **Tear down** controls do not run anything on the control plane.
They dispatch a run of your own workflow, in your own repository, on the branch
the environment is on. Your database, your secrets and your captured traffic
stay where they already are.

That needs two things. The App has Actions write, above. And the workflow
accepts a dispatch:

```yaml
on:
  pull_request:
    types: [opened, synchronize, reopened, ready_for_review, labeled, unlabeled]
  workflow_dispatch:
    inputs:
      command: { type: choice, default: up, options: [up, down, agents, load, scenario, explore], description: "Which part to run" }
      workflows: { description: "Comma separated names out of the manifest. Empty means all of them." }
      duration: { description: "How long to send load for, as a Go duration such as 60s" }
      scale: { description: "Multiplier on production's rate" }
      seed: { description: "Makes two runs do the same thing" }
      concurrency: { description: "Ceiling on requests in flight" }
      run_id: { description: "Leave it empty. The engine asks." }
```

Almost always a permission the App was not granted, or a token from a workflow
with a narrower `permissions:` block than the job needs. The message carries
GitHub's own words, which name the missing scope.

Related: [scheduling](/docs/concepts/scheduling), [the control plane](/docs/self-hosting/control-plane).
