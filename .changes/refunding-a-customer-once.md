# added

Operators can move money, and the same button pressed twice moves it once.

Refund, credit, plan change, trial extension, cancel, reactivate, discount,
retry payment and resend invoice, on the operator portal, each with the reason
recorded beside it.

A refund button that does not refund is a support failure. A refund button that
refunds twice is somebody's money, gone, and an apology that does not fix it.
Between a double click, a client retry, a load balancer replaying a request and
an operator pressing the same button in two tabs, twice is not an edge case.

So every write is claimed in a ledger whose primary key IS the idempotency key,
before anything is sent, and the same string goes to the provider in its
`Idempotency-Key` header. The ledger closes the window between the two presses;
the header closes the one the ledger cannot, where a process claimed the key,
called the provider and died before recording the answer. A key reused with
different parameters is refused before anything is sent, rather than answered
with the first attempt's result, because reporting success for a refund that
never happened is worse than either refunding twice or failing.

The distinction that took two attempts to get right: a provider that REFUSED
made a decision, so a deliberate retry is a new request and gets a key of its
own. Reusing it there leaves a declined payment un-retryable forever, because
the provider replays its own refusal at a customer who has since fixed their
card. A request that got NO answer may have been executed with the response
lost, so its only safe retry carries the same key. There is no default that is
right for both.

A refund larger than what is left on a charge is refused before it is sent, with
both amounts and their currency named, so an operator who mistyped has not
consumed a key or left a refused refund in the provider's dashboard.

Every one of these is recorded twice: once in the platform's own chain, and once
in that customer's audit log, in the same transaction. A record only the vendor
can read is a vendor's private note rather than accountability.
