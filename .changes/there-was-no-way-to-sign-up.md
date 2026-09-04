# added

Anybody can create an account. Signing in with GitHub now lands you in your own
organization on the free plan, owned by you, and the free plan's quotas and cost
caps are enforced against it from the first environment. There is no card, no
invitation and no password anywhere in the flow: GitHub has to report a verified
address before a user row is written, which is the whole of the email
verification and is stronger than a link this domain could not send.

The organization is named after your GitHub account and carries its login, so
installing the GitHub App on that account later adopts the same organization
rather than creating a second one beside it. Environments, audit chain and plan
survive the step.

`AF_SELF_SERVE_SIGNUP=1` turns it on and it is off by default, because what it
grants is a tenant with real compute against it and forgetting a variable has to
close a door rather than open one. The process says which mode it is in at
start-up, beside the line about the sign-in allowlist, because the two settings
are one sentence: who may sign in, and whether there is anything on the other
side of the door.

# changed

The waitlist is gone. It stored one address per person in a table nothing in this
repository could read, and mailed nobody, on a domain that publishes no mail
exchanger and an SPF policy authorizing no outbound sender at all. Somebody who
left an address was waiting for a message with no route to them. The function,
its client, its scheduled probe and three error codes that nothing could return
any more are gone, and the sign-up page describes what pressing the button
actually does instead.

# added

A contact form for buying, which is what replaces the waitlist for anybody who
needs seats, single sign-on, a security review or an agreement to sign. It
writes a row into the control plane's own database, where the role that serves
public requests holds insert and no select, so no request to the site can ever
return somebody else's contact details. `af-control-plane-backup leads` reads
the queue, oldest first, and marks one handled. The confirmation on the page
says which of two things happened, because a deployment with no mailer records
the lead and tells nobody, and a form that answered a plain success there would
be the waitlist again with better spacing.

# fixed

The first operator could not exist. Operator accounts were created by a route
that needs an operator session, which needs an operator account, and nothing
anywhere ever wrote an operator's password, so the operator portal was
unreachable by anybody on every deployment.
`af-control-plane-backup bootstrap-operator` creates the permanent root operator
once and refuses to take over one that already exists, and
`set-operator-password` gives a password to an operator the portal created,
revoking every session that operator holds. The password is read from the
environment or standard input and deliberately never from an argument.

# fixed

The sign-in allowlist could not be configured to mean "everybody". Terraform
always set the variable, and an empty list rendered it as an empty string, which
the control plane reads as naming nobody, so the most open intent and the most
closed one produced the same deployment. The variable is nullable now, in
Terraform and in the Helm chart, with all three states rendered and checked.
