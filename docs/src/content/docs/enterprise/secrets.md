---
title: Enterprise secret stores
description: Vault, AWS, Azure and Google in the lookup chain, and what each one says when it cannot answer.
sidebar:
  order: 6
---

*Requires an enterprise license with the `enterprise_secrets` feature, and the
enterprise binary built from `ee/`.*

The community edition looks for a declared variable in four local places: this
shell, `.env`, the encrypted store beside it, and the system keyring. The
enterprise edition adds four more, asked after every local one.

## Which stores are asked

Nothing is detected. A store is asked because you named it:

```sh
export AF_SECRET_SOURCES=vault
```

The order is the order you write, and it decides which of two stores holding the
same variable answers.

Nothing is auto-detected. A store you named that cannot be built stops the engine
at startup with the reason, rather than resolving your variables out of `.env`
instead.

## Where they sit in the chain

1. This shell's environment
2. `.env`
3. The encrypted local store
4. The system keyring
5. **Every store you named, in order**

Last, so an export you typed and a file in this repository both override the
company secret manager.

## HashiCorp Vault

```sh
export AF_SECRET_SOURCES=vault
export VAULT_ADDR=https://vault.example.com:8200
export VAULT_TOKEN=...            # or an AppRole, below
export AF_VAULT_PATH=antifailure  # the secret holding your variables
```

One secret holding every variable is the shape this expects, because that is how
they are usually organised: one document per application with the variables as
its keys. For an organisation whose access policies are per path:

```sh
export AF_VAULT_PATH_PER_NAME=1
export AF_VAULT_FIELD=value       # the field read at {path}/{NAME}
```

An AppRole instead of a token, which is what CI has and what can be renewed:

```sh
export VAULT_ROLE_ID=...
export VAULT_SECRET_ID=...
```

A token supplied by a person is not renewed. It belongs to somebody, it may be a
root token, and calling `renew-self` on it is presumptuous, so a rejection is
final on the first try. An AppRole logs in again, once.

Other variables: `VAULT_NAMESPACE` for Vault Enterprise, `AF_VAULT_MOUNT` when
the KV engine is not at `secret`, and `AF_VAULT_KV_V1=1` for the older engine.

**The KV version is the thing that goes wrong.** Reading a version 2 mount as
version 1 has no path without the `data/` segment, and reading a version 1 mount
as version 2 has no path with it, so both answer 404 and both present as "the
variable is not set" for a variable that is plainly there in the UI. The engine
reads the mount's own metadata and says so:

```
the mount secret is KV version 2 and this source is configured to read
version 1, which would report every variable as absent
```

## AWS Secrets Manager

```sh
export AF_SECRET_SOURCES=aws
export AWS_REGION=eu-west-1
export AF_AWS_SECRET_ID=antifailure/production   # one secret holding every variable
```

Or one secret per variable, which costs more because Secrets Manager charges per
secret per month:

```sh
export AF_AWS_SECRET_PREFIX=antifailure/production/
```

Credentials are looked for in three places, in this order: `AWS_ACCESS_KEY_ID`
and `AWS_SECRET_ACCESS_KEY` in the environment, the ECS or Pod Identity
credential endpoint, and the EC2 instance role through IMDSv2. A profile in
`~/.aws/credentials` and a web identity token file are **not** read, and the
message says so rather than reporting "no credentials" and leaving you to guess
which of five mechanisms was meant to supply them.

IMDSv2 only. Version 1 answers an unauthenticated GET.

## Azure Key Vault

```sh
export AF_SECRET_SOURCES=azure
export AZURE_KEY_VAULT_URL=https://your-vault.vault.azure.net
export AZURE_TENANT_ID=...
export AZURE_CLIENT_ID=...
export AZURE_CLIENT_SECRET=...
```

Leave the tenant, client and secret unset to use the managed identity of the
host it runs on, which is the better path where it exists because there is no
key material anywhere.

**A Key Vault secret name may hold only letters, digits and hyphens**, and an
environment variable is conventionally `SCREAMING_SNAKE_CASE`. `DATABASE_URL` is
not a name the service will accept and never was. Underscores are mapped to
hyphens, so store it as `DATABASE-URL`, and the source says so in the list of
places it looked. A name that cannot be mapped is refused rather than stripped:
stripping would map two different variables onto one secret.

`AZURE_AUTHORITY_HOST` for Azure Government (`https://login.microsoftonline.us`)
or the China cloud (`https://login.partner.microsoftonline.cn`).

**The service principal needs `Key Vault Secrets User` and nothing more.** That
role grants get and not list, so a 403 on a listing is the normal state of a
correctly configured installation, and the source reports the vault as reachable.

A vault that cannot be reached is reported as unreachable even when the
credential is perfect. Microsoft Entra and the vault are different hosts, so a
vault behind a firewall rule, a private endpoint, or a typo will still issue a
valid token, and a source that stopped at the token would call itself healthy
and leave you reading AF-SEC-001 wondering why the value never arrived.

## Google Secret Manager

```sh
export AF_SECRET_SOURCES=gcp
export GOOGLE_CLOUD_PROJECT=your-project
export GOOGLE_APPLICATION_CREDENTIALS=/path/to/key.json   # or nothing, on Google
```

On Cloud Run, GKE or Compute Engine, leave the credentials unset and the
attached service account is used, which needs no key on disk.

`AF_GCP_SECRET_PREFIX` prepends to every name. `AF_GCP_SECRET_VERSION` defaults
to `latest`, which is what rotation is for. `AF_GCP_SECRETMANAGER_ENDPOINT` for
a regional endpoint where data residency requires one.

## When a store cannot answer

Every source says why, and the reason is printed beside its name:

```
AF-SEC-001 The variables STRIPE_SECRET_KEY are declared in the manifest but
were not found in any configured source.
  Next: Add them to one of the searched sources: this shell's environment,
  .env (not present), the encrypted local store (no passphrase is set),
  the system keyring, HashiCorp Vault at https://vault.example.com:8200
  (secret/antifailure) (is sealed).
```

A store that cannot be used is named with its reason: the vault is sealed, the
token expired, the licence lapsed.

Run `af explain` to see the same list without starting anything.

## A credential that is refused

Every cloud store here authenticates with a token that expires, so a long-lived
process will eventually present a stale one. That gets exactly one renewal, once
per process. A second rejection is not an expiry:

```
AF-SEC-002 The credential for Azure Key Vault at https://af.vault.azure.net
was rejected after one refresh: Key Vault answered 403 Forbidden.
  Next: Rotate the credential and store the new value where it reads it.
```

Retrying will not help. One renewal per process rather than one per lookup, so
twenty declared variables against a revoked credential are not twenty rejections.

## What happens when the licence lapses

The stores are still configured and nothing is deleted. They report themselves
as unavailable with the reason, the chain steps over them, and every local
source works exactly as it did:

```
the enterprise_secrets feature needs a licence and none is installed
```

The check happens on every lookup rather than once at startup, so a licence that
expires while a long-running process is up stops the feature rather than
carrying on until somebody restarts it. Renewing turns it back on unchanged.
