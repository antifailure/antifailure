# added

The Helm chart can set every configuration variable the control plane reads.

It could set 15 of the 46 the configuration reference documents. The other 31
had no value in the chart and no generic escape hatch either, so a Kubernetes
installation could not turn them on at all. That is not a documentation gap. The
operator portal, the whole GitHub App and therefore installations and webhooks,
all of billing, all of mail, the secret that seals customers' provider keys, the
analytics pipeline and the origin a marketing site is allowed to post from were
unreachable, and every one of them presents as a broken feature rather than as
something nobody configured. The contact form is the clearest case: it tells the
visitor to check their connection, when the server refused the request on
purpose because it had no origin to allow.

The chart now names all of them, with the argument for each written where an
operator sets it, and validates the sets that are all-or-nothing. A GitHub App
missing its private key, billing missing its webhook secret, an operator pool
with no operator credential and a required plan with billing off are refused at
render time with a sentence, rather than installing and failing later or, worse,
running half configured. `helm install` now prints which optional features are
off in the release it just created.

`extraEnv` is there too, for a variable the chart does not name yet and for one
that belongs to something else in the pod.

`tools/wirecheck` was the check that should have caught this and could not: it
compared the reference page against the Terraform module only, so a variable
could be documented, read by the application, wired for the hosted installation
and unreachable for every self-hosted Kubernetes one while the gate stayed
green. It now asks the same question of both installation routes, and the
exemption file carries the route each reason applies to, because a reason true
of one route is routinely false of the other.
