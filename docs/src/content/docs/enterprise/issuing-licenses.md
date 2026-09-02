---
title: Issuing a license
description: How an enterprise license key is signed, delivered, reissued and withdrawn.
sidebar:
  order: 8
---

This is the vendor side of [licensing](/docs/enterprise/licensing). That page
describes installing a key. This one describes producing one.

Two audiences read it. The vendor issuing a paid license is the obvious one.
The other is an air gapped installation that mints its own licenses against its
own signing key, which is a supported arrangement rather than a workaround, and
the steps are identical.

## The tool, and why nothing calls it

Issuing is `tools/licensegen`, a command line program with no caller. That is
deliberate. Issuing is a vendor action taken a handful of times a year by a
person holding a signing key, and wrapping it in a workflow would mean putting
the signing key somewhere a workflow can reach. A key a pipeline can read is a
key that leaks with the pipeline.

What is not deliberate is that until this page existed, running it was tribal
knowledge. A command run by hand is a legitimate design. A command nobody can
find is not.

## Before anything: three things that are not true yet

Read these first. Each one changes what issuing means today.

**No released binary carries a signing key.** `ee/engine/license/keys.go`
expects a release to stamp public keys into `trustedKeys` with a linker flag.
Nothing does. `tools/release/build.sh` builds the community engine and stamps
the version, the commit and the build date, and it does not build the
enterprise binary at all. So a key signed today verifies only where
`AF_LICENSE_PUBLIC_KEYS` supplies the public half, which is the air gapped
arrangement. Issuing to a customer running a downloaded binary is not possible
until an enterprise release exists.

**Nothing records what was issued.** No ledger, no database row, no file. The
only record of a license is the customer's copy and whatever the person who
signed it wrote down. Every reissue and every support question depends on that.

**There is no revocation list.** `Verifier.Revoke` exists, has no caller
outside its own test, and nothing loads a list of withdrawn identifiers. See
[withdrawing a license](#withdrawing-a-license) for what is actually available.

## Step one: the signing key

Once per key, not once per license.

```sh
go run ./tools/licensegen keygen -id 2026-09
```

It prints a key id, a public key and a private key, and writes nothing to disk.
Paste the private half into the key vault immediately and nowhere else. The
program has no way to recover it.

The key id is a label you choose. Date it, because the only thing it has to do
is let somebody a year from now tell one rotation from another.

The public half goes to the verifier as `kid=base64`, and the key id in that
pair has to be the one you just chose. Keep every previous entry: a build that
trusts only the newest key cannot verify a license already in the field.

```sh
export AF_LICENSE_PUBLIC_KEYS=2026-09=gxvgko3UB27tcxm07XOfJrEDRcAbLzmdtbnCOjEp9yw
```

Standard and URL safe base64 are both accepted, so a key pasted out of whatever
tool produced it works either way.

## Step two: the request

A JSON file describing what was bought.

```json
{
  "org": "acme",
  "plan": "enterprise",
  "features": ["sso", "scim"],
  "seats": 25,
  "months": 12
}
```

| Field | Meaning |
| --- | --- |
| `org` | The organization slug. Required, and inside the signature, so a key cannot be edited to name a different one. It must equal the customer's `AF_ORG` exactly, ignoring case and surrounding space. |
| `plan` | Display only. Nothing branches on it. |
| `features` | What the license permits, from the closed set below. Refused if it names anything else. |
| `seats` | The member limit. Required, and **zero means unlimited**, so it has to be written rather than omitted. |
| `months` | How long from the issue time. Defaults to 12. |
| `grace_days` | How long after expiry features keep working. Defaults to 14. |
| `trial` | Marks an evaluation license, which shows a banner. |

The features are `air_gapped`, `audit_stream`, `billing`, `compliance_packs`,
`enterprise_dashboard`, `enterprise_secrets`, `multi_runtime`,
`policy_enforcement`, `rbac`, `scim`, `sso` and `support_access`. Anything else
is refused at issue time, because the verifier cannot refuse it: a license
issued for a newer release names features an older binary has never heard of,
and rejecting the whole license over one unknown name would take away the
features the customer did buy. So the verifier carries an unknown name without
acting on it, and the generator is the only place the set can be closed.

Before that check existed, `"features": ["ssoo"]` signed cleanly, verified
cleanly, reported the license active, and permitted nothing.

## Step three: sign it

The private key arrives in the environment from the vault, for the length of
one command, and is never read from a file in this repository.

```sh
AF_LICENSE_SIGNING_KEY=$(vault-read antifailure/license/2026-09) \
  go run ./tools/licensegen issue \
    -request ./acme.json \
    -key-id 2026-09 \
    -id lic-0001
```

`-id` is the license identifier. Nothing generates it and nothing checks it is
unique, so pick a scheme and keep to it.

The key goes to standard output on one line. A receipt goes to standard error:

```
signed lic-0001 for acme: 25 seats, 12 months, features sso, scim
key id 2026-09 must name this public key in the verifier: gxvgko3UB27tcxm07XOfJrEDRcAbLzmdtbnCOjEp9yw
```

**Check the second line before you send anything.** `-key-id` is a label this
program cannot verify against the key it signed with. Sign with one key and
label it as another and the customer's engine looks the label up, finds a
different public key, and reports the license as tampered with. The licensing
page tells them that almost always means the token was truncated in transit, so
a typo here sends everybody hunting for a paste error that never happened.
Comparing that line against the entry in the verifier's key list is the only
thing that catches it.

## Step four: what the customer does

Two environment variables, and nothing is stored.

```sh
export AF_LICENSE_KEY=aflic_eyJleHBpcmVzX2F0IjoiMjAyNy0wOS0wMl...
export AF_ORG=acme
af license status
```

```
  This is the enterprise edition, licensed to acme.

  Licensed to acme on the enterprise plan
  Expires 2 September 2027

  Features:
    scim
    sso
```

Ask them to send that output back. It is the only confirmation available that
the key they received is the key you signed, and it is cheaper than every
alternative.

`af license inspect` does not exist. To read a key during a support call, use
the generator, which decodes without verifying and says so:

```sh
go run ./tools/licensegen inspect -token "$AF_LICENSE_KEY"
```

## Step five: reissuing

There is no renewal. Sign a new key from a new request and send it, and the
customer replaces the variable. The old key stays valid until its own expiry,
which is why a shortened reissue does not shorten anything.

An expired license does not stop the engine. It enters the grace period with a
warning on every command, then falls back to the community behaviour with every
enterprise setting preserved. A renewal restores them unchanged, so a late
purchase order costs warnings rather than an outage.

## Withdrawing a license

Say plainly what is available, because the obvious answer is not.

The verifier has a `Revoke` method and a revoked state. Nothing calls it and
nothing loads a list of withdrawn identifiers, so a revoked state cannot be
reached by any shipped binary. Marking a license as revoked is not something
you can currently do.

What is available:

1. **Let it expire.** The reason grace periods and short terms exist. A twelve
   month license issued to somebody who should not have it is a twelve month
   problem, so keep terms short where the relationship is uncertain.
2. **Rotate the signing key.** Removing a public key from the verifier's list
   invalidates every license signed with it, not one. That is the blunt
   instrument, and it means reissuing to every other customer on that key first.
3. **Ask.** For a self hosted installation the key sits in the customer's
   environment and only they can remove it. Offline verification means there is
   no other lever, and that is the price of a license that keeps working when
   the network does not.

Choosing between these is a decision, not a procedure. Making it once and
writing it down is worth more than any of the three.

## What goes wrong, and what the customer sees

| They see | Cause |
| --- | --- |
| `no licence signing keys, so no licence can be verified` | The binary carries no stamped keys, which is every build today. Set `AF_LICENSE_PUBLIC_KEYS`. |
| The license key's signature does not verify | Usually a truncated paste. Otherwise `-key-id` named a key that is not the one that signed. |
| Signed by a key this build does not know | The key id is not in the verifier's list, or a rotation dropped it. |
| Issued to one organization, installed at another | `AF_ORG` does not match the request's `org`. |
| Active, and no features | The request named features that were signed and are not permitted, or named none at all. |
| Seats all in use with room to spare | `seats` was omitted before it was required, or set from the wrong line of the order. |

Related: [licensing](/docs/enterprise/licensing),
[compliance](/docs/enterprise/compliance).
