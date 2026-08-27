---
title: SCIM provisioning
description: Users and groups managed by your identity provider, with deprovisioning that actually removes access.
sidebar:
  order: 3
---

Your identity provider creates, updates and removes members here, so that
somebody who leaves loses access without anybody remembering to do it.

SCIM 2.0, Users and Groups. This is an enterprise feature; it lives in
`ee/web/scim`, under the Antifailure Enterprise License, and the community build
does not contain it.

## Connecting

Your provider needs two things:

```
Base URL   https://<your-control-plane>/scim/v2
Token      a bearer token issued per organisation
```

Create the token in the control plane. Only its hash is stored, so it is shown
once. Tokens can be given an expiry and rotated: two are live during the
overlap, because a cutover means provisioning is broken for however long it
takes somebody to paste the new value into the identity provider.

What is supported is published where a client will look for it, and it is
written from what the code does rather than from what would be nice:

```sh
curl -H "Authorization: Bearer <token>" \
  https://<your-control-plane>/scim/v2/ServiceProviderConfig
```

`patch`, `filter` and `etag` are supported. `bulk`, `sort` and `changePassword`
are not, and say so. A configuration document claiming a capability the server
lacks makes a client use the path that fails instead of the one that works.

## What each operation does here

| SCIM | Effect |
| --- | --- |
| Create a user | An account and a membership, with the default role. They can sign in immediately. |
| `active: false` | **The membership is deleted and every live session is revoked**, in the same transaction. |
| `active: true` | The membership is restored with the default role. |
| Delete a user | The same as deactivation, and the SCIM resource goes too. The account row stays. |
| Create a group | A group. Members are recorded whether or not those users exist here yet. |
| Add a group member | Recorded. If the user does not exist yet, the reference is kept and resolved when they arrive. |

### Deactivation removes the membership

This is deliberate and it has a cost worth knowing about.

`active: false` from a directory means this person no longer works here, and the
only honest implementation of that is that the row granting access stops
existing. A flag that every read path has to remember to check is the shape of
bug where the button says deactivated, the flag is set, and one query that
forgot the check still returns their data.

The cost: a role you set by hand here is not remembered across a deactivate and
reactivate cycle. Somebody promoted to admin and then deactivated comes back as
a member. That is the right trade against a departed employee keeping access,
and mapping the role from a group avoids it entirely.

Sessions are revoked in the same transaction as the membership, not by a job
that runs later. Deprovisioning that took effect at the end of somebody's
current session would mean a person removed at nine still reading data at five.

### Group membership can arrive before the user

Okta and Entra ID both send group membership naming users they have not created
yet. An implementation that resolves the reference at write time either drops
the member silently or rejects the request, and both leave the group
permanently missing somebody while every response was a 200.

Here the reference is stored as it arrived and resolved when the user is
created. Until then the group reports the member using the provider's own
identifier, because reporting a smaller group than the provider believes is how
a reconciliation job decides to add everybody again.

## PATCH, and why your provider's shape works

RFC 7644 describes one operation shape. Providers send at least five. All of
these are handled, and every one of them is a real message:

```jsonc
// Okta deactivating somebody: no path, attributes inside the value
{"op": "replace", "value": {"active": false}}

// Entra ID: capitalised op, and the boolean sent as a string
{"op": "Replace", "path": "active", "value": "False"}

// Entra ID again: the value wrapped in the multi-valued shape
{"op": "Replace", "path": "active", "value": [{"value": "False"}]}

// Removing one group member, with a filter inside the path
{"op": "remove", "path": "members[value eq \"<id>\"]"}
```

The string `"False"` is the one that quietly does nothing elsewhere: it is
truthy, so an implementation writing `Boolean(value)` deactivates nobody while
answering 200 to everything.

An operation this server does not understand is a **400 with a `scimType`**, not
a skip. A skipped operation returns 200 and your provider records the change as
applied; the first time anybody notices is when a departed employee still has
access. Profile attributes this schema does not keep (`title`, `department`,
`locale` and similar) are accepted and ignored on purpose, and are listed by
name in the code so that "ignored deliberately" and "not understood" stay
distinguishable.

## Filters

```
GET /scim/v2/Users?filter=userName eq "ada@example.com"
```

Filterable: `id`, `userName`, `externalId`, `active`, `displayName`,
`emails.value`, `name.givenName`, `name.familyName`. Groups: `id`,
`displayName`, `externalId`.

A filter this server cannot answer is refused with `invalidFilter`. That
matters more than it sounds: a provider asking "who has this userName" and
receiving every user will create a duplicate of everybody, so silently ignoring
a filter is worse than refusing it.

The filter is parsed into a syntax tree and never concatenated into SQL. Every
attribute maps to a known column through a closed list, and every literal is a
bound parameter, including the wildcards in `co`, `sw` and `ew`, which are
escaped so a value cannot smuggle one.

## Errors

| Status | When |
| --- | --- |
| 400 | A filter, a patch, or a body this server cannot act on. Carries a `scimType`. |
| 401 | No token, or a token that is revoked, expired or never existed. All four answer the same. |
| 404 | No such resource. **A delete for an unknown user is a 404 and is fine**, because deprovisioning arrives twice more than anything else. |
| 409 | `uniqueness`: that `userName` is taken. |
| 412 | A stale `If-Match`. Fetch the resource again and retry. |
| 429 | Rate limited. Bursts of 200 are absorbed; a first directory sync will not trip it. |
| 500 | Ours, and the only case a client should retry. Logged here with the underlying cause. |

## What is audited

Every write: `scim.user.created`, `scim.user.activated`,
`scim.user.deactivated`, `scim.user.updated`, `scim.user.deleted`,
`scim.group.created`, `scim.group.replaced`, `scim.group.deleted`. The audit log
is append-only and hash chained, and the application role holds `INSERT` and
`SELECT` on it and nothing else, so those entries cannot be rewritten by the
thing being audited.

## What is not here yet

- **`sort`** and **bulk operations**. Both are declared unsupported.
- **A reconciliation report** showing drift between your directory and this
  organisation. The data is all present; the report is not written.
- **Group-to-role mapping through SCIM.** Groups sync, and a group can carry a
  role, but the mapping is configured through the single sign-on connection
  rather than through SCIM.

Each is absent rather than half-present, and none is claimed anywhere in the
product.
