---
title: Custom roles
description: A role your organisation defines, granted to a member at one repository or group, on top of the four built-in roles.
sidebar:
  order: 11
---

The four built-in roles, owner, admin, member and viewer, are the right four for
a team and they are in every edition. A large organisation is shaped
differently: somebody administers two repositories and reads the rest, a
compliance team approves masking changes and creates no environments, a
contractor sees one repository and nothing about the others.

A custom role is a name, a description and a set of permissions from the same
fixed catalogue every route already declares. A grant gives one person one role
at one scope: the whole organisation, a named group of repositories, one
repository, or one environment.

This is an enterprise feature. It lives in `ee/web/rbac`, under the Antifailure
Enterprise License, and the community build has the four built-in roles and
nothing that stores or reads a custom one.

## Two rules that make a model predictable

**A narrower scope grants, it never revokes.** A grant at a repository adds to
what the organisation level already gave. It cannot take something away. The
other reading looks tidy and is unusable: an administrator adds a role to give
somebody access to one repository and silently removes their access to every
other, and nobody can say what anyone can do without evaluating every rule in
order.

**A custom role cannot narrow a built-in one.** Every permission a built-in role
holds stays held. A custom role is asked only where the built-in role has
already refused, so the worst a wrong model can do is grant too little.

## The model is a file

There is no route that adds one role or one grant, on purpose. A permission
model edited one click at a time is a model nobody reviews. It is exported as
YAML, reviewed as a pull request the way every other change is, and applied
whole after a dry run.

```yaml
version: 1
roles:
  - id: deployer
    name: Deployer
    description: Brings environments up for the payments repositories.
    permissions:
      - environments.view
      - environments.create
      - environments.teardown
groups:
  - name: payments
    repositories:
      - acme/billing
      - acme/invoices
grants:
  - userId: 4f1c8e02-6b1a-4c77-9a3e-0d51d1a2b3c4
    roleId: deployer
    scope:
      kind: group
      name: payments
approvals: []
```

A description is required. A role called `ops` with no description is a role
nobody can review, and reviewing it is the point of writing it down. A
permission that is not in the catalogue is refused rather than ignored, because a
typo that grants nothing looks exactly like a grant.

The catalogue is the one every route already declares, and every permission in it
carries the sentence a security team reads. `GET /roles/members/<id>/permissions`
below answers what one person holds and where each permission came from.

## The routes

All four need a signed-in session, the CSRF header every mutation needs, and
`members.manage` in your **built-in** role. That last part is deliberate: a
custom role granting `members.manage` does not open the model to its holder, or
one grant would be every grant.

| Request | What it does |
| --- | --- |
| `GET /roles/policy` | The current model, as YAML. |
| `POST /roles/policy/dry-run` | What applying a file would change, and anything that would stop it. |
| `PUT /roles/policy` | Applies a file, whole, in one transaction. |
| `GET /roles/members/<id>/permissions` | What one person can do and where each permission came from. You may always read your own. |

A dry run is worth taking. The person applying a permission model is usually the
person a wrong one would lock out.

## You cannot grant what you do not hold

A file is refused if it would give anybody a permission your own built-in role
does not have. An admin holds `members.manage` and deliberately holds neither
`billing.manage` nor `organization.delete`, so an admin cannot define a role
holding those, and cannot grant a role an owner defined that holds them. Without
that rule the permission to edit the model would quietly be every permission
there is.

The rule applies to what changes. An owner may define a role an admin could not,
and the admin can go on editing the rest of the file without being refused for
it.

## What is not here

`approvals` is part of the file format and nothing enforces it yet, so a file
that carries a non-empty `approvals` section is refused whole, naming it. A
stored approval requirement that nothing checks would be a control reporting
itself as held, which is worse than not having one.

## What happens without the entitlement

Custom roles are refused per organisation and per installation, and the two are
different answers:

- The installation's licence does not permit `rbac`: every route above answers
  402 naming the feature and the state of the licence.
- The organisation is not entitled on its plan: every route answers 403 with the
  sentence that says so, and a stored grant widens nothing.

Neither removes anything. Built-in roles keep what they had, the stored model is
left alone, and restoring the entitlement restores the grants exactly as they
were.

Related: [licensing](/docs/enterprise/licensing), [single sign-on](/docs/enterprise/sso),
[SCIM provisioning](/docs/enterprise/scim).
