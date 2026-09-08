# fixed

Two implementations of one decision, with a comment claiming they were held
together by a file that was not in the tree.

`ee/engine/license` parses and evaluates licences in Go and runs in the engine.
`ee/web/server/src/license.ts` does the same in TypeScript and runs in the
control plane, because single sign-on and provisioning are mounted there and the
control plane has no engine in it. That file's own header names the hazard, and
then lists three things holding the two sides together. The second was this, in
the present tense: `ee/license-vectors.json` is a corpus of tokens with the
verdict each one must produce, one suite here reads it and one in the engine
reads the same file.

None of it existed. Not the corpus, not either of the two files it named, not a
single occurrence of the string in either suite. `ee/README.md` records three
claims of exactly that shape and the lesson written under them is that a claim
resting on an invented mechanism reads identically to a true one until somebody
goes looking, and that the person who goes looking is usually the customer.

The corpus exists now, emitted by the Go side because the Go side is the
definition, on the same pattern the community tree already uses three times for
the policy engine, for webhooks and for mock packs. Twenty one cases, covering
every state and every refusal either implementation can produce, checked by a
test on both sides that the coverage is complete rather than merely present.

It found two divergences on its first run, which is what the paragraph had been
promising to prevent.

The first is the one that reaches a customer. The engine has always refused to
permit a feature no build enforces anywhere, filtering them in `Evaluate`. The
control plane did not: it permitted every feature a licence named. So one key
made the engine say billing is not permitted and the control plane print that it
is, in one deployment, to one buyer, with neither binary aware of the other. It
is not reachable through the two gates the enterprise entry point mounts today,
and it is reachable through the startup line that reports what the licence
permits right now, which is the line an operator reads to check what they bought.

The second is at a boundary. At the exact instant a grace period ends the
control plane returned negative zero days, which JavaScript treats as a value
distinct from zero, and the engine returned zero, because it is an integer and
has no such value. Invisible in a rendered string and not invisible to anything
comparing the two.

The corpus also binds two lists that were duplicated across the two languages
with a comment saying they matched and nothing checking that they did: every
feature a licence can carry, and the features no build ships.

There is no signing key in the repository and this adds none. The corpus carries
the public half in the form an operator pastes into a deployment, and the tokens
are fixtures, so regenerating recomputes the verdicts and leaves the tokens
alone: a semantic change shows up as a diff of verdicts rather than of the whole
file.
