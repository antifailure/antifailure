The secret that seals every customer's stored provider key could not be
rotated. Replacing it made every stored key stop opening, permanently, and the
failure was silent: rows recorded which key version sealed them and nothing read
that column, so the application tried every row against the one key it held and
reported the same failure for all of them. A value that will not decrypt is
indistinguishable from a value somebody altered, so an operator saw
authentication failures across every organization and no sentence saying why.
The column's own comment said it existed so a rotation could find the rows still
needing re-sealing. The rotation it anticipated had not been built.

The control plane now holds a SET of sealing keys addressed by version.
`AF_PROVIDER_KEY_SECRET` is still one key and still means version `v1`, so an
installation that has never rotated sets nothing new and its rows keep opening
byte for byte; `AF_PROVIDER_KEY_SECRETS` takes `v2=<32 bytes of base64>`,
comma separated, in the same grammar as `AF_LICENSE_PUBLIC_KEYS` and for the same
reason. The two are merged rather than one replacing the other, so a rotation adds
one value and never reads the old one back out of a vault to compose a combined
string. `AF_PROVIDER_KEY_VERSION` says which version new keys are sealed under,
and with several keys configured it is required: guessing which of somebody
else's keys to seal their credential with is not a guess to make.

A row whose version this control plane does not hold now raises a different,
diagnosable error from a row that fails authentication. It names the missing
version, names the versions that are held, says in those words that this is a
configuration rather than a damaged row, and carries no key material. The other
error now says the key for that version IS configured, so nobody goes looking for
a missing one. That distinction is the difference between a silent outage and a
message, and it is the half of this change that matters most.

`af-control-plane-backup reseal` opens every stored credential under its own
version and rewrites it under a new one. It is idempotent, resumable, and holds a
batch of rows rather than the table. Each row is its own transaction and the
UPDATE names the ciphertext it expects to replace, so two runs at once, or one
racing a customer saving a key from the console, ends with one write landing and
the other reporting the row changed underneath it. The new value is opened again,
under the new key and the new associated data, before the UPDATE is composed, so a
failure part way through leaves a row unchanged rather than corrupted. `--dry-run`
writes nothing. `--check` opens every row whatever version it is at, which is the
different question and the one to ask before throwing an old key away: "nothing
left to re-seal" and "every row is at the new version and none of them open" look
identical otherwise.

The rotation is proven end to end against a real Postgres, and the proof is its
last step: seal under one key, add a second, confirm the old rows still open,
re-seal, then REMOVE the first key and confirm everything still opens. With the
old key still configured, a rotation that quietly did nothing and one that
completed look the same.

`docs/src/content/docs/self-hosting/rotating-secrets.md` said not to rotate this
one. It now carries the procedure, including how the new key reaches Key Vault, a
start-up log line naming the versions a revision actually picked up, and the check
to run before the old key is removed. The Terraform holds the new key as a secret
it addresses rather than reads, so nothing planning the stack needs vault read
access, and a manual container app job runs the tool inside the virtual network,
which is the only place Postgres can be reached from.
