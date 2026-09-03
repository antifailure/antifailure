# added

The pricing page publishes what the free plan actually allows, and the numbers
come from the code that enforces them. Three environments live at once, twenty
four environment-hours committed by one run, and seventy two environment-hours
in any rolling day. All three were enforced in the control plane and printed
nowhere a customer could read, so the only way to learn the shape of the free
plan was to reach a limit and read the refusal.

They live in `www/lib/plan-facts.ts` and the page renders them from there.
`web/apps/api/test/plan-facts.test.ts` fails the build if that file and
`PLAN_QUOTAS` or `PLAN_COST_CAPS` stop agreeing, which is the same gate
`legal-facts.test.ts` puts over the legal pages and for the same reason: every
one of the seven published claims that test was written for was true when it
was written.

Two quotas are deliberately not published. `PLAN_QUOTAS` also declares
`goldens` and `artifactGigabytes` for every plan, and neither is enforced
anywhere: both are counted for display and no path refuses a creation over
either. Publishing a limit that nothing applies is the same defect pointing the
other way, so the test refuses those two by name until somebody wires them.

The page also answers the questions a visitor arrives with, including whether
the MCP server is free. It is. `af mcp` is a command of the engine, the engine
is MIT licensed, it runs on your own machine over standard input and output,
and the enterprise directory contains no MCP code at all.

The free tier is the page's primary action now rather than an outlined
afterthought beside an invitation wall.
