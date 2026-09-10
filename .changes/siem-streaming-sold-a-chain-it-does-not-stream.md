# fixed

The enterprise README sold "SIEM streaming with a tamper evident hash chain".
Both halves are real, they are not joined to each other, and the conjunction was
false in the way that is hardest to notice, because each half can be pointed at.

What ships is `ee/engine/auditsink`, registered in `ee/engine/cmd/af/main.go`,
asking the licence per call, forwarding the five actions the audit stream page
lists to Splunk, Event Hubs, an object store or a webhook. Its webhook signs an
HMAC over the exact bytes posted, so one delivery is tamper evident. Nothing in
it chains entries together.

The chain exists twice and neither copy is streamed. `audit_entries` carries
`prev_hash` and `entry_hash` and is written by the control plane regardless of
any licence, which is MIT and is not sold. `ee/web/audit` implements the
forwarder that would carry that chain to a sink, with a bounded queue, four
sinks and signed batch manifests over the chain head, and nothing imports it. It
is not a declared dependency of any package in the workspace, including
`ee/web/server`, so it could not be imported without a package.json change,
while its own suite runs and passes in CI on every pull request.

So the control plane's audit log reaches no sink. Single sign on logins,
directory provisioning, operator impersonation and every admin action are
written to `audit_entries` and forwarded nowhere.

The customer facing page was already right. `docs/enterprise/audit-stream.md`
scopes itself to the engine in its second paragraph and lists exactly the five
engine actions. It was the one line summary in `ee/README.md`, repeated in
`LICENSING.md`, that sold more than the product does, which is where a product
oversells itself first.

This corrects the claim rather than building the missing half. The wiring is
enterprise side work and is described where the claim used to be, so the
paragraph is deletable by whoever lands it.
