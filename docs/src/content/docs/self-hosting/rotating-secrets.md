---
title: Rotating secrets
description: Every secret the control plane's Key Vault holds, what breaks while each one is being replaced, and how to prove the replacement took.
sidebar:
  order: 8
---

The Terraform in `infra/terraform/modules/control-plane` puts eight secrets in
one Key Vault. One runbook each below.

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

**Owned.** Terraform generated the value, so the next `terraform apply`
proposes to put the generated value back.

**Seeded.** Terraform wrote a placeholder once and then stopped, through
`ignore_changes` on the value in `keyvault.tf`. Without that line the next
apply would put the placeholder back and break sign-in.

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
off by default; see `keyvault.tf`. Grant it once, by hand:

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
`af_app` only when the role is absent and leaves an existing one alone; see
`deploy/docker/bootstrap.mjs`. Changing the vault value alone gives the
application a password the database has never heard of. The `ALTER ROLE` is
yours to run.

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
change beside it.

---

## `migration-database-url`

**What it is.** The owner's connection string, as `af_migrator`. It runs
migrations and owns the tables. The serving app never holds it.
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
zero.

**Afterwards.** `database.tf` carries `ignore_changes` on
`administrator_password`, so Terraform will not fight the reset on the server
itself. It will still propose to restore the generated URL in the vault, for the
same reason as `database-url`.

---

## `provider-key-secret`

**This can be rotated.** The steps below
add a second key, move every stored credential onto it, and then take the first
one away. Read all of them before starting: the order is the whole procedure.

**What it is.** Thirty two bytes that seal every customer's stored provider key
under AES-256-GCM. `web/apps/api/src/providers/seal.ts` holds the shape. The
sealing key never reaches Postgres, so a database dump on its own decrypts
nothing.

**How the keys are held.** The application holds a SET of sealing keys
addressed by version, so the old key and the new one are open at the same time.
A row names its version, is opened with the key that version names, and a row
whose version is not held produces its own error naming the missing version.
Look for that error in the logs if any step below goes wrong.

**What breaks while you rotate.** Nothing, if the steps are run in this order:
no key is removed until every row has been moved off it and verified.

**One thing to decide first.** If the sealing key is rotating because it was
COMPROMISED, re-sealing is the wrong operation: the keys it sealed are
compromised with it. Tell each affected organization to revoke their provider
key at the provider and store a new one, described in
[provider keys](/docs/guides/provider-keys). Rotate the sealing secret
afterwards, with these steps.

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
   `deploy.sh`.

   **The re-sealing job is not inside that target, and neither cd step will ever
   create it.** `tools/configguard` accepts a plan that changes
   `module.control_plane.azurerm_container_app.this` and nothing else, so
   `azurerm_container_app_job.reseal` is created once per environment by the hand
   apply below. After that, `deploy.sh` moves the job to each release's tested
   image, the same way it moves the maintenance job, and a deploy that runs before
   the job exists says so and carries on.

   Use the Terraform version cd uses, the one `TERRAFORM_VERSION` names in
   `.github/workflows/cd.yml` and `infra.yml` pins identically. A newer Terraform
   writing this state can leave it in a format cd's cannot read, and every deploy
   after that stops at the configuration apply.

   Run it after the deploy of this change to that environment has finished, so
   the image it pins is one that contains `backup-cli.mjs`. Staging, from a
   checkout of the commit that deploy carried:

   ```sh
   cd infra/terraform/stacks/control-plane
   terraform init -reconfigure -backend-config=backend.hcl
   export TF_VAR_subscription_id="$(az account show --query id -o tsv)"
   export TF_VAR_github_client_id=seeded-once-not-read-here
   export TF_VAR_github_client_secret=seeded-once-not-read-here
   img="$(az containerapp show -n afcp-app -g af-cp-centralus \
     --query 'properties.template.containers[0].image' -o tsv)"
   terraform plan -var-file=staging.tfvars -out=reseal.tfplan \
     -var "image_repository=${img%@*}" -var "image_digest=${img#*@}" \
     -target='module.control_plane.azurerm_container_app_job.reseal[0]'
   terraform show -json reseal.tfplan | jq -r '.resource_changes[]
     | select(.mode == "managed" and .change.actions != ["no-op"])
     | "\(.change.actions | join(",")) \(.address)"'
   ```

   The last command must print exactly one line,
   `create module.control_plane.azurerm_container_app_job.reseal[0]`. Anything
   else is a change this procedure has no business making: stop, and do not
   apply. When it does print that one line:

   ```sh
   terraform apply reseal.tfplan
   az containerapp job show -n afcp-reseal -g af-cp-centralus \
     --query 'properties.template.containers[0].[image, command]' -o tsv
   ```

   The image must be the one `img` held and the command `node backup-cli.mjs
   reseal`. Production is the same commands with `backend.production.hcl`,
   `production.tfvars`, `afcpprod-app` and `afcpprod-reseal` in
   `af-cp-prod-centralus`, run after the tag's production deploy has finished.

   The image is pinned on the command line because the job otherwise reads
   `image_repository` and `image_tag` from the stack's defaults, which can
   predate `backup-cli.mjs`. The job ignores later image changes from
   Terraform, so only `deploy.sh` moves it from then on.

   **Confirm the revision actually holds both keys before going further.** The
   start-up log names the versions, which is the only way to check this without
   decrypting somebody's credential:

   ```sh
   az containerapp logs show -n afcp-app -g af-cp-centralus --tail 200 \
     | grep 'sealing key'
   ```

   It must say `2 sealing keys (v1, v2)`. One key means the secret reference did
   not arrive and step 4 would report that no row can be opened.

3. Seal new keys under the new version. In the same tfvars:

   ```hcl
   provider_key_version = "v2"
   ```

   Merging this deploys it the same way. From here, a customer who saves a key
   gets it sealed under `v2` and every existing row still opens under `v1`.

   This is a separate deploy from step 2 on purpose: during a traffic shift
   both revisions serve, and a key sealed under `v2` cannot be opened by a
   revision that has not got `v2` yet.

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
   It re-seals revoked rows too, which is what makes step 5 unambiguous. And it
   covers every table sealed under these keys, not only provider keys: on the
   enterprise edition that includes each organization's audit stream collector
   credential, which its log reports as a table of its own. `backup-cli.mjs` is
   the image's launcher, the same path in both images, and the enterprise copy
   registers the enterprise tables before the tool starts. Pointed at a database
   holding sealed values in a table it was not told about, the tool refuses to
   run and names the table.

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

5. **Verify before removing anything.**

   ```sh
   az containerapp job show -n afcp-reseal -g af-cp-centralus -o json \
     | jq '{containers: [.properties.template.containers[0]
         | {name, image, command: ["node", "backup-cli.mjs", "reseal", "--check"],
            env, resources: {cpu: .resources.cpu, memory: .resources.memory}}]}' \
     > reseal-check.json
   az rest --method post --body @reseal-check.json \
     --headers Content-Type=application/json \
     --url "https://management.azure.com$(az containerapp job show \
       -n afcp-reseal -g af-cp-centralus --query id -o tsv)/start?api-version=2025-07-01"
   az containerapp job execution list -n afcp-reseal -g af-cp-centralus \
     --query "[0].{name:name, command:properties.template.containers[0].command}" -o json
   ```

   The last command must show `node`, `backup-cli.mjs`, `reseal`, `--check` as
   four separate entries before you read anything the execution reports. Without
   `--check` it is step 4, which writes.

   This is not `az containerapp job start --command`: the CLI takes that flag
   as a list, so a quoted command arrives as one program name with spaces in
   it, and it sends a container named after the job rather than `reseal` with
   no image and no environment. Every value the check needs comes from the job
   itself here, including the second key and the version a rotation adds in
   step 2.

   It opens EVERY row whatever version it is at and writes nothing. It must
   report zero rows that could not be opened and zero rows not yet at `v2`.

   A row still at `v1` here, reported as naming a key this revision does not
   hold or simply counted as not yet at `v2`, is not a failed job. It is a key a
   customer saved through a revision that was still sealing under `v1` after
   step 4 had finished, which no guard in the job can see because the write came
   after it.
   Run step 4 again and then this check again. Both are safe to repeat as often
   as it takes.

6. Remove the old key, which is the last proof that step 4 finished. In the
   tfvars:

   ```hcl
   provider_key_secret_enabled = false
   ```

   The feature does not go with it: `AF_PROVIDER_KEY_SECRETS` still carries `v2`,
   and `AF_PROVIDER_KEY_VERSION` still names it. What goes is `v1`, which nothing
   should now need.

   Merging it moves the container app, through the configuration apply. It does
   not move the re-sealing job, which still references `provider-key-secret`, and
   it does not remove the generated key. Both are one more guarded hand apply,
   the same commands as the job's creation in step 2 with this plan in place of
   that one:

   ```sh
   terraform plan -var-file=staging.tfvars -out=retire-v1.tfplan \
     -target='module.control_plane.azurerm_container_app_job.reseal[0]' \
     -target='module.control_plane.azurerm_key_vault_secret.owned["provider-key-secret"]' \
     -target='module.control_plane.random_bytes.provider_key_secret[0]'
   ```

   The `jq` line from step 2 must print exactly these three lines, in any order,
   and nothing else:

   ```text
   update module.control_plane.azurerm_container_app_job.reseal[0]
   delete module.control_plane.azurerm_key_vault_secret.owned["provider-key-secret"]
   delete module.control_plane.random_bytes.provider_key_secret[0]
   ```

   The job survives, because it exists while any sealing key is configured, and
   loses its reference to `v1`. The pull request that sets the flag shows the two
   destroys on its `plan` check, and `tools/planguard/destroys-acknowledged.tsv`
   needs a row for each in that same pull request, naming this rotation.

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
it is only ever the `v1` Terraform generated. The re-sealing job is still there,
because it exists while `provider_key_secrets_name` names a secret, and changing
the value of that secret is a vault write rather than a Terraform change, so later
rotations need no hand apply at all.

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

From a source checkout of the enterprise edition, run
`node ee/web/server/src/backup-cli.ts reseal` instead, which registers the audit
stream's table first; the community path refuses once any organization has
chosen an audit stream destination.

The connection string is read from the environment rather than taken as an
argument, because an argument is visible in `ps` to every user on the machine and
lands in shell history. `--url` exists for a terminal where that does not matter.

**On the Helm chart** the same rotation is three values under `providerKeys`, and
each one maps to a step above. Step 2 is adding `providerKeys.secrets` as
`v2=<the new key>` beside the existing `providerKeys.secret`, then `helm upgrade`.
Step 3 is `providerKeys.version: v2` and another upgrade. Step 6 is removing
`providerKeys.secret` once step 5 is clean. With `providerKeys.existingSecret`,
put `AF_PROVIDER_KEY_SECRETS` into that Secret instead; both sealing key
references are optional there, so the Secret may drop `AF_PROVIDER_KEY_SECRET`
at step 6 without the pods refusing to start.

Step 4 is a Job you run once, not a value. The chart deliberately does not
give the serving pods the migration connection, which is the credential this
tool uses, so the Job reads it from the chart's database Secret the way the
maintenance CronJob does. For a release named `cp` with the chart creating its
own Secrets:

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: cp-reseal
spec:
  backoffLimit: 0
  template:
    spec:
      restartPolicy: Never
      automountServiceAccountToken: false
      securityContext:
        runAsNonRoot: true
        runAsUser: 1000
        fsGroup: 1000
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: reseal
          # The image the release is running: kubectl get deploy
          # cp-antifailure-control-plane -o jsonpath='{..image}'
          image: ghcr.io/antifailure/control-plane:<version>
          command: ["node", "backup-cli.mjs", "reseal"]
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: ["ALL"]
          env:
            - name: AF_RESEAL_DATABASE_URL
              valueFrom:
                secretKeyRef:
                  name: cp-antifailure-control-plane-database
                  key: AF_MIGRATION_DATABASE_URL
            - name: AF_PROVIDER_KEY_SECRET
              valueFrom:
                secretKeyRef:
                  name: cp-antifailure-control-plane-provider-keys
                  key: AF_PROVIDER_KEY_SECRET
                  optional: true
            - name: AF_PROVIDER_KEY_SECRETS
              valueFrom:
                secretKeyRef:
                  name: cp-antifailure-control-plane-provider-keys
                  key: AF_PROVIDER_KEY_SECRETS
                  optional: true
            - name: AF_PROVIDER_KEY_VERSION
              value: v2
          volumeMounts:
            - name: tmp
              mountPath: /tmp
      volumes:
        - name: tmp
          emptyDir: {}
```

```sh
kubectl apply -f reseal-job.yaml
kubectl wait --for=condition=complete --timeout=30m job/cp-reseal
kubectl logs job/cp-reseal
```

With `database.existingSecret` or `providerKeys.existingSecret`, use those
Secret names instead. For step 5, delete the Job and apply it again with
`command: ["node", "backup-cli.mjs", "reseal", "--check"]`. The
chart's NetworkPolicy only restricts traffic into the control plane's own pods,
so it does not stand between this Job and Postgres.

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

**How to verify.** A completed sign-in is the verification.

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
revision that starts has a key it could parse. Parsing is not GitHub accepting
the signature: step 4 is the verification, not step 3.

---

## `github-app-webhook-secret`

**This one has a window and it cannot be avoided.** An App has exactly one
webhook secret. The moment you change it in GitHub, deliveries signed with the
old one are refused, and the app is still holding the old one until a revision
starts.

**What it is.** The shared secret GitHub signs webhook deliveries with. Without
a valid signature the endpoint refuses the delivery.

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
is old.
