---
title: Personas
description: The users an agent signs in as, and how they come to exist.
sidebar:
  order: 9
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

  - name: secured
    email: secured@example.test
    role: member
    login: totp
    mfa: true
    attributes:
      plan: free
      onboarded: "false"
```

`example.test` is a reserved domain that can never receive mail, so a persona
address is safe by construction even before capture mode is considered. A
persona that signs in by SMS gets a number from the `+1 555 0100` block, which
is reserved for fictional use, for the same reason.

## Login strategies

| Strategy | How the agent signs in |
| --- | --- |
| `none` | Does not sign in at all, for an application with no sign in or a page that is public. |
| `password` | Types the password. |
| `magic_link` | Waits for the mail, opens the link. |
| `email_code` | Waits for the mail, reads the code. |
| `sms_code` | Reads the code from the captured text. |
| `totp` | Types the password, then generates the code from the enrolled secret. |
| `session` | Does not sign in through a form. |

Everything except `none` and `password` depends on capture mode: a magic link
that was really emailed is a link the agent cannot read, and a real address
receiving it is a person getting mail from a pull request.

A persona set to `none` has no account, so nothing is created for it and your
application does not need anywhere to put one. That is the shape an API with no
sign in has, and until it was actually run, such a manifest was refused with
"no users table could be found" for an account that was never going to be used.
A workflow still has to name a persona, because a workflow runs as somebody
even when that somebody is a visitor who never signed in.

## Where personas come from

They are created before the agents run, by an authentication adapter, and the
adapter is chosen for you. A repository that depends on `@supabase/supabase-js`
gets the Supabase adapter written into its manifest by `af init`; at run time
the engine looks at the branch's actual schema, and what it finds there wins,
because a table is a fact and a dependency list is an intention.

| Adapter | Where the persona is created | Chosen when |
| --- | --- | --- |
| `direct` | Your own users table | You own authentication |
| `supabase` | Supabase's `auth` schema, in the branch | `@supabase/supabase-js` and friends |
| `supabase_api` | A Supabase project's auth admin API | You set it, and give a project URL |
| `nextauth` | The NextAuth and Auth.js tables | `next-auth`, `@auth/core` |
| `clerk` | Clerk, through its backend API | `@clerk/nextjs` and friends |
| `auth0` | Auth0, through the Management API | `auth0`, `@auth0/nextjs-auth0` |
| `workos` | WorkOS User Management | `@workos-inc/node` |
| `seed` | A command you name | Nothing else fits |

Provisioning is idempotent and reconciles rather than duplicates. That matters
because a golden is a masked copy of production, so a persona's address may
already be there as a real user who has since been masked. Two rows with one
email is a broken fixture that looks exactly like a broken application, and
takes a day to find. Running twice is safe, which is also what lets a persona
be created once in the golden and reconciled again on every branch.

Passwords and TOTP secrets are derived from the environment and the persona
name, never stored and never transmitted. The adapter that writes the hash and
the runner that types the password compute the same value independently. Two
branches of the same repository therefore have different passwords for the same
persona, and neither is a secret that outlives its branch.

## Sessions do not survive

Masking rewrites a customer's name and address. It does not touch the row in
`auth.sessions` that still authenticates as them, because a session token is
not personal data by any rule a scanner applies. A branch published with those
rows intact hands anybody who can reach it a working login belonging to a real
person.

So provisioning empties the session and token tables of whichever scheme is in
use, every time. If your application keeps its own alongside the framework's,
name them:

```yaml
auth:
  sessions: [app_sessions, api_tokens]
```

## Hosted providers

Clerk, Auth0 and WorkOS will not accept a row written into a table, because the
table is not in your database. Personas are created through their APIs instead,
and they need somewhere that is not production to create them:

```yaml
auth:
  adapter: clerk
  token_env: CLERK_SECRET_KEY
  sandbox: true
```

`token_env` is the name of the variable holding the admin key, never the key
itself. `sandbox: true` says that the tenant the key belongs to is a
development instance, a sandbox or a staging environment. Without it,
provisioning refuses:

```
AF-DB-020 Personas cannot be provisioned because clerk creates users only
through its own API, and no sandbox tenant is configured.
```

That refusal is deliberate and `af init` will never set `sandbox` for you. The
only tenant left to fall back to is the production one, and a persona created
there is a real user of your real product with a password Antifailure
generated. It is the one setting that has to come from a person.

## An application that owns its users

With no `auth` block the engine looks for a users table and reads its columns.
Where the names are not ones it would guess, say them:

```yaml
auth:
  adapter: direct
  table:
    name: accounts
    id: account_id
    email: email_address
    password: password_digest
    role: kind
    attributes:
      plan: subscription_tier
    timestamps: [created_at, updated_at]
```

Passwords are hashed with bcrypt at cost 10, which is what most frameworks
write. If your application's rules are stricter than the generator, say so, and
the generated password is shaped to fit rather than being refused at sign in:

```yaml
auth:
  password:
    min_length: 16
    forbid: "!"
```

## `seed`

For anything else. The command runs once per persona, against the branch, with
the persona in its environment:

```yaml
auth:
  adapter: seed
  seed: npm run seed:persona
```

| Variable | What it holds |
| --- | --- |
| `AF_PERSONA_NAME` | The persona's name |
| `AF_PERSONA_EMAIL` | Its address |
| `AF_PERSONA_PHONE` | Its number, for `sms_code` |
| `AF_PERSONA_ROLE` | Its role |
| `AF_PERSONA_LOGIN` | Its login strategy |
| `AF_PERSONA_PASSWORD` | The password it must end up with |
| `AF_PERSONA_TOTP_SECRET` | The base32 secret to enrol, when `mfa` is set |
| `AF_PERSONA_MFA` | `1` when a second factor is wanted |
| `AF_PERSONA_ATTRIBUTES` | The attributes, as a JSON object |
| `AF_DATABASE_URL` | The branch to write to, also as `DATABASE_URL` |

Two rules. It must be idempotent, because it runs again on every branch. And it
must exit non zero if it did not create the account, because an exit code of
zero is read as "the persona exists", and an agent told about an account that
was never created reports the application refusing a correct password.

It can print the account's identifier on its last line, and that is recorded.
Anything else it prints is ignored unless it fails, in which case its output is
what explains why.

## `attributes`

Anything your application reads to decide what a user sees: plan, feature
flags, onboarding state. An attribute with a column of its own goes there; the
rest go to the scheme's JSON column, which for Supabase is
`raw_user_meta_data`. An attribute with nowhere to go is an error rather than a
silent omission, because a persona quietly in the wrong state fails a workflow
for a reason nobody can see.

Use them to reach states that are otherwise hard to arrange. A persona that has
never onboarded is one line here and twenty minutes of clicking otherwise.

Related: [workflows](/docs/guides/workflows), [the inbox](/docs/guides/inbox),
[agents](/docs/concepts/agents).
