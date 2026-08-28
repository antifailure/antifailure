# The hosted loop

What is live, where it is, and what is not done. Written to be checkable: every
claim below is either an address you can open or a thing marked deferred with a
reason.

## Addresses

| What | Where | State |
| --- | --- | --- |
| Control plane, staging | `https://app.dev.antifailure.dev` | live, managed certificate |
| Marketing site and docs, production | `https://antifailure.dev` | live, deployed by `deploy.yml` |
| Docs, staging | `docs.dev.antifailure.dev` | **not created** |
| Control plane, production | `app.antifailure.dev` | **not created** |
| Container App | `afcp-app` in `af-cp-centralus` | Multiple revision mode |
| Registry | `ghcr.io/antifailure/control-plane` | public read, pulled anonymously |

## What is live

**The control plane serves on a custom domain with a real certificate.**
`afcp-app` runs in `af-cp-centralus`, behind `app.dev.antifailure.dev` with an
Azure managed certificate, bound `SniEnabled`.

**Its database works, which it did not before.** Migration `0001_init.sql` opens
with `CREATE EXTENSION IF NOT EXISTS pgcrypto`, and Azure Database for PostgreSQL
refuses any extension not named in the `azure.extensions` parameter, which
defaults to empty. So the first statement of the first migration was refused, the
file rolled back, and the application came up with no schema at all: `/health`
answered `200` while every endpoint that touched a table returned `500`. The
extension is now allow-listed in Terraform and the bootstrap job has run.

**Sign-in is GitHub OAuth, with signups closed.** The OAuth App
`Antifailure (staging)` exists under the `antifailure` organization. Its secret
is in Key Vault and in a GitHub environment secret; it is in no file.
`AF_SIGNIN_ALLOWLIST` names who may sign in at all, and a refusal happens during
the callback before any row is written, so a refused account leaves nothing
behind. **Set but empty means nobody, not everybody** — an empty value is far
more likely to be a deployment script that lost one than a decision to admit the
world.

**Continuous deployment.** See `docs/plan/notes/cd.md`. A merge to `main` runs
the full gate, builds, migrates, deploys a revision at zero traffic, checks it,
promotes it, checks the public origin including *which commit answers*, and rolls
traffic back if that fails.

**`af login`, `af logout`, `af whoami`.** The device authorization grant against
the control plane, issuing a scoped token stored in the operating system keyring.
The token can read environments and runs and write events; it cannot manage
members or read a provider key, and asking for more is intersected away rather
than granted.

**The console.** Environments, runs with verdicts and artifacts, masking
attestation history, network policy, the audit log, members, provider keys, and
the device approval screen. Server-rendered, no JavaScript, same origin as the
API so the session cookie is the session the pages read.

**Bring your own key.** Anthropic and OpenAI keys sealed with AES-256-GCM under
a secret held outside the database and bound to the organization and provider, so
a row copied between tenants does not open. A monthly cap per provider is checked
*before* the key is decrypted. A provider with no budget row cannot spend
anything: a missing cap reads as zero, not unlimited.

## Proofs

### Continuous deployment

<!-- FILL after the merge of #15 -->

### A random GitHub account gets no access

<!-- FILL -->

### The key never appears anywhere

The unit and integration tests assert this by grepping what the system actually
wrote rather than by reading the code: the `provider_keys` row dumped to text,
every `audit_entries` row dumped to text, and the console listing serialised to
JSON, each searched for the key and for its prefix. `tools/scanrepo` runs over
the whole tree in CI using the engine's own detector.

<!-- FILL: the live grep over logs, events and artifacts after a real run -->

## Not done, and why

Listed honestly rather than folded into the section above.

**The console is live but empty.** There is no GitHub App, so no installation
exists, so no repository is connected, so there are no environments, runs or
artifacts to show. The pages render their empty states, which say what would
appear and why nothing has. This is the largest remaining gap against the brief,
which asked for a populated console including the dogfood repository, one
environment, one run with artifacts, and network decisions. Creating a GitHub App
needs the web UI and a passkey; the OAuth App was created that way and the same
session can create this one.

**No live BYOK run.** The storage, the budget enforcement, the rotation and the
console are built and tested. What has not happened is a real `af test` against
the corpus starter with a real key from each provider, the spend recorded, and
both keys rotated afterwards. Vir will enter the keys through the console on the
live site; that is the point at which this becomes proven rather than written.

**No `af provider` CLI commands.** BYOK is reachable through the console only.
The brief asked for both. The store is provider-agnostic and the tRPC surface it
needs is small, but it is not written.

**`af login` is not verified from a clean machine.** It is tested against a real
control plane in the API suite and its client is tested against a scripted
server, but nobody has timed install-to-`af env list` on a machine that has never
seen this product. That number is the claim worth making and it has not been
measured.

**No Linux or Windows keyring.** `internal/secrets` has a system keyring on
macOS and `nil` elsewhere, so on those platforms `af login` stores the token in
`~/.antifailure/credentials/` with mode `0600` inside a directory with mode
`0700`. It says so when it does. Writing a Secret Service and a Credential
Manager backend is worth doing and is not worth guessing at, which is the same
reasoning `keyring_other.go` already gives.

**No SSH access for the cofounder.** He has no public key on his GitHub account
and declined to provide one when asked. The runbook change is a few lines and
cannot be written honestly without knowing which machine is the agent
workstation.

**No staging docs site.** `docs.dev.antifailure.dev` does not exist. Production
docs deploy from `main` through `deploy.yml`, so the gap is a staging preview
rather than a broken path.

**No production control plane.** The promotion path is wired end to end —
approval gate, federated credential, and promoting the digest staging tested —
and the job refuses because there is nothing to deploy into. That is a decision
to spend money that nobody has made.

## Two claims in STATUS.md that were wrong

`docs/self-hosting/control-plane.md` was thought to be unfollowable because the
ghcr package reads as private in its settings. It is not: the package inherits
access from a public source repository and pulls anonymously, which the running
Container App proves by pulling it with no registry credential at all. The
earlier conclusion came from a token request with unencoded colons in the scope,
which returns 401 in a way indistinguishable from a private package. An ACR was
built on that false premise and has been removed.

Phase 8.10 said Terraform "plans clean" and that what remained was a decision to
spend. Both were true and neither was sufficient: the plan was clean and the
deployment it produced could not migrate.

## Costs

About 32 USD a month for the staging stack, against a 300 USD budget on
`af-cp-centralus` enforced by `azurerm_consumption_budget_resource_group`. No
registry cost: the earlier ACR was removed.
