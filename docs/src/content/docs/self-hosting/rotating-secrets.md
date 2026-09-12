---
title: Rotating secrets
description: Every secret the control plane's Key Vault holds, what breaks while each one is being replaced, and how to prove the replacement took.
sidebar:
  order: 8
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

One of them carries a warning that is not a matter of rehearsal. Rotating
`github-app-webhook-secret` has a window during which GitHub deliveries are
refused, and it is described below rather than left to be discovered.

`provider-key-secret` used to carry a worse one: rotating it destroyed every
stored provider key, permanently and silently. It no longer does. That runbook is
the longest on this page because it is the only one where the application holds
two values at once on purpose, and its steps are proven by
`web/apps/api/test/reseal.test.ts` against a real Postgres rather than derived
from the code. The proof is the last step: the old key is removed and everything
still opens.

## What is in the vault

Ownership is the first thing to know, because it decides whether Terraform will
put your new value back.

| Secret | Who owns the value | What reads it |
| --- | --- | --- |
| `database-url` | Terraform generates it | the app, and the bootstrap job |
| `migration-database-url` | Terraform generates it | the bootstrap and maintenance jobs |
| `provider-key-secret` | Terraform generates it | the app, and the reseal job |
| `provider-key-secrets` | you, entirely | the app, and the reseal job |
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

`provider-key-secrets` is the one secret on this page that does not exist until
you create it. It holds the sealing keys a rotation adds, and Terraform only
addresses it: the module builds its versionless id from the vault address and the
name, so nothing that plans this stack ever reads its value. Its runbook below
creates it.

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

**This can now be rotated, and before 2026-09-12 it could not.** The steps below
add a second key, move every stored credential onto it, and then take the first
one away. Read all of them before starting: the order is the whole procedure.

**What it is.** Thirty two bytes that seal every customer's stored provider key
under AES-256-GCM. `web/apps/api/src/providers/seal.ts` holds the shape. The
sealing key never reaches Postgres, so a database dump on its own decrypts
nothing.

**What used to break, and why it was silent.** Replacing the value in place made
every stored key stop opening, permanently. Rows recorded which key version
sealed them and nothing read that column, so the application tried every row
against the one key it held and reported the same failure for all of them: a value
that will not decrypt is indistinguishable from a value somebody altered. An
operator saw authentication failures across every organization and no sentence
saying why.

**What happens now instead.** The application holds a SET of sealing keys
addressed by version, so the old key and the new one are open at the same time.
A row names its version, is opened with the key that version names, and a row
whose version is not held produces its own error naming the missing version. That
error is the difference between a silent outage and a message, and it is the one
thing to look for in the logs if any step below goes wrong.

**What breaks while you rotate.** Nothing, if the steps are run in this order.
There is no window in which a stored key cannot be opened, because no key is
removed until every row has been moved off it and that has been verified.

**One thing to decide first.** If the sealing key is rotating because it was
COMPROMISED, re-sealing is the wrong operation: the keys it sealed are compromised
with it, and re-sealing protects values that already need replacing. In that case
tell each affected organization to revoke their provider key at the provider and
store a new one, which is a normal operation for an owner or admin and is
described in [provider keys](/docs/guides/provider-keys). Rotate the sealing
secret afterwards, with these steps, so the new keys are sealed under a key
nobody has seen.

### Steps

1. Generate the new key and write it to a vault secret of its own. Never into
   `provider-key-secret`, which Terraform owns and would put back.

   ```sh
   umask 077
   printf 'v2=%s' "$(openssl rand -base64 32)" > /tmp/afseal
   az keyvault secret set --vault-name afcp-kv-centralus \
     --name provider-key-secrets --file /tmp/afseal --output none
   shred -u /tmp/afseal
   ```

   The value is `v2=<32 bytes of base64>`. The version label is yours; `v2` is
   the obvious one after `v1`, which is what every existing row says. Several
   keys are comma separated, which is what a third rotation looks like.

   The old key is NOT in this value, and that is deliberate. The application
   merges `AF_PROVIDER_KEY_SECRET`, which is version `v1`, with
   `AF_PROVIDER_KEY_SECRETS`, so `v1` stays exactly where Terraform generated it
   and you never read a live sealing key out of the vault to compose a combined
   string.

2. Point the deployment at it, holding both keys and still sealing under the old
   one. In the environment's tfvars:

   ```hcl
   provider_key_secrets_name = "provider-key-secrets"
   ```

   Leave `provider_key_version` unset for now. This is a secret reference change
   on the container app, so merging it deploys it: `deploy/cd/apply-config.sh`
   plans the tfvars targeted at the container app and applies it before
   `deploy.sh`. The `azurerm_container_app_job.reseal` resource in the same
   change is NOT inside that target, so it needs one hand apply, which is the
   `terraform apply` in
   [the control plane runbook](/docs/self-hosting/control-plane).

   **Confirm the revision actually holds both keys before going further.** The
   start-up log names the versions, which is the only way to check this without
   decrypting somebody's credential:

   ```sh
   az containerapp logs show -n afcp-app -g af-cp-centralus --tail 200 \
     | grep 'sealing key'
   ```

   It must say `2 sealing keys (v1, v2)`. One key means the secret reference did
   not arrive and step 4 would report every row as unopenable.

3. Seal new keys under the new version. In the same tfvars:

   ```hcl
   provider_key_version = "v2"
   ```

   Merging this deploys it the same way. From here, a customer who saves a key
   gets it sealed under `v2` and every existing row still opens under `v1`.

   This is a separate deploy from step 2 on purpose. Both revisions serve for a
   few seconds during a traffic shift, and a key sealed under `v2` by the new
   revision cannot be opened by a revision that has not got `v2` yet. Making the
   set available first and switching which one seals second removes that window
   rather than relying on it being short.

4. Move every stored credential onto the new key. This is the job that did not
   exist:

   ```sh
   az containerapp job start -n afcp-reseal -g af-cp-centralus
   az containerapp job execution list -n afcp-reseal -g af-cp-centralus \
     --query "[0].{name:name,status:properties.status}" -o tsv
   ```

   It opens each row with the key its own version names and writes it back under
   `v2`, one row per transaction, a batch at a time rather than the table at
   once. It is idempotent and resumable, so starting it again after an
   interruption continues from where it stopped, and starting it twice is safe.
   It re-seals revoked rows too, which is what makes step 5 unambiguous.

   Read its log. It prints a count per version and it prints no key material:

   ```sh
   az containerapp job execution show -n afcp-reseal -g af-cp-centralus \
     --job-execution-name <name> --query properties.status
   ```

   Exit 3 means some rows could not be opened, and the log says which of two
   things that is. Rows under a version nothing holds means the environment is
   missing a key, which is step 2 not having taken. Rows that will not
   authenticate under a version that IS held means those rows are damaged or were
   moved between organizations, and they are a separate investigation. Nothing
   has been lost either way: a row the job cannot open is left exactly as it was.

5. **Verify before removing anything.** This is the step that separates a
   completed rotation from one that appears complete, and it asks a different
   question from step 4: not "what is left to do" but "does what has been done
   actually work".

   ```sh
   az containerapp job start -n afcp-reseal -g af-cp-centralus \
     --command "node apps/api/src/backup-cli.ts reseal --check"
   ```

   It opens EVERY row whatever version it is at and writes nothing. It must
   report zero rows that could not be opened and zero rows not yet at `v2`.
   Without this check, "nothing left to re-seal" and "every row is at the new
   version and none of them open" look identical.

6. Remove the old key, which is the last proof that step 4 finished. In the
   tfvars:

   ```hcl
   provider_key_secret_enabled = false
   ```

   The feature does not go with it: `AF_PROVIDER_KEY_SECRETS` still carries `v2`,
   and `AF_PROVIDER_KEY_VERSION` still names it. What goes is `v1`, which nothing
   should now need.

   This destroys `random_bytes.provider_key_secret` and the vault secret it
   wrote, so do not run it on a report you have not read. Key Vault soft delete
   keeps the destroyed secret for the vault's retention period, so a mistake here
   is recoverable within it, and outside it is not.

7. Run step 5 once more, against the revision that no longer holds `v1`. It must
   say the same thing. If it now reports rows under version `v1`, put
   `provider_key_secret_enabled` back to `true`, deploy, and go back to step 4:
   nothing is lost while the old key still exists in the vault.

**How to verify, end to end.** A customer request that spends the key is the only
complete proof, because it exercises the same `borrowKey` path the rotation
changed. Anything that calls `/byok/anthropic/v1/messages` will do. Short of
that, the console's Provider keys page still showing the same fingerprint and
last four for every organization is a good check that this moved the ciphertext
and not the value inside it: the fingerprint is of the plaintext, so re-sealing
cannot change it and a changed one would mean something opened the wrong row.

**Afterwards.** Every subsequent rotation is the same procedure with the version
numbers moved on: put `v3=<new>` alongside `v2` in `provider-key-secrets`, set
`provider_key_version = "v3"`, re-seal, check, and drop `v2` from the secret's
value. `provider_key_secret_enabled` stays false from the first rotation onward;
it is only ever the `v1` Terraform generated.

**On a self-hosted installation** with no Key Vault, the same three variables are
set however that deployment sets environment variables, and the tool is the same
one:

```sh
AF_PROVIDER_KEY_SECRET=<the old key> \
AF_PROVIDER_KEY_SECRETS=v2=<the new key> \
AF_PROVIDER_KEY_VERSION=v2 \
AF_RESEAL_DATABASE_URL=postgres://owner:...@db:5432/antifailure \
  node apps/api/src/backup-cli.ts reseal
```

The connection string is read from the environment rather than taken as an
argument, because an argument is visible in `ps` to every user on the machine and
lands in shell history. `--url` exists for a terminal where that does not matter.

**An installation that does not want the feature** can run with no sealing secret
at all. The app says so in its start-up log and in the console, and refuses a
save rather than accepting one it cannot seal.

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
