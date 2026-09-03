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
  workflow_dispatch:
    inputs:
      command:     { type: choice, options: [up, down, agents, load, scenario, explore], default: up }
      workflows:   { required: false, default: '' }
      duration:    { required: false, default: '' }
      scale:       { required: false, default: '' }
      seed:        { required: false, default: '' }
      concurrency: { required: false, default: '' }
      run_id:      { required: false, default: '' }
```

`up` and `down` bring the environment up and take it away. The four other
values run a [workload](/docs/concepts/workloads), and the step that handles
them is one `af workload run` invocation rather than a case arm per verb. That
command refuses an input the verb's own command has no flag for, rather than
dropping it, and writes a result document carrying what was measured and the
plain `af` command that reproduces it.

The values are verbs rather than the kind names the control plane stores, and
that is deliberate. GitHub reads this trigger list from your **default
branch** and answers a dispatch carrying an undeclared value with a 422, which
looks exactly like the file being missing. Renaming them would make every copy
of this file already in the wild start failing on the values that work today.
`scenario` and `explore` need this newer file; the rest work on the older one.

`examples/github-workflow.yml` carries the whole file. Two things about this
cost an afternoon if you meet them the hard way: GitHub reads the trigger list
from the **default branch**, so adding `workflow_dispatch` on a feature branch
alone changes nothing, and a dispatch to a workflow without it answers 404
rather than saying what is wrong.

The console checks all of that when you choose a repository, not when you press
the button, and says what is missing in the form. It does not disable the
button: the check can be a few seconds out of date by the width of whatever you
just did on GitHub, and a form that refuses to submit because of a stale read
is worse than one that tries and tells you.

`agents` resolves to a browser workflow and `load` to an observed load mix. The
result says which kind a verb resolved to, so nobody has to infer it.

GitHub refuses a dispatch that carries an input the workflow does not declare,
so a workflow still carrying the older four-input block runs `up`, `agents` and
`load` and refuses `scenario` and `explore`. Copy the current example over
your file on the default branch to get the rest. Nothing is lost while you
have not: the workload run is recorded either way, and an engine can claim it.

The control plane records nothing about the environment when it dispatches.
The run appears in your Actions tab, and the environment appears in the console
when the engine reports it, the same way it does for a run you started
yourself. A workload run is the one thing it does record before dispatching,
because the run names a definition that lives only in the control plane, and
"asked for and never picked up" is a state you need to be able to see.

## The one secret without which nothing appears in the console

The workflow needs `AF_CONTROL_PLANE_TOKEN` in its environment. Create one with
`af token create ci` and put it in the repository's secrets:

```yaml
env:
  AF_CONTROL_PLANE_TOKEN: ${{ secrets.AF_CONTROL_PLANE_TOKEN }}
```

Nothing about the work needs it. The environment comes up, the agents run, the
report lands on the pull request, and the job exits with the right code, all
with no token at all. What it decides is whether any of that is **reported**.

Without it the engine sends no events and claims no hosted run. The console's
environment list stays empty, and a workload somebody started from the console
is dispatched, runs to completion, and is recorded as *abandoned* at its
deadline, because the control plane never heard from it. That reads as a
plumbing fault in the product and it is a missing repository secret.

If a run is stuck in the console saying nobody reported on it, and the Actions
tab shows it finishing perfectly well, this is why.

Leave it out if you do not use the hosted control plane. `af ci` on a pull
request needs none of it.

## What the comment carries

One comment per pull request, edited in place rather than added to. A bot that
adds a comment on every push is a bot people mute, and a muted bot reports
nothing.

The comment carries a headline saying what the run amounted to, a link to the
environment in the console, a row per workflow with its verdict and the detail
behind it, steps for reproducing anything that did not pass, and a footer naming
the branch, the commit, the duration and the golden it branched from.

It also carries what the data said. Every invariant the manifest declares is
asked after the workflows, and a violated one puts the violating rows in the
comment and the failure in the headline, so a run where every workflow passed
and the data is broken does not read as a pass.

And it carries what this change does to the database and the network: the
migrations rehearsed against a branch of the golden, the locks they held, what
Postgres rewrote, the lint findings, the plans that changed, the hosts the
environment reached for, whether the branch read back masked, and what teardown
removed. Each of those is ranked by the manifest's
[policy block](/docs/concepts/verdicts), worst first, and the ones set to
`fail` are what stop the merge.

## Signature verification

```
AF-GH-001 The webhook signature did not verify.
```

Every delivery is verified against the App's secret before anything is read. An
unverified webhook is an unauthenticated request asking for an environment to be
created, so this fails closed and says nothing more: telling a caller why their
forgery failed helps them forge better.

## API failures

```
AF-GH-002 The GitHub API rejected the request: 403 Resource not accessible by
integration.
```

Almost always a permission the App was not granted, or a token from a workflow
with a narrower `permissions:` block than the job needs. The message carries
GitHub's own words, which name the missing scope.

Related: [scheduling](/docs/concepts/scheduling), [the control plane](/docs/self-hosting/control-plane).
