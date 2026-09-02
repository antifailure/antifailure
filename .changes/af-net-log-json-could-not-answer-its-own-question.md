# fixed

`af net log -o json` could not say whether a sandbox credential was actually
swapped.

The sidecar has always recorded `via`, `host_only`, `substituted`, `waited_ms`
and `limit` on every decision, and the runtime has always decoded them, but the
JSON view dropped all five. So a program reading the decision log could see
that a request to a payment provider was allowed and could not see whether the
credential was replaced on the way out, which is the question the log exists to
answer. The text table already showed the rate limit, so the machine readable
output was strictly less informative than the prose beside it.

The five fields are now carried, and the mapping lives in a function with a
test through the encoder. It had neither, which is why five fields went missing
on this surface without anything noticing.
