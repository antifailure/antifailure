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
behind. **Set but empty means nobody, not everybody**: an empty value is far
more likely to be a deployment script that lost one than a decision to admit the
world.

It was not set until 2026-08-28 14:02 UTC. See below: the allowlist was written
and tested and given no value, and this deployment accepted any GitHub account
in the world for its first week.

**Continuous deployment.** See `docs/plan/notes/cd.md`. A merge to `main` waits
for CI to go green on that commit, builds, migrates in a job, brings up a
revision at zero traffic, checks it on its own address, promotes it, and checks
the public origin including *which commit answers*.

**`af login`, `af logout`, `af whoami`.** The device authorization grant against
the control plane, issuing a scoped token stored in the operating system keyring.
The token can read environments and runs and write events; it cannot manage
members or read a provider key, and asking for more is intersected away rather
than granted.

**The console.** Environments, runs with verdicts and artifacts, masking
attestation history, network policy, the audit log, members, provider keys, and
the device approval screen. Server-rendered, no JavaScript, same origin as the
API so the session cookie is the session the pages read.

**Bring your own key, as far as storing one goes.** Anthropic and OpenAI keys
sealed with AES-256-GCM under a secret held outside the database and bound to the
organization and provider, so a row copied between tenants does not open. A
monthly cap per provider is checked *before* the key is decrypted. A provider
with no budget row cannot spend anything: a missing cap reads as zero, not
unlimited. Reachable from the console and from `af provider`.

What it does NOT do is get used. See below.

## Proofs

### Continuous deployment

Both are in `docs/plan/notes/cd.md`. Run `33146545883` deployed commit `8376958`
to `app.dev.antifailure.dev` on a merge to `main`, verified against the live
origin; and a knowingly bad image was refused on the revision's own address with
traffic never moving. The post-promotion rollback branch is written and unproven,
and cd.md says why.

### A random GitHub account gets no access

Two separate claims, proven in two different places, because one is about the
code and the other is about this deployment.

**What this deployment enforces**, from the serving revision's own start-up log:

```
$ az containerapp logs show -n afcp-app -g af-cp-centralus \
    --revision afcp-app--0000002 --tail 30
sign-in is restricted to 2 account(s) by AF_SIGNIN_ALLOWLIST
provider keys can be stored: AF_PROVIDER_KEY_SECRET is set
```

The two are `virsanghavi` and `maksymrajszewski`, set in
`stacks/control-plane/staging.tfvars` and applied through Terraform, so adding
somebody is a commit and a plan rather than an edit in a portal.

**What a refusal does**, from `web/apps/api/test/allowlist.test.ts`, which drives
the real two-step exchange against the real handler:

- an account not on the list gets `400`, the body says the instance is not open
  for sign-ups, and **no `set-cookie` is issued**;
- **no user row is written**, asserted by counting rows afterwards. A refusal
  that still created the account would only have postponed the problem to
  whenever somebody added a membership by hand;
- an account on the list signs in and gets a session, which is the negative
  control: without it a server that refused everybody would pass everything
  above;
- and that account still lands in no organization, because being let through the
  door is not being given a tenant.

**What is not proven.** Nobody has pointed a real, third-party GitHub account at
`app.dev.antifailure.dev` and been refused. The exchange above runs against a
GitHub stub, so what it does not cover is GitHub itself, and only that. The real
thing needs a second GitHub account, which nobody has offered. Said here rather
than implied to be covered.

**And it was not enforced at all until today.** The allowlist had an
implementation, a test file, and no value on the running deployment. The
application said so in its own start-up log from the day it went up, and nobody
read the log. This is the failure mode where every part of a feature exists and
nothing invokes it: it passes review from every direction except the one that
counts.

### The key never appears anywhere

The unit and integration tests assert this by grepping what the system actually
wrote rather than by reading the code: the `provider_keys` row dumped to text,
every `audit_entries` row dumped to text, and the console listing serialised to
JSON, each searched for the key and for its prefix. `tools/scanrepo` runs over
the whole tree in CI using the engine's own detector.

<!-- FILL: the live grep over logs, events and artifacts after a real run -->

## Not done, and why

Listed honestly rather than folded into the section above.

**A stored provider key has no consumer. Nothing spends it.** `borrowKey` is the
one function that decrypts a key, and `recordSpend` is the one that charges a
budget, and outside their own tests neither has a single call site anywhere in
this repository:

```
$ grep -rn 'borrowKey\|recordSpend' --exclude-dir=node_modules . \
    | grep -v providers/store.ts | grep -v test
(nothing)
```

So a customer can store a key, cap it, rotate it, watch it appear in the audit
log, and no run will ever use it. The engine reaches a model through
`ANTHROPIC_API_KEY` in the process environment. `runner/src/model.ts` and
`engine/cmd/af-proxy/synth.go` both read it from there, and the control plane
never calls a model at all.

That makes the brief's "run `af test` on the corpus starter once with each
provider, verify budgets enforce, spend caps hold" impossible as built, and not
because the keys are missing: `af test` would use the key in the shell's
environment and the stored one would sit untouched.

This is the same failure the allowlist had, found the same way, and it is
deliberately NOT patched over here, because closing it is a design decision
rather than a wiring one:

- **The control plane proxies model calls.** The key never leaves the server,
  which is what every comment in `providers/store.ts` already assumes. It is
  also a new service on the request path of every run.
- **The control plane serves the key to an engine that asks.** Small, and it
  puts a customer's key on every build machine that holds a token, which is the
  thing the current design refuses on purpose. It would mean deleting the rule
  that there is no route which returns a key.
- **The engine keeps using its own environment key and reports what a run cost**,
  so the cap is enforced against reported spend rather than at the point of
  spending. Honest about what it is: an accounting limit, not a spending one, and
  a machine that does not report is a machine with no cap.

Each produces materially different work and a different security posture, and
picking one on my own would be picking for Vir. Asked rather than assumed.

**The console is live but empty.** There is no GitHub App, so no installation
exists, so no repository is connected, so there are no environments, runs or
artifacts to show. The pages render their empty states, which say what would
appear and why nothing has. This is the largest remaining gap against the brief,
which asked for a populated console including the dogfood repository, one
environment, one run with artifacts, and network decisions. Creating a GitHub App
needs the web UI and a passkey; the OAuth App was created that way and the same
session can create this one.

**No live BYOK run.** The storage, the budget enforcement, the rotation and the
console are built and tested, and the live control plane can now store a key:
`AF_PROVIDER_KEY_SECRET` is generated by Terraform, held in Key Vault, and read
by the app's managed identity. It was not set until 2026-08-28 14:02 UTC either,
so before that a key entered through the console would have been refused rather
than stored.

What has not happened is a real `af test` against the corpus starter with a real
key from each provider, the spend recorded, and both keys rotated afterwards.
Vir will enter the keys through the console on the live site; that is the point
at which this becomes proven rather than written.

**`af provider` is done.** `af provider list / set / rm / budget` against
`/v1/providers`, behind a scope a plain `af login` does not carry. The key is
never an argument: there is no `--key` flag, and it is asked for without echo,
piped, or read from a named environment variable. There is no command and no
scope that reads a key back. See the guide at `guides/provider-keys`.

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

**No production control plane.** The promotion path is wired end to end: an
approval gate, a federated credential, and a promotion of the same digest
staging tested. The job refuses because there is nothing to deploy into. That is a decision
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
