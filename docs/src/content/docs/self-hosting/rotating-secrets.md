---
title: Rotating secrets
description: Every secret the control plane's Key Vault holds, what breaks while each one is being replaced, and how to prove the replacement took.
sidebar:
  order: 5
---

The Terraform in `infra/terraform/modules/control-plane` puts eight secrets in
one Key Vault. This page is one runbook for each: what it is, what stops working
while it is being replaced, the steps, and how to check the new value is the one
in use.

Read the honesty note before you run any of it.

## What has been rehearsed

None of these runbooks has been performed against the live deployment. Each is
derived from the Terraform and the application code, and every step names the
file it comes from so you can check the derivation rather than trust it.

Two of them carry a warning that is not a matter of rehearsal. Rotating
`provider-key-secret` destroys data and cannot be undone. Rotating
`github-app-webhook-secret` has a window during which GitHub deliveries are
refused. Both are described below rather than left to be discovered.

## What is in the vault

Ownership is the first thing to know, because it decides whether Terraform will
put your new value back.

| Secret | Who owns the value | What reads it |
| --- | --- | --- |
| `database-url` | Terraform generates it | the app, and the bootstrap job |
| `migration-database-url` | Terraform generates it | the bootstrap and maintenance jobs |
| `provider-key-secret` | Terraform generates it | the app |
| `github-client-id` | seeded once, then you | the app |
| `github-client-secret` | seeded once, then you | the app |
| `github-redirect-uri` | seeded once, then you | the app |
| `github-app-private-key` | you, entirely | the app |
| `github-app-webhook-secret` | you, entirely | the app |

Three kinds, and the difference matters when you rotate:

**Owned.** Terraform generated the value, so a difference between the
configuration and the vault is drift it will correct. Rotating one of these by
hand means the next `terraform apply` proposes to put the generated value back.

**Seeded.** Terraform wrote a placeholder once and then stopped, through
`ignore_changes` on the value in `keyvault.tf`. That line is what makes the
instruction to rotate these by hand true. Without it, the next apply would put
the placeholder back and break sign-in.

**Yours.** GitHub mints an App private key and shows it once, so Terraform can
neither create it nor recreate it. The module reads both App secrets with a data
source. Nothing here will overwrite them.

`github-redirect-uri` is in the vault with the others and is not a secret. It is
a public callback address. It is listed for completeness, and rotating it is a
configuration change rather than a security operation.

## Before any of them

**You need write access to the vault.** The role assignment that grants it is
off by default, for the reason in `keyvault.tf`: a role assignment whose
principal is whoever ran Terraform churns on every plan by a different caller.
Grant it once, by hand:

```sh
az role assignment create \
  --role "Key Vault Secrets Officer" \
  --assignee-object-id "$(az ad signed-in-user show --query id -o tsv)" \
  --assignee-principal-type User \
  --scope "$(terraform output -raw key_vault_id)"
```

**A new version in the vault is not a new value in the app.** The container app
references every secret by its versionless id, so the value a replica holds is
the one it read when it started. Do not wait for the platform to notice. Create
a revision, which reads the vault again:

```sh
az containerapp update -n afcp-app -g af-cp-centralus \
  --revision-suffix "rotate$(date -u +%Y%m%d%H%M)"
```

That app runs in `Multiple` revision mode, so the new revision starts with no
traffic and the old one keeps serving. Check the new revision on its own address
before shifting traffic to it. `deploy/cd/deploy.sh` does all of that in order,
including putting traffic back if the new revision fails its health check, and
running it is the safer way to pick up any of these values.

**Never print a secret.** `az keyvault secret set` takes the value on the
command line, which puts it in your shell history. Every runbook below reads the
value from a file or a pipe instead.

---

## `database-url`

**What it is.** The connection string the serving process uses, as `af_app`.
That role is a member of `antifailure_app`, owns nothing, and cannot run DDL.
Terraform generates the password in `database.tf` and assembles the URL in the
same file.

**What breaks while you rotate it.** Nothing, until a revision starts with the
new value. From that moment the app can only connect if Postgres knows the new
password too.

**The step nothing in this repository does for you.** The bootstrap job creates
`af_app` only when the role is absent, and leaves an existing one alone. Read
`deploy/docker/bootstrap.mjs`: it says so, and the reason is that silently
resetting the credential of a running system is worse than refusing to. So
changing the vault value alone gives the application a password the database has
never heard of. The `ALTER ROLE` is yours to run.

Postgres has no public endpoint, so you cannot run it from a laptop. It has to
come from inside the virtual network, which means a container app job using
`migration-database-url`.

**Steps.**

1. Generate the new password and hold it in a file with no other reader.

   ```sh
   umask 077
   openssl rand -base64 32 | tr -d '\n' | tr '+/' '-_' > /tmp/afpw
   ```

   The translation is not decoration. The URL is parsed with `new URL()`, and
   `+` and `/` in a password change what the parser reads.

2. Change the password in Postgres, from inside the network. Use the maintenance
   job's image and its migration credential:

   ```sql
   ALTER ROLE af_app PASSWORD '<the new password>';
   ```

3. Write the new URL to the vault, from a file:

   ```sh
   printf 'postgres://af_app:%s@%s:5432/antifailure?sslmode=require' \
     "$(cat /tmp/afpw)" "$PG_FQDN" > /tmp/afurl
   az keyvault secret set --vault-name afcp-kv-centralus \
     --name database-url --file /tmp/afurl --output none
   shred -u /tmp/afpw /tmp/afurl
   ```

4. Create a revision and shift traffic to it, or run `deploy/cd/deploy.sh`.

**How to verify.** The new revision reaching `Running` is not enough on its own:
the process starts without a database and does not connect until the first
request. Ask it for something that reads a table, then confirm the counter
moved.

```sh
curl -sf https://your-control-plane/health          # liveness only, proves little
curl -s https://your-control-plane/metrics | grep af_http_requests_total
```

**Afterwards.** `random_password.app` still holds the old value in Terraform
state, so the next plan will propose to put the old URL back into the vault.
Either import the new value or accept that this rotation needs a Terraform
change beside it. This is the sharpest edge on the page and it is a consequence
of the secret being owned rather than seeded.

---

## `migration-database-url`

**What it is.** The owner's connection string, as `af_migrator`. It runs
migrations and owns the tables. The serving app never holds it, which is the
point of the two roles: a process on a public address should not be able to drop
the policies that isolate tenants.

**What breaks while you rotate it.** Nothing that serves traffic. The bootstrap
job and the nightly maintenance job both use it, so a deploy or a partition
maintenance run inside the window fails.

**Steps.**

1. Reset the server administrator password. This is an Azure operation rather
   than a SQL one, because the login is the flexible server's administrator:

   ```sh
   az postgres flexible-server update -n afcp-pg -g af-cp-centralus \
     --admin-password "$(cat /tmp/afpw)"
   ```

2. Write the new URL to `migration-database-url`, the same way as above.
3. Run the bootstrap job, which proves the credential end to end:

   ```sh
   az containerapp job start -n afcp-bootstrap -g af-cp-centralus
   ```

**How to verify.** The bootstrap job reports `bootstrap complete` and exits
zero. It asserts the end state it exists to produce, so a run that achieved
nothing fails rather than reporting success.

**Afterwards.** `database.tf` carries `ignore_changes` on
`administrator_password`, so Terraform will not fight the reset on the server
itself. It will still propose to restore the generated URL in the vault, for the
same reason as `database-url`.

---

## `provider-key-secret`

**Do not rotate this one.** It is a one way door and there is no way back.

**What it is.** Thirty two bytes that seal every customer's stored provider key
under AES-256-GCM. `web/apps/api/src/providers/seal.ts` holds the shape. The
sealing key never reaches Postgres, so a database dump on its own decrypts
nothing.

**What breaks if you rotate it.** Every stored provider key, permanently. A
sealed value that will not open looks exactly like a tampered one, so the
failure is silent in the worst way: the rows are still there and none of them
work.

There is no re-sealing tool. The rows record a `keyVersion` and the comment
beside it says the version exists so a rotation can find the rows that still
need re-sealing. Nothing reads that column for that purpose. The rotation it
anticipates has not been built, and this page says so rather than implying the
column is a plan.

**What to do instead.** If the sealing key is compromised, the keys it sealed
are compromised too, and re-sealing them would be protecting values that already
need replacing. Tell each affected organization to revoke their provider key at
the provider and store a new one. Storing a key is a normal operation for an
owner or admin, from the console or from a terminal, and it is described in
[provider keys](/docs/guides/provider-keys).

An installation that does not want the feature can run with the secret unset.
The app then says so in its start-up log and in the console, and refuses a save
rather than accepting one it cannot seal.

---

## `github-client-id`, `github-client-secret`

**What they are.** The OAuth application that signs people in. Terraform seeds
both once and then leaves them alone.

**What breaks while you rotate them.** New sign-ins, for the length of the
window. Existing sessions are unaffected: a session is a row in the database,
and the OAuth credentials are used only to complete a sign-in.

**Steps.**

1. In the GitHub OAuth application's settings, generate a new client secret. Do
   not delete the old one yet.
2. Write it to the vault from a file:

   ```sh
   umask 077
   cat > /tmp/ghsecret   # paste, then Ctrl-D
   az keyvault secret set --vault-name afcp-kv-centralus \
     --name github-client-secret --file /tmp/ghsecret --output none
   shred -u /tmp/ghsecret
   ```

3. Create a revision, or run `deploy/cd/deploy.sh`.
4. Sign in, in a private window, all the way to a page that needs a session.
5. Only then, delete the old secret in GitHub.

Step 5 is the whole reason for the ordering. GitHub allows both secrets to be
live at once, so a rotation done in this order has no window at all.

**How to verify.** A completed sign-in is the verification. There is no shortcut
that proves the value without exercising it, because the failure mode is GitHub
refusing the exchange rather than the app refusing to start.

The client id is public and changes only when the OAuth application itself
changes. If you do change it, change `github-redirect-uri` in the same pass and
check that it matches the callback URL registered on the application, character
for character.

---

## `github-app-private-key`

**What it is.** The PEM key the App uses to mint installation tokens. Terraform
reads it and never writes it, which is why the module uses a data source.

**What breaks while you rotate it.** Nothing, if you do it in this order. An App
can hold more than one private key at a time, and both work until you delete
one.

**Steps.**

1. Generate a new private key in the App's settings. GitHub downloads a PEM and
   keeps the old key working.
2. Write the whole PEM, including the header and footer lines, to the vault:

   ```sh
   az keyvault secret set --vault-name afcp-kv-centralus \
     --name github-app-private-key --file ./downloaded.pem --output none
   shred -u ./downloaded.pem
   ```

3. Create a revision, or run `deploy/cd/deploy.sh`.
4. Exercise something that needs an installation token, such as a pull request
   comment on a repository the App is installed on.
5. Delete the old key in GitHub.

**How to verify.** The app refuses a half configured App at start-up, so a
revision that starts has a key it could parse. That is a weaker statement than
it looks: parsing is not the same as GitHub accepting the signature. Step 4 is
the verification and step 3 is not.

---

## `github-app-webhook-secret`

**This one has a window and it cannot be avoided.** An App has exactly one
webhook secret. The moment you change it in GitHub, deliveries signed with the
old one are refused, and the app is still holding the old one until a revision
starts.

**What it is.** The shared secret GitHub signs webhook deliveries with. Without
a valid signature the endpoint refuses the delivery, which is the behaviour you
want and the reason the window exists.

**What breaks.** Every delivery between the change in GitHub and the new
revision serving. GitHub records each one as a failed delivery and they can be
redelivered by hand from the App's advanced settings.

**Steps.**

1. Prepare the new value first, so the window is as short as you can make it.

   ```sh
   umask 077
   openssl rand -hex 32 > /tmp/whsecret
   ```

2. Write it to the vault. Nothing reads it yet.

   ```sh
   az keyvault secret set --vault-name afcp-kv-centralus \
     --name github-app-webhook-secret --file /tmp/whsecret --output none
   ```

3. Change it in the App's settings to the same value. The window opens here.
4. Create a revision immediately. The window closes when it serves traffic.
5. `shred -u /tmp/whsecret`.

**How to verify.** Redeliver a failed delivery from the App's advanced settings
and confirm GitHub records a 2xx. Do not accept the absence of new failures as
proof, because a quiet repository produces no deliveries to fail.

---

## What none of this covers

The engine's own credentials are not here. `af` stores a control plane token in
the operating system keyring, and rotating it is creating a new engine token and
setting `AF_CONTROL_PLANE_TOKEN`. Tokens are stored as a hash, so a control
plane database that leaks does not leak anything usable against it, and a
revoked token stops working immediately.

There is no automated expiry on any secret above and nothing warns you that one
is old. Rotation here is a decision somebody makes, not a schedule the
infrastructure keeps.
