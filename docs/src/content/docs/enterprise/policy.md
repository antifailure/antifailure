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
at all counts as unsatisfied too, because a policy that quietly passes when the
thing it protects is absent stops protecting the moment somebody renames a
table.

`denied_hosts` refuses a host named in any mode other than `block`. A repository
may still write a `block` rule for one, so that it can document what it
deliberately refuses.

The engine prints which rules are in force at startup, on standard error:

```
af: organization policy: egress deny list (1 hosts)
af: organization policy: required masking (*.email, customers.card_number)
```

A file you named that cannot be read, cannot be parsed, or carries a key this
build does not know stops the engine with the reason. That is deliberate:
starting anyway means every environment is created without being checked and
nothing in the output says so, which is exactly the behaviour the policy exists
to change.

Setting nothing registers nothing and prints nothing, which is the ordinary
case for an installation with no organization policy.

Approvals live in the control plane and this file does not carry them, so
`synth_requires_approval` refuses every synth rule when the engine reads its
policy from a file. A lookup that answered "approved" because it had nowhere to
ask would turn the rule into decoration.

## Where it runs

Most of the policy is checked before anything is created, not after. A policy
that refused an environment halfway through would leave resources behind and a
decision nobody can act on.

`required_masked_columns` is the exception, and the reason is worth knowing
before you write one. At creation time the engine has read a manifest, and a
manifest enumerates services rather than columns. Expanding a column pattern
there would mean expanding it against the tables a manifest happens to mention,
which is not the set of tables that exist, and a required pattern that matched
nothing in that smaller set would refuse a repository whose schema satisfies it
perfectly.

So the masking rule is checked during a golden refresh instead, after the
engine has read the database's catalogue and worked out which columns its rules
will rewrite, and before the first row is rewritten. A refusal there means the
golden is never published, and an unverified golden cannot be branched, so no
environment can hold data the policy refused. It is later than the other rules
and it is still before the data exists.

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

That asymmetry is the whole safety property. A hook that could grant permission
would be a way to switch off masking verification, egress policy, or tenant
isolation from outside the engine, and none of those should have an off switch
that lives in somebody's plugin.

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
and a hook needs both: masked columns alone cannot tell a database that has no
email column from one that has three and masks none of them, and those deserve
opposite answers.

Register it with the engine's extension registry. The community build registers
nothing, so each check iterates an empty slice and returns nil.

Related: [licensing](/docs/enterprise/licensing), [egress](/docs/concepts/egress).
