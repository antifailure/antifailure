# Continuous deployment

How a commit becomes a running deployment, what stops one that should not, and
the evidence that both actually work.

## The shape

```
merge to main  ->  gate  ->  build  ->  staging                      (automatic)
push a v* tag  ->  gate  ->  build  ->  staging  ->  [approval]  ->  production
```

`.github/workflows/cd.yml` drives it. The two scripts that do the work are
`deploy/cd/deploy.sh` and `deploy/cd/health-gate.sh`, in the repository rather
than inside the YAML, so they can be read and run outside a CI log.

| | |
| --- | --- |
| Staging | `https://app.dev.antifailure.dev` |
| Resource group | `af-cp-centralus` |
| Container App | `afcp-app`, Multiple revision mode |
| Bootstrap job | `afcp-bootstrap` |
| Image | `ghcr.io/antifailure/control-plane`, deployed **by digest** |
| Azure identity | `af-infra-ci`, OIDC, no stored credential |

## The order, and why it is that order

1. **Migrations first, in a job, before any traffic moves.** They are the
   irreversible half of a deploy. If they fail nothing has changed and the old
   revision is still serving. Rolling traffic first and migrating second means a
   failed migration has already broken the running application.
2. **The new revision starts at zero traffic** and is checked on its own
   address. A revision that cannot start never receives a real request.
3. **Traffic shifts.**
4. **The public origin is checked**, including which commit answers.
5. **Any failure after step 2 puts traffic back** on the revision that was
   serving and deactivates the new one.

Step 5 is fast only because the app runs in Multiple revision mode: the old
revision is still up with no traffic, so the way back is one API call. In Single
mode it would be a rebuild, which is minutes during which the bad build serves.

## The gate checks which commit is answering

`/readyz` reports the build it was stamped with, and the health gate compares it
to the commit being deployed. A healthy **wrong** build fails.

This exists because a rollout that silently did not happen leaves the previous
build passing every probe perfectly. A gate that only asks "does the origin
answer" calls that a success and every later deploy inherits a lie about what is
running.

`/health` is not used for this. It is a static literal that touches nothing, on
purpose, so that a liveness probe cannot turn a slow database into a restart
loop. On this deployment's first day it answered `200` for thirteen minutes
while the database had no schema and every endpoint that read a table returned
`500`.

## Deviation from the brief, stated plainly

The brief said "Helm upgrade with the migration pre-check". This deploys with
Container Apps revisions instead, and Vir made that call when it was put to him.

The reasoning: the control plane is one web process and a Postgres. The cheapest
always-on AKS control plane is about 75 USD a month before a node runs; this
whole stack is roughly 32. The Terraform already targeted Container Apps and
`afcp-env` already existed. The guarantees the Helm wording asks for are all
here — migration pre-check, post-deploy health gate, automatic rollback — and
the rollback is better than `helm rollback`, because the previous revision is
still running rather than being rebuilt.

The Helm chart is unchanged and still proven: `control-plane-image.yml` installs
it on a real kind cluster on every pull request, including the assertion that
matters, `pg_has_role('af_app','antifailure_app','MEMBER')`. It remains the
artifact self-hosting operators use. We do not run it ourselves.

## Why it waits for CI rather than checking anything itself

The `gate` job polls for CI's conclusion on the same commit and refuses anything
but success. CI already runs on this push and is the promise the repository
makes about itself; running the same checks again here would double the work
and, worse, the two runs could disagree, at which point nobody knows which
verdict was real.

The first version ran `just gate` directly and failed with exit 127 across half
its recipes. `just gate` is a workstation command that assumes vale, lychee,
cspell and an npm install in three workspaces are present; `ci.yml` does not use
it for exactly that reason, and copying it here without its prerequisites
produced a gate that failed for reasons unrelated to the commit.

The cost is real and was chosen deliberately: while `main` is red for a reason
that has nothing to do with the control plane, staging does not move. Staging
keeps serving the last green build, which is a working system, and the fix is to
fix `main`.

## Proof

### A commit deploys itself

Run `33146545883` on `8376958`, 2026-08-28. `gate` waited for CI's conclusion on
that commit, `build` pushed the image, `staging` deployed it.

```
=== migration pre-check: running afcp-bootstrap on the new image
=== migrations applied
=== creating revision afcp-app--c8376958b-061404 at zero traffic
=== smoke test on the new revision only: https://afcp-app--c8376958b-061404...
ready on commit 8376958bf1a480374c752a8b97ea5ee8f4178ebc after 1 attempt(s)
=== shifting 100% of traffic to afcp-app--c8376958b-061404
=== DEPLOYED: https://app.dev.antifailure.dev is serving 8376958...
```

Checked against the live origin rather than the workflow's own report:

```
$ git log --oneline origin/main -1
8376958 cd: wait for CI rather than re-running it badly

$ curl -s https://app.dev.antifailure.dev/readyz
{"ready":true,"version":"main","commit":"8376958bf1a480374c752a8b97ea5ee8f4178ebc"}

$ az containerapp ingress traffic show -n afcp-app -g af-cp-centralus
afcp-app--c8376958b-061404   100
```

`GET /` went from 500 to 200 in the same deploy, because the running image
finally had the console in it.

Four attempts were needed. Three failures, all avoidable: an action pinned to a
SHA that does not exist, a gate job running `just gate` without the vale, lychee
and npm prerequisites that command assumes, and a federated credential created
only in the mutable subject form when this repository presents the immutable
one. Each time the pipeline failed at the right step and deployed nothing.

### A bad build is caught and never reaches a user

Run by hand with `deploy/cd/deploy.sh`, the same script CI runs, against
`ghcr.io/antifailure/control-plane:v0.1.1` -- a real image that predates
`/readyz` and answers it with a 500. Nothing was planted in the source: the
image is genuinely unable to satisfy the gate, which is a better test than a
commit written to fail.

```
=== currently serving: afcp-app--c8376958b-061404
=== migrations applied
=== creating revision afcp-app--c8376958b-011710 at zero traffic
=== smoke test on the new revision only: https://afcp-app--c8376958b-011710...
attempt 1/30: not ready - HTTP 500: {"error":"This endpoint has no declared rate limit..."}
...
HEALTH GATE FAILED after 30 attempts over 90s.
=== THE NEW REVISION IS NOT HEALTHY. Traffic never moved;
    afcp-app--c8376958b-061404 is still serving.
```

Exit 1. Throughout, and afterwards:

```
$ curl -s https://app.dev.antifailure.dev/readyz
{"ready":true,"version":"main","commit":"8376958bf1a480374c752a8b97ea5ee8f4178ebc"}
```

**What this proves and what it does not.** It proves the property that matters
most: a revision that cannot serve never receives a request, because the gate
runs on the revision's own address while every real request still goes to the
previous one. Traffic never moved, so there was nothing to move back.

It does NOT exercise the post-promotion rollback, the branch where traffic has
already shifted and the public origin then fails. Reaching that branch honestly
needs a build that is healthy on its own address and unhealthy behind the
ingress, and manufacturing one would test the manufacture rather than the
deploy. That branch is written, it is read in review, and it is unproven. Said
here rather than implied to be covered.

## What is not automated, and why

**Terraform is not applied by CD.** `infra.yml` plans on every pull request that
touches `infra/` and posts the destroy count; applying is still a person. That is
deliberate after the incident below, and it is the one place where a human gate
buys more than it costs: an infrastructure plan is the only thing here that can
destroy something.

**Production refuses.** The approval gate on the `production` environment is
real, the federated credential is real, and the job promotes the same digest
staging tested rather than rebuilding from the tag. What does not exist is a
production Container App, because that is a decision to spend money nobody has
made. The job says so and exits non-zero. A job that quietly deployed
"production" to staging's app would be worse than one that stops.

## The incident that shaped this

On 2026-08-28, two agents held different configurations of the same Terraform
stack. One applied a module containing a container registry from a working tree
whose changes were in no branch; the other planned, saw resources that existed
in no configuration, and applied a plan that destroyed them. The registry was
not recoverable.

Neither half was the whole cause. Applying uncommitted infrastructure is what
put resources in state that nobody could review. Chaining `terraform plan | grep
… && terraform apply` in one command is what meant the destroy list was printed
and never read.

Three things changed:

- Everything this deployment runs on is in a ref. `staging.tfvars` records the
  values `app.dev.antifailure.dev` is deployed with.
- Every plan since has been read in full before applying, and the destroy count
  is posted to the team notepad when it is not zero. The last apply was
  `0 to add, 5 to change, 0 to destroy`.
- `azguard` learned to read a resource group out of an ARM resource id, so a
  scoped `az role assignment create` is checked against the boundary instead of
  being refused with advice that cannot be followed.

## Things that were broken and are now not

Found by deploying rather than by reading:

- **The schema could never apply on Azure.** `CREATE EXTENSION pgcrypto` in
  `0001_init.sql` is refused unless the extension is named in the
  `azure.extensions` server parameter, which defaults to empty. Invisible in
  every other proof, because they all use a stock `postgres` image.
- **The migration runner discarded the real error.** A failed migration leaves
  the connection in an aborted transaction, so the advisory unlock in the
  `finally` block fails too, and an exception from a `finally` replaces the one
  being propagated. Operators saw `current transaction is aborted` pointing at
  the unlock. With the fix reverted the test does not merely fail, it hangs: the
  lock is leaked onto a pooled connection and the next migration waits forever.
- **A plan that would have destroyed the Key Vault.** The module computed
  `afcp-kv` from `var.name`; the live vault is `afcp-kv-centralus`. A vault
  cannot be renamed.
- **The budget could not be updated at all.** Azure refuses a notification with
  no contacts, so removing the personal address made every apply fail with a
  400.
- **`infra.yml` could not reach the state.** Without `use_azuread_auth` the
  backend authenticates by calling `listKeys` on a storage account that has
  key-based authentication disabled outright.
