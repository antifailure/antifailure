# fixed

The Kubernetes runtime had no command anybody could type, and the number the
site quoted for the Docker one had gone stale by five behaviours.

`engine/internal/runtime/k8s/conformance_test.go` runs the shared runtime
conformance suite against a real cluster, and it is gated behind
`AF_KUBE_CONTEXT` because it creates namespaces, deletes namespaces and runs
pods that try to reach the internet. Required rather than defaulted, on
purpose. What was missing was the other half: a way to satisfy that
requirement without a paragraph of setup nobody repeated, which is why the
status row carried numbers from runs that could not be reproduced. `just
k8s-conformance` creates a single node k3d cluster, names that cluster's own
context in `AF_KUBE_CONTEXT`, runs every behaviour and deletes the cluster
afterwards. The guard is untouched for everybody else.

The count is the second half. Three documents stated how large the runtime
conformance roster is: the product page, the plan's status row and the claims
ledger. All three were right when they were written and all three said
thirty-two. Five behaviours were added to the roster afterwards by three
separate pull requests, none of them touched a document, and nothing anywhere
compared the two, so a customer facing page kept quoting thirty-two while the
suite held thirty-seven. `TestEveryQuotedRosterSizeMatchesTheRoster` now reads
those documents and fails when the number they quote is not the number of
behaviours there are. A document that has been reworded past its own sentence
fails too, rather than passing while checking nothing.
