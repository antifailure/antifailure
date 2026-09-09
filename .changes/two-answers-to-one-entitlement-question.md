# fixed

There were two entitlement systems and they did not know about each other.

The engine has `license.Feature`, twelve names a signed license may carry. The
control plane has `organizations.plan` and `entitlements.ts`, which is real,
tested, and about quotas. Nothing reconciled them, so "what does this customer
get" had no single answer, and a self hosted license and a hosted plan could
disagree in silence.

`ee/engine/feature/catalogue.go` is now the one answer. Every feature carries
what it is, where it is enforced as `path:symbol`, and, when it is not refused,
which reason applies: it is refused by the control plane rather than the engine,
it is implemented and deliberately free, it is built and loaded by no binary, or
the capability does not exist.

Of the twelve, five are refused when a license does not name them, three by the
engine and two by the control plane. Seven change nothing whatever when they
are absent, and the page says so.

THE FIRST VERSION OF THIS CATALOGUE PUT FOUR OF THE TWELVE IN THE WRONG PLACE,
and the four are worth recording because they failed in two opposite directions
with one cause. Every one of them found real code and never asked who that code
served.

Two were UNDER claimed, because the measurement read one language. Enforcement
lives in Go and in TypeScript; the count was of `feature.Enabled` call sites,
which only the engine has. `sso` and `scim` came back zero and were published as
features nobody has, paid or not, while the enterprise control plane refuses
both by name with a 402. A zero in that count means not enforced by the ENGINE,
which is a different fact from not enforced.

Two were OVER claimed, because the code served a different subject. `billing`
read as free because `billingRouter` is real, mounted and ungated, and that code
bills the customer FOR Antifailure, while the licensed feature would meter on
the customer's own behalf and does not exist. `enterprise_dashboard` read as
covered by the plan because the console really is refused below the enterprise
tier, which is our own funnel enforcing our own pricing rather than anything a
license grants. Both are named in `notShipped` and cannot be sold at all.

Two checks now hold those two failures, and NEITHER COVERS THE OTHER'S CASE,
which is stated here so nobody assumes one gate closes the class. A cross
language check compares the catalogue's state claims against the TypeScript
registry's declarations in both directions, and compares the named site byte for
byte against what that registry actually declared; it catches `sso` and `scim`
and cannot catch `billing`, because that row was true about the code it named. A
second check holds the catalogue to `notShipped`, whose own comment says it is
the only place that has to change when one of these is built; it catches
`billing` and `enterprise_dashboard` and cannot catch `sso` and `scim`, because
those are sellable and were merely described wrongly. Nothing automated found
`billing`. A person read the catalogue's own prose and asked who the code was
for.

A state meaning "refused on the plan as a whole" has been REMOVED rather than
left unoccupied. It was the affordance that made the wrong classification
available, and a refusal keyed on our own pricing tiers does not describe
anything a license sells.

The rest of the scaffolding stands. A feature in the license and not the
catalogue fails. A `feature.Declare` with no catalogue entry fails. A row
claiming enforcement at a file with no `feature.Enabled` call for that exact
feature fails, which is how `compliance_packs` was found to have spent its whole
life declared at `compliance.Pack.Evaluate`, a function that takes no context
and therefore cannot ask about a license at all. For each feature the catalogue
calls enforced in the engine, the real entry point is called twice, once with a
license granting everything else and once with everything, and the behaviour has
to differ. The control plane's half is checked from the control plane, because a
developer renaming `orgProcedure` does not run a Go module that is deliberately
outside the workspace.

The licensing page's feature table and its count are generated from the
catalogue and say the number out loud, including when the number is
unflattering.
