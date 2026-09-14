# changed

Four places in the shipped tree phrased a deliberate boundary as work somebody
would get to later, and a boundary described as deferred reads as a hole. Each
now states the decision it always was, with no behavior touched.

A budgeted key still refuses a streaming request with a 400, but it no longer
says streaming is "not supported yet" or that passing the stream through is
"worth doing when something actually streams". A budgeted key is non-streaming
by design: the budget is a per token spend limit, and a streamed response
carries no total token count until it finishes, so there is no figure to meter
the spend against while the tokens are on the wire. Refusing is the fail closed
answer and it is the correct one.

Per person GDPR erasure showed a red "Not implemented" badge and a rationale
that read like a build list, naming the foreign key walk and the audit chain
answer a future implementation would need. It now reads "Not offered by
architecture", in a neutral tone, because that is what it is: the audit chains
hash each entry into the next, so a per person deletion that reached them could
not rewrite them without breaking the tamper evidence the compliance record
itself depends on. Organization erasure, which is implemented and running,
stays the operation this product offers. The disclosure is unchanged in
substance and no erasure code path was added or altered.

The control plane's Key Vault module carried a "WHAT WOULD MAKE IT REAL"
comment framing an Event Grid expiry alert as a trigger to revisit. It is not a
step left for later: Key Vault raises SecretNearExpiry and SecretExpired as
Event Grid events, never as the Azure Monitor metrics every alert in
modules/alerting is built on, so wiring one would need a whole new subsystem
(a system topic, a subscription, and a bridge, because an event subscription
cannot target a Monitor action group directly) and would still leave the two
harms the block already documents. Rotation of github-client-secret stays an
operator procedure, on the operator's calendar, not a date in a file with
nothing behind it.
