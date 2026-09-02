# added

`https://antifailure.dev/openapi.json` and `https://antifailure.dev/errors.v1.json`
are published at the apex, so an agent that guesses at either address finds it.

The OpenAPI document is a file generated from the router, validated before it
can be committed, and pinned to the revision that built it, rather than a
request-time proxy of the production control plane. The site deploys on every
push to main and the hosted control plane moves on a release promotion, so a
proxy would have served the pre-promotion document while the site's own
documentation described the new one. A proxy can also only validate what fits in
a function: the one written first checked three root keys, and a document whose
nested path item was malformed passed and was cached.

The deploy now checks both documents byte for byte against what the run built,
because a stale document is a perfectly healthy 200, and fails when the API
version the apex publishes differs from the one the control plane serves.
