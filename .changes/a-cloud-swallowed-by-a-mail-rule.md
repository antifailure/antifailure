# fixed

An S3 write from an environment was answered as a delivered email.

The third party catalog registered `*.amazonaws.com` under "Amazon SES" in
capture mode. Every AWS host an application touched matched a mail rule, so an
S3 `PUT`, an SQS `SendMessage`, a Secrets Manager read and an STS
`AssumeRole` all fell through to the generic capture handler and came back
`200 {}` from code that believed it was holding an email. The application had
every reason to think the object was stored. Nothing left the environment and
nothing said so. The catalog held no GCP or Azure service host at all, so those
fell to the default block with no rule naming them.

Twenty two services across the three clouds are named individually now, on
twenty seven hosts between them, each with a sentence saying what it costs to
reach: a message on a real queue is picked up by production workers, a read
from Secrets Manager hands a production credential to unreviewed code, an
object write is indistinguishable from production data once it lands. Twenty
six of those hosts are refused. SES keeps capture and keeps only the mail
endpoint. The SMTP submission endpoint is named and blocked rather than left
implicit, because the sidecar speaks HTTP and mail sent that way would reach a
real address instead of the inbox.

Naming a service rather than a cloud needs a pattern the engine did not have.
Every regional AWS endpoint is `<service>.<region>.amazonaws.com`, so the only
leading wildcard that reaches S3 also reaches SES. A star anywhere but the front
of a pattern now stands for exactly one label, so `email.*.amazonaws.com`
reaches SES in every region and reaches nothing else, and `*.s3.*.amazonaws.com`
reaches a virtual hosted bucket. A star must be a whole label, and a pattern of
nothing but stars is refused because it matches every host while reading as
though it named one.

Capture no longer invents a success for a host nobody named. It answers
generically when the rule names the host and refuses otherwise, with a decision
saying which rule decided and why an invented success would be believed. Amazon
SES and Slack gained real handlers: SES because a mail rule that returns an
empty object is not a captured email, and Slack because every Slack client
reads `ok` first, so a captured message read as a failed one.
