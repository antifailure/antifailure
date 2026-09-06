# added

The hosted control plane reports its own MCP tool calls and the model calls it
brokers, and nothing it reports belongs to a customer.

Two producers, both server side, both off unless `AF_POSTHOG_PROJECT_KEY` and
`AF_POSTHOG_REGION` are set. A hosted MCP tool call sends the tool name, which
is a closed set of the eight tools the surface registers, how it ended, and how
long it took. A brokered model call sends PostHog's own `$ai_generation` shape:
model, provider, the token counts the provider itself reported, latency and a
trace id, plus the cost when there was usage to price.

What is never sent is the point. Not a tool's arguments and not its results: a
tool call carries project identifiers, hostnames, table names, SQL and error
text, and `inspect_recorded_egress` alone would ship a customer's outbound
destinations to a vendor. Not a prompt and not a completion, which are the
customer's input and the model's answer about it. Not an organization
identifier: the pseudonym is the same domain separated HMAC the control plane's
own analytics already computes, so PostHog holds the identifier our own store
holds and neither is an org id, and with analytics off nothing is reported at
all.

`$ai_latency` is in seconds. Everything in this process measures milliseconds,
so sending one into the other is a silent thousand fold error on a dashboard
nobody would question. It is converted once, in the sink, and the test holds the
reported value against how long the request actually took rather than against a
plausible looking bound: the first version asserted the number was under sixty,
which a local stub answering in three milliseconds satisfies in either unit.

Nothing of this kind exists in the engine and nothing of this kind may be added
to it. `af mcp` runs on a customer's own machine, inside their network.
`engine/internal/telemetry` already carries the engine's events, requires a
redactor before any sink may write, and exports to the customer's own control
plane. A path from there to our analytics vendor would be an outbound flow
nobody agreed to, out of a product sold on the promise that production data
stays in the customer's boundary. That is now written at the top of the
telemetry package, where somebody would otherwise be tempted.

A failure cannot reach a caller. Both producers sit on load bearing paths, one
being a customer's agent and the other being the proxy that spends their money,
so every send is fire and forget, swallows its own errors, and is flushed at
shutdown rather than awaited on a request. The generation is reported after the
spend is recorded, never before, because the charge is the thing that must not
be lost.
