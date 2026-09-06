# fixed

The control plane buffered a request body of any size before deciding whether
to refuse it. This is a security fix.

The node server this process runs on sets no body limit, and the only two
places that set one were the PostHog proxy and the MCP endpoint, because those
were the two places somebody remembered. Both webhook handlers read the whole
body with one call and verified the signature over those bytes afterwards,
which is the right order for the signature and the wrong order for memory: a
stranger with no secret could post a multi gigabyte body to `/webhooks/github`
or `/webhooks/stripe` and the 401 came only after every byte had been read
into the process. `/trpc/*` and `/v1/events` had no bound at all.

Every endpoint now has a body limit, applied before any route runs, from one
catalog beside the rate limits: a default of one megabyte, and a named
exception with its reason for each route whose honest bodies are a different
size. GitHub deliveries are bounded at five megabytes, which admits an
installation event listing forty thousand repositories, the largest delivery
the events this App subscribes to can produce, and refuses the twenty above it
that GitHub itself would still send. Stripe events are bounded at half a
megabyte, an order of magnitude above the largest invoice. The
bring-your-own-key proxy keeps the provider's own thirty-two megabyte ceiling,
because a long prompt is the point of that route. A body over the limit is
answered 413 with a sentence naming the number, from the declared
content-length without reading a byte when the client sends one and from the
count when it streams, and a body under it reaches the handler byte for byte,
so the signature still verifies over what was sent.

Two console lists had no upper bound. `members.list` returned every member and
`runtimes.list` every runtime, when every other list on the console takes a
limit capped at two hundred or applies one, and a membership synced from a
large GitHub organization is exactly the case that comment warns about.
`members.list` now takes a limit and a cursor and answers two hundred at a
time; the cursor names both the timestamp and the member, because a sync
writes its whole batch in one transaction and every row shares one timestamp
to the microsecond, so a cursor on the timestamp alone could never page past
the batch it started in. `runtimes.list` is capped at two hundred. The Members
page follows the cursor until there is none, so the table it renders is still
the whole membership.

Renaming or transferring a repository on GitHub forked its history into an
orphan row. Every table of consequence points at the repository's id, so the
row is the history, and the `renamed` action went through the same upsert as a
new repository, keyed on the organization and the full name: the new name
matched nothing, a second row was inserted, and the first stayed live under a
name GitHub no longer served, with every environment and verdict still
attached to it. A rename now finds the row by the id GitHub keeps stable, or by
the old name it sends, and changes the name on that row. A transfer archives
the row under the owner it left, and records a fresh one under the owner it
reached only when that owner already has an installation here; before this a
transfer to an owner this control plane had never seen minted a tenant for
them and pointed the delivery's installation at it. The row does not cross
tenants, because every table beneath it carries its own tenant and its own
policy, and handing one customer another customer's run history because
somebody pressed Transfer is not a trade anybody agreed to.
