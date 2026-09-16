# added

The security suite could see that a change reached an outbound host, but not
that it was coaxed there, not that a payload changed how a query ran, and not
that the change now creates three PaymentIntents where it used to create one.
The egress and capture logs counted requests; nothing counted what a request
MEANT.

Three security check families now read those same logs and drive the same twin
to prove behavior rather than match a string. The injection family fires SQL,
command, template, NoSQL, path traversal and dynamic query payloads at the
endpoints a change touched and reports only the ones it can prove altered the
application: an injected sleep that moved the response time, a database error
that surfaced the schema, a template that returned the evaluated product rather
than the expression, a traversal that read a file outside the application root,
an operator that turned a refusal into an answer. Every payload is measured
against a benign control, so a slow endpoint or a reflected value is never
mistaken for a proven one, and a finding names the endpoint and the class and
never the payload value.

The SSRF family reads the egress decision log for a request the application was
coaxed into making against an internal or metadata address, and takes the
firewall's refusal of that request as the proof. It is a reader over the
existing egress mechanism, not a second firewall: an allowed internal request
is the twin talking to its own services and is not a finding, and a refused
external host stays the egress layer's surprise to report. A refused reach to a
loopback, link local, private, carrier grade or metadata address is the finding,
and a refused internal reach a webhook or callback produced is reported as
callback drift, because the fix is different.

The side effect family counts dangerous effects by meaning, a charge, a refund,
an email, an SMS, a webhook, a cloud create, a cloud delete, a queue publish,
and compares the count against the same workflows run on the base branch. An
increase over the base is a policy denial the base branch did not incur; a
destructive operation fires on its own with no baseline defense, because the
base deleting a bucket too is not a reason to allow deleting one. A base twin
that could not be built leaves the increase unmeasured rather than reporting
every effect as new, because a zero that means "did not measure" is the defect
this repository keeps finding in its own instruments.

Each family emits an ordinary security finding in the security namespace, so it
rides the verdict, the exit code and the read_security_findings projection the
spine already wired, and a finding never carries a response body, a row, or the
payload that produced it.
