# added

Capacity can be sold to one customer without moving their plan.

`PLAN_QUOTAS` and `PLAN_COST_CAPS` are the price list and they are right: three
plans, fixed numbers, enforced. What they could not express is the customer on
`team` who was sold forty environments, the design partner using something
nobody else has, and the trial extended by a week because somebody asked on a
Friday. Every one of those is ordinary commercial reality, and doing any of them
by moving the plan charges the wrong amount.

An override is now a row: which limit, at which of four scopes, to what value,
WHY, who granted it, and when it stops. The expiry is not decoration. A grant
with no end date is how a one-week trial extension becomes permanent revenue
leakage nobody can find, so forever is something a person has to choose rather
than something they get by leaving a field blank.

The part that makes it real rather than a table: the three places that can
refuse a request now ask it. The environment quota and both cost caps on `af up`
read the resolved value, and there is a seat limit on invitations that did not
exist before. Every entry in the catalogue names the call site that reads it and
a test greps for it, so an entitlement cannot claim to be enforced and not be;
four entries name nothing and say why in prose instead.

The Plan screen shows the value that applies, the plan's own value struck
through beside it, an Override badge, the reason somebody typed, who set it and
when it ends. Before this, moving the quota under the screen would have left it
reporting twenty five while `af up` refused at forty, which is worse than
reporting nothing: somebody acts on it.
