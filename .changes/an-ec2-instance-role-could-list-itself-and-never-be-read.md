# fixed

An EC2 instance role could never supply AWS credentials. The chain asked
instance metadata for a version 2 session token and sent it when listing the
role, then read the role's credentials without it, and an instance that requires
version 2 answers that request 401. The failure was then reported as metadata
that did not answer, on an instance where it had answered twice. Every AWS call
the enterprise edition makes on an instance role was affected: the Aurora
provider and the AWS Secrets Manager backend both refused to start. It was found
by running the Aurora provider on a real EC2 instance, and the error now says
what metadata answered when the role path fails.
