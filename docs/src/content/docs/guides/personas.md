---
title: Personas
description: The users an agent signs in as, and how they come to exist.
sidebar:
  order: 8
---

A persona is a user of your application. Agents sign in as one, and different
roles see different things, which is the point.

```yaml
personas:
  - name: owner
    email: owner@example.test
    role: admin
    login: password

  - name: member
    email: member@example.test
    role: member
    login: magic_link

  - name: locked-out
    email: locked@example.test
    role: member
    login: totp
    mfa: true
    attributes:
      plan: free
      onboarded: "false"
```

`example.test` is a reserved domain that can never receive mail, so a persona
address is safe by construction even before capture mode is considered.

## Login strategies

| Strategy | How the agent signs in |
| --- | --- |
| `password` | Types a password. |
| `magic_link` | Waits for the mail, opens the link. |
| `email_code` | Waits for the mail, reads the code. |
| `sms_code` | Reads the code from the captured SMS. |
| `totp` | Generates the code from the seed. |

Everything except `password` depends on capture mode: a magic link that was
really emailed is a link the agent cannot read, and a real address receiving it
is a person getting mail from a pull request.

## Where personas come from

They are created in the branch before the agents run, as rows, using the same
tables your application uses. That works because a branch is a real database.

```
AF-DB-020 Personas cannot be provisioned because Supabase creates users only
through its own API.
```

Some providers own their user table and will not accept a row written directly.
Where that is true, the persona has to be created through the provider's API or
by your own seed step, and the manifest's `seed` is where that goes.

## `attributes`

Anything your application reads to decide what a user sees: plan, feature
flags, onboarding state. They become whatever your schema stores them in.

Use them to reach states that are otherwise hard to arrange. A persona that has
never onboarded is one line here and twenty minutes of clicking otherwise.

Related: [workflows](/guides/workflows/), [the inbox](/guides/inbox/),
[agents](/concepts/agents/).
