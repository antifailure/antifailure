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

## Why the full gate

`cd.yml` runs `just gate` before it builds. That is the promise the repository
makes about itself, and a deploy that ran a narrower check than a pull request
did is a deploy that can ship something a pull request would have refused.

The cost is real: while `main` is red for a reason that has nothing to do with
the control plane, staging does not move. That is the correct trade, and Vir
chose it explicitly. Staging keeps serving the last green build, which is a
working system, and the fix is to fix `main`.

## Proof

### A commit deploys itself

<!-- FILL: run id, commit, and the /readyz output after the merge of #15 -->

### A planted failing health check rolls back

<!-- FILL: run id, the planted commit, the traffic weights before and after -->

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
