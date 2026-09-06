# fixed

An application that reads its webhook signing secret under a name of its own
could not receive a simulated event, and this repository's own rehearsal
environment was that application.

The engine derives one signing secret per provider and hands it to every
service as `STRIPE_WEBHOOK_SECRET` or `GITHUB_WEBHOOK_SECRET`. An application
reading `AF_STRIPE_WEBHOOK_SECRET` had no way to receive that value: a
manifest alias, `from: STRIPE_WEBHOOK_SECRET`, was looked up in the shell, the
`.env` file and the keyring, none of which can hold a value that does not
exist until `af up`, so the variable was reported missing and billing stayed
off. The derived secrets are now a source in the secrets chain, in front of
every stored one, so the alias resolves to exactly what `send_webhook_event`
and `af webhook trigger` sign with, and `af explain` names that source.

The github catalogue now carries `installation`, the first event a new
customer's control plane receives, and every github sample names the account it
is about, so a delivery reaches the handler rather than being acknowledged as
"the payload names no account". Each github delivery now carries the
`X-GitHub-Event` header, which the sender never set, so every event arrived as
"unknown"; and its own delivery identifier, which was a constant, so the second
delivery of anything was fenced as a replay of the first.

Three things about `send_webhook_event` and `af webhook trigger` that stopped
the orderings being read. A field value that parses as JSON is now sent as
JSON from the MCP tool, as the CLI already did and the tool's own description
claimed, so a subscription's items can be set. The application's answer is kept
on an accepted delivery as well as a refused one, because "already handled" and
"recorded; no organization holds this customer yet" are both a 200 and the body
is the only thing that says which. And `event_id` pins the provider's event
identifier, so the same event can be sent twice and a retry rehearsed on
purpose rather than by racing the clock.

This repository's manifest declares `webhook_path` for Stripe and GitHub,
replaces the typed placeholder webhook secret with the alias, and turns the
GitHub App on in its own environment with rehearsal values, so the orderings a
launch depends on, a checkout completed before the organization exists, the
same event twice, a late `updated` after `deleted`, an installation created,
are rehearsed by the product rather than read for in the code.
