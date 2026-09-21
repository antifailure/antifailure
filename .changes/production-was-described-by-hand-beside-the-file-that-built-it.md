# added

Everything this engine does to rehearse a change against a twin of production
depends on knowing what production is: which services run which images, how
much CPU and memory they get, how many replicas, which Postgres version and
which server parameters, which buckets and queues exist, what the health probes
check, what the network lets out. All of that had to be written into a manifest
by hand, beside the infrastructure as code that actually built production, and
the copy went stale the first time somebody edited one and not the other.

A new reader reads the infrastructure instead. Its primary input is
`terraform show -json` plan output, because a plan has already had every
variable, local, function, for_each and module evaluated by Terraform, so every
number a reader cares about is a literal and nothing is unresolvable. Failing
that it reads Terraform and OpenTofu HCL, raw Kubernetes manifests, and the
Kustomize image and replica overrides that would otherwise have made the
reported image a tag production does not run. Every component says which of
those it came from, because a fully resolved description and a partly resolved
one must not carry the same authority.

It never executes anything, never reaches a cloud, and never reads a Terraform
state file. Those are not conventions. There is one entry point and it takes a
directory, so a state file cannot be handed to it in the first place, and a
state file is recognised by its content as well as its name, because a state
file copied to `plan.json` is still a state file full of plain text passwords.
A test walks the package's real import graph and fails if it can reach
`os/exec` or `net/http`.

The part that matters most is what it says when it cannot read something. A
variable with no default, a value that only exists after an apply, a module
from a registry, a Helm chart it will not render: none of those are reported as
zero, and none are guessed. Every field is one of three answers, "not
declared", "read", or "declared and I could not resolve it", the third carrying
a sentence and a line number, and the value of an unresolved field cannot be
read at all without asking. A resource behind a `count` this reader cannot
resolve is reported as neither present nor absent, because it is genuinely
neither until Terraform runs.

A value the reader worked out and refuses to carry because it is a credential
is a fourth answer again, separate from one it could not work out. Both are
things it cannot tell you; only one is a hole somebody should go and look at,
and a report that counted them together would send somebody hunting a defect
that is not there.

Environment variable names, never values. Secret references, never secrets.
Four independent controls drop a value and the engine's own redactor catches
what they miss, which was measured rather than assumed: `terraform show -json`
writes a sensitive variable's value in the clear in three separate places and
marks it in only two of them.
