# fixed

Two secret stores reported themselves usable without ever speaking to the store.

The reachability check for Google Secret Manager acquired an OAuth token and
stopped there. Google's token endpoint is a different host from Secret Manager,
so a project whose Secret Manager API was never enabled, a Service Controls
perimeter, or a typo in the project id all handed back a perfectly good token,
and the source reported itself available. The lookup chain then listed it as a
place a missing variable could have come from, while nothing in it could be
read, which is the exact failure the chain's reason reporting exists to prevent.

AWS Secrets Manager had the same fault and worse. Where credentials come from
the environment it made no network call at all, so no unreachable store could
fail it: not a virtual private cloud endpoint pointed at the wrong place, not a
region the account has never enabled, not a mistyped endpoint. Where credentials
come from a container or instance role it did make a call, to the credential
endpoint, which is a different host from the one secrets are read from.

The argument that had kept the check out of the AWS adapter was that a probe
would be a signed, billed call on every run. It is neither. The probe happens at
most once per process, because the source guards it with a sync.Once, and it is
sent unsigned, which the service answers with a missing authentication error.
That answer is the whole proof being sought, since what is in question is
whether the host is there. Sending it unsigned is also the safer choice: the
address being wrong is the case the check exists to detect, and a signed probe
would hand a working credential to whoever owns the mistyped name.

Both adapters now reach the store, and a refusal still counts as reachable,
because whether a credential may read one particular variable is a different
question answered per variable.

The same defect was found in the Azure adapter by its first run against a real
vault and fixed there alone. It survived in these two because neither has ever
had a live run, and because no local fake could show it: each fake is one
process serving both the credential endpoint and the store, and each harness
built its unreachable case by pointing the whole fake at a dead address, so the
two failed together and the credential failure hid the other one. The tests
added here split the two hosts, which is the only arrangement in which this is
visible without an account.

AWS had also never been run through the secret store conformance suite at all,
while the other three adapters had. That is why the behaviour named "is
unavailable with a reason when the store cannot be reached" had been written and
passing for years without anyone discovering that this adapter could not satisfy
it. It runs against that adapter now.
