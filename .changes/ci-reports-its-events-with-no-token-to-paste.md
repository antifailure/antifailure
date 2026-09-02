# added

A run in GitHub Actions reports its events with no token to paste anywhere.

The engine's control plane sink took its credential from
`AF_CONTROL_PLANE_TOKEN` and from nowhere else, and nothing in any workflow this
project ships has ever set it. So on a CI runner the sink was never built: the
events that say an environment is coming up, is ready, or has been torn down
went to the local log and the terminal and no further, and the dashboard stayed
empty for every run anybody had.

A job that GitHub will vouch for now proves what it is instead. It asks the
runner for the identity GitHub signs for it, trades that with the control plane
for a short lived credential, and reports with that. This is the same exchange
the report step already performs, so a workflow needs `permissions: id-token:
write` and nothing else: no secret in the repository, nothing to rotate, and
nothing for a customer to store.

The credential is short lived, so the engine renews it when the control plane
refuses a batch and re-sends that batch rather than losing it. A refusal that
renewing cannot fix is attempted at most once a minute, and the events wait on
disk rather than being dropped.

`AF_CONTROL_PLANE_TOKEN` still works and still wins when it is set. A developer's
machine and a self hosted engine have no runner to vouch for them, and somebody
who has configured a token has said what they want.

Three things a fork or a misconfiguration used to say badly now say what to do.
GitHub declines to mint an identity for a pull request from a fork, which is
what stops a fork reporting as the repository it forked, and that now reads as
the deliberate refusal it is. A control plane too old to offer the exchange says
to upgrade it rather than answering "refused". A repository the control plane
has not been told about reads as a setup step rather than as an authentication
failure, and a rate limit says how long to wait.

Two fixes came with it. The example workflow set `AF_CONTROL_PLANE` while the
engine reads `AF_CONTROL_PLANE_URL`, so a self hosted installation would have
reported its events to the hosted instance, which is the address the engine
falls back to. And an engine token's `expires_at` was written and never read, so
a credential with an expiry authenticated forever; ingestion now enforces it.
