# added

The authenticated authorization differential had a reader and no producer.

The authz family already knew how to decide an idor, a cross-tenant break and a
privilege escalation from structured per-persona observations, and canary_leak
already knew how to call a planted value in a response a leak. Both read a golden
that carried no canaries and observations no runner emitted, so both were live
code that could never fire: NewGoldenView had no caller, and every reach the
runner made recorded no authorization reading. A build could not tell a held
boundary from an unmeasured one, because nothing was ever measured.

A manifest now declares the ownership-scoped objects the suite cannot infer from
a diff: a concrete object at a route, who owns it, and the canary the
application's own seed planted into it. The engine builds the golden's canary
view from those fixtures and drives a new access-probe pass: the runner signs in
as each persona and once as nobody, reaches each declared object, decides inside
the run whether the object's canary came back, and emits one observation per
reach. A persona that reached another owner's object and got the canary back is a
proven idor or cross-tenant break; the owner reading its own object is the
self-access liveness arm, which is what lets a refusal prove a boundary held
rather than that the id was invented. The value stays inside the engine
throughout; an observation and a finding carry a route template, a class label
and a flag, never the token or the body.

Off by default in the strongest sense: a manifest with no access block does no
probing and pays nothing for it, and an absent access-probe pass is the honest
not-measured state the differential fails closed on rather than reading as a
clean pass.
