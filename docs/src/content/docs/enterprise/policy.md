---
title: Policy enforcement
description: Organisation rules that decide whether an environment may exist.
sidebar:
  order: 2
---

*Requires an enterprise license with the `policy_enforcement` feature.*

A policy is an organisation rule checked before an environment is created. It
can refuse.

```
AF-EE-010 Organization policy no-unmasked-goldens refuses this environment:
the golden gv_20260826120000_a1b2c3d4 was published without a verification
attestation.
  Next: Ask an organization administrator to review no-unmasked-goldens, or
  bring the repository into compliance.
```

## Writing the policy down

A policy is a YAML document, and the engine reads it from the path in
`AF_ORG_POLICY_FILE`:

```sh
export AF_ORG_POLICY_FILE=/etc/antifailure/policy.yaml
```

```yaml
# Every key is a restriction. There is no key that grants anything.
required_masked_columns:
  - "*.email"
  - "customers.card_number"
denied_hosts:
  - api.stripe.com
allowed_modes:
  - block
  - capture
  - mock
synth_requires_approval: true
allowed_providers:
  - neon
allowed_regions:
  - westeurope
```

`required_masked_columns` is `table.column` with `*` allowed in either part.
Write the schema too, as `public.users.email`, when you mean one schema in
particular; a pattern without one names that table in whichever schema holds
it.

The rule is checked against the database's own catalogue, so it means every
column it names. `"*.email"` is satisfied when every email column in the
database is masked, and a plan that masks `users.email` and leaves
`contacts.email` readable is refused by name. A pattern that matches no column
at all counts as unsatisfied too.

`denied_hosts` refuses a host named in any mode other than `block`. A repository
may still write a `block` rule for one, so that it can document what it
deliberately refuses.

The engine prints which rules are in force at startup, on standard error:

```
af: organization policy: egress deny list (1 hosts)
af: organization policy: required masking (*.email, customers.card_number)
```

A file you named that cannot be read, cannot be parsed, or carries a key this
build does not know stops the engine with the reason.

Setting nothing registers nothing and prints nothing, which is the ordinary
case for an installation with no organization policy.

Approvals live in the control plane and this file does not carry them, so
`synth_requires_approval` refuses every synth rule when the engine reads its
policy from a file.

## Where it runs

Most of the policy is checked before anything is created, not after.

`required_masked_columns` is the exception: it is checked during a golden refresh
instead, after the engine has read the database's catalogue and worked out which
columns its rules will rewrite, and before the first row is rewritten. A refusal
there means the golden is never published, and an unverified golden cannot be
branched, so no environment can hold data the policy refused.

One consequence to plan for: a golden published before you tightened the policy
is not re-examined. Refresh the golden after a policy change, with
`af golden refresh`, and the new rule decides whether it may be published.

The extension points it uses are in the community edition, in
`engine/pkg/extension`. That is deliberate: the sockets are MIT so that anybody
can write a hook, and the enterprise edition supplies one implementation of
them.

## Hooks can only refuse

A hook returns a refusal or nothing. It cannot permit something the engine would
otherwise refuse.

## Writing one

```go
type PolicyHook interface {
    // Returns an error to refuse. Nil permits nothing; it declines to object.
    Check(ctx context.Context, req EnvironmentRequest) error
}

type MaskingHook interface {
    // Asked during a golden refresh, with the columns a plan will rewrite and
    // the whole catalogue it read them from.
    CheckMasking(ctx context.Context, req MaskingRequest) error
}
```

A hook may implement either or both. `MaskingRequest` carries two column lists
and a hook needs both.

Register it with the engine's extension registry. The community build registers
nothing, so each check iterates an empty slice and returns nil.

Related: [licensing](/docs/enterprise/licensing), [egress](/docs/concepts/egress).
