# security

A documentation page that says exactly what crosses from a customer's boundary
to a control plane, with the file behind every line of it.

The claim on the site is that production data stays inside the customer boundary
and that the control plane receives evidence rather than records. Nothing in the
repository showed a reviewer how to check that, so the page is built the way the
vendor list is built: read the code that talks to the far end, then write down
what it sends.

Most of the claim survives contact with the code. Production rows are read from
the machine the engine runs on, the golden is an image on that machine's Docker
daemon and nothing pushes it, artifacts are never uploaded, every connection
string is registered with the redactor where it is obtained, and the engine
dials out with nothing dialling in. A hosted run starts as a request to GitHub
to dispatch the customer's own workflow, and even a cancel arrives as the answer
to a heartbeat.

Four things are weaker than the sentence sounds and the page names all four. An
invariant that does not hold carries up to five rows of the twin's database into
the check comment, and the control plane stores that comment, so records do
cross on that one path. Table names, column names and per-table row counts ride
the masking events. Every line of build output is an event, redacted for
credentials and for nothing else. And an event type the control plane does not
recognise is forwarded rather than dropped, so what crosses is bounded by what
the engine emits rather than by a published list.

The page also states what it cannot prove: it is a reading of the source and not
a statement about how any deployment is configured or what it retains.
