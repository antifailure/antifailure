---
title: Licensing
description: What is MIT, what is not, and how a license is verified.
sidebar:
  order: 1
---

Everything in this repository is MIT licensed except the `ee/` directory, which
is under the Antifailure Enterprise License. The community build does not
contain `ee/` at all: it is a separate Go module the community build cannot
resolve, and CI has a job that fails if the community binary carries an
enterprise symbol.

That is stronger than a runtime check. A feature you cannot compile is a feature
that cannot be switched on by patching a boolean.

## Installing a license

```sh
af license install <token>
af license status
af license remove
```

A license is an Ed25519 signed statement carrying the organisation it was issued
to, the features it permits, the seat count, when it expires, and which key
signed it. Verification is a signature check against a key compiled into the
binary; it needs no network, which is what makes an air gapped installation
possible.

## When it does not verify

```
AF-EE-001 The enterprise license could not be verified.
  Next: Reinstall the license with 'af license install'; the token may have been
  truncated in transit.
```

Almost always truncation. A license token is long and survives being pasted into
a chat window less often than people expect.

## Wrong organisation

```
AF-EE-003 This license was issued for organization acme and this instance is
globex.
  Next: Install the license issued for globex.
```

The organisation is inside the signature, so a license cannot be edited to name
a different one. This is what stops a key being passed around.

## Clock

```
AF-EE-002 The system clock is 3 days behind the last time this license was
seen.
  Next: Correct the system clock. Enterprise features resume once it passes the
  recorded time.
```

Expiry is checked against the clock, and a clock that can be moved backwards is
an expiry that can be avoided. The last seen time is recorded, so going
backwards is detected rather than believed. Correcting the clock resolves it;
nothing has to be reinstalled.

## Seats

```
AF-EE-004 The license covers 25 seats and they are all in use.
  Next: Remove an inactive member, or contact licensing@antifailure.dev to add
  seats. No existing member was removed.
```

The last sentence is the important one. Reaching a seat limit refuses the
addition and never evicts somebody to make room.

## Expiry and grace

An expired license keeps working for a grace period, with a warning on every
command. Enterprise features that stop working the moment a renewal is late
turn a billing delay into an outage, and nothing in `ee/` is worth doing that
for.

After the grace period the enterprise features stop and everything else carries
on. The community edition is the whole product minus `ee/`, and an expired
license leaves you with it rather than with nothing.

## What is in `ee/`

`sso`, `scim`, `rbac`, `audit_stream`, `policy_enforcement`, `multi_runtime`,
`enterprise_secrets`, `billing`, `enterprise_dashboard`, `support_access`,
`compliance_packs`, `air_gapped`.

Each is named in the license, so a license permits exactly what was bought.

## Contributing

Contributions are under the DCO, not a CLA. You keep your copyright. See
`CONTRIBUTING.md`.

Related: [policy](/docs/enterprise/policy), [runtimes](/docs/enterprise/runtimes).
