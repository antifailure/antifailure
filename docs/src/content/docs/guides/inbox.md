---
title: The inbox
description: Mail an environment sends goes here, so a flow finishes and no real address receives anything.
sidebar:
  order: 4
---

A host in `capture` mode is answered locally. The provider's API returns what it
would have returned, the application carries on believing the message was sent,
and the message lands in the inbox.

```yaml
egress:
  rules:
    - host: api.resend.com
      mode: capture
      note: "mail goes to the inbox; no real address receives anything"
```

That is what makes a sign-up flow finish. A welcome email that never arrives
means an agent waiting for a confirmation link waits forever, and a real email
means somebody's actual address received mail from a pull request.

## Reading it

```sh
af inbox list
af inbox get <id>
af inbox wait --to owner@example.test --subject "Verify"
```

`af inbox wait` is the one used in a workflow. It checks what has already
arrived before it starts waiting, which matters more than it sounds: the message
is usually sent before anything starts waiting for it, and a wait that only
looks forward is how a test passes on a slow machine and fails on a fast one.

```sh
af inbox wait --to owner@example.test --subject Verify --timeout 90s
```

It prints the message, so a script can pull a code or a link out of it with
whatever it already uses for that.

## Nothing arrived

```
AF-NET-011 No message matching to=owner@example.test within 60s.
  Next: Check the egress rules; a host in block mode sends nothing, and a host
  with no rule at all is blocked by default.
```

In order of how often it is the cause:

1. The provider host has no rule, so it is blocked and nothing was sent. `af
   net log` shows the refused request.
2. The rule is `block` rather than `capture`.
3. The application sent to a different address than the one being waited for.
   `af inbox list` shows everything captured, which settles it immediately.
4. The send genuinely failed inside the application. `af logs <service>`.

## What is captured

SMTP and the HTTP APIs of the common providers. A provider whose API is not
recognised still gets captured if its host is in capture mode: the request is
recorded and answered with a plausible success, and `af inbox get` shows the
raw body. Less convenient than a parsed message and better than a flow that
cannot finish.

Related: [egress](/docs/concepts/egress), [personas](/docs/guides/personas),
[workflows](/docs/guides/workflows).
