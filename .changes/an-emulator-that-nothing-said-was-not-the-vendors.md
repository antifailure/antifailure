# added

Google Cloud in an environment, and an honest table of what that means.

Six services are answered inside the environment: Cloud Storage, Pub/Sub,
Firestore, Datastore, Bigtable and Spanner. A Google host outside that list is
not routed to an emulator at all, so it is refused by the egress policy rather
than answered, because a wrong answer from an emulator is worse than a refusal.

Six containers, not one. Google ships no single emulator the way LocalStack is
one for AWS: its official emulators are five separate programs, four of them
inside the Google Cloud CLI image and selected by the container's command, and
Cloud Storage has no official Google emulator at all. So `EmulatorContainer`
grew a `Command`, without which four of the six are identical containers that
run a shell and answer nothing.

The one that is not Google's is the reason the declaration grew an `Official`
field. Cloud Storage is answered by fake-gcs-server, which is a community
project with no affiliation to Google, and nothing in the emulator declaration
could say so: a reader had to already know which of the project names were
vendor names. A guide printing six emulators in one table with nothing
distinguishing them would have presented somebody else's software as Google's,
and a user who found that out from a failing test was misled by us.

The guide publishes what was measured rather than what was hoped. An
unmodified `@google-cloud/storage` and an unmodified `google-cloud-storage`
both create a bucket, upload, download and list with no endpoint override and
no emulator environment variable. The other five services speak gRPC, and the
sidecar's inspected path reads HTTP/1.1 out of the connection it terminates,
so an unmodified gRPC client spends its whole sixty second deadline retrying.
The same client against the same proxy forwarding HTTP/2 instead answers in
456 milliseconds, which is what says the blocker is one property of one file
rather than anything about the emulators.
