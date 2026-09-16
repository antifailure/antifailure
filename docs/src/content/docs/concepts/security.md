---
title: Security checks
description: How Antifailure routes security check families at exactly what a change touched, and the boundary every finding respects.
sidebar:
  order: 18
---

A security check is an ordinary finding in a new namespace. It rehearses the
change against the sanitized twin the rest of the product already builds, at
exactly the routes, screens and boundaries the diff touched, and folds what it
finds into the same verdict, exit code and pull request comment every other
check uses. There is no second pipeline and no second report.

## What a finding carries, and what it never does

A security finding is a `report.Finding`: a rule, a level, a one line title, a
bounded description, a fix, and a location. The rule is the stable name you
grep for and the manifest key that decides what the finding does, both at once,
so `security.authz.idor` is what a report shows, what the manifest configures,
and what a coding agent reads back.

It never carries the value that proved it. The offending request body, the
leaked row, the response and the screenshot stay inside the copy of production
the run drove; the finding reports the location and a bounded, neutralized
description and nothing else. That boundary is the product: these findings come
from real data, so the one place a value must not travel is out of the run.

## Routing, so a check runs where the change is

A docs only or test only change routes no security family, exactly as it routes
no workflow today, which is what keeps the check fast. A change to a route runs
the families that read a route; a change to a guard, a policy or a migration
runs the families that read who may do what. The router names the units it
routed, each carrying the facts that produced it, so the reasoning is auditable
rather than a black box.

A change to who may do what is its own surface, `auth`, and it is deliberately
broad: authentication and authorization middleware, route guards, the
organisation policy package, the entitlement catalogue, licence gating and the
extension request shape all route there. A control is as often evaded by an
absent rule as by a wrong one, so a change anywhere near the security edge is
treated as a security change rather than as ordinary code.

## Configuring what a finding does

Every security key is a `security.<family>.<rule>` entry in the manifest's
`policy` block, and it takes the same three levels every other policy key does. Each
key becomes available when its family lands, so once the authz family ships you
set `security.authz.idor` to `fail`, `warn` or `ignore` in `policy` just as you
set any other key.

A finding at `fail` stops the merge, one at `warn` is reported and the check
still passes, and one at `ignore` is dropped. A level the manifest does not
recognise is refused rather than quietly coerced, the same way every other
policy value is, so a manifest that says `block` is told `block` is not a level
rather than silently warning.

## Exit codes

A security finding that is the worst failure decides the process exit, so a
script reading only the exit knows which kind of problem it hit. A family that
proved the running application is insecure by exercising it exits with the
verification code; a family that refused a change on policy or configuration
grounds, without exercising a runtime hole, exits with the policy denial code.
The catalog carries both, and the rule's own key decides which one applies.

## Reading findings from a coding agent

The `read_security_findings` tool projects the security findings out of a run
already in the store, grouped by family and filterable by level and location.
It returns the rule, the level, the title, the bounded description, the fix and
the location, and never a value, so the loop is read a finding, read its fix
and its location, change the code, re-run the rehearsal, and read again.
