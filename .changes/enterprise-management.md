# added

An organization can now be run without emailing anybody. Settings holds the
display name, the billing contact that decides where invoices go, every live
session with a way to sign any of them out, a complete copy of everything this
control plane holds, and the way to delete the organization. Members gains
invitations, so a finance person or a contractor who is not in your GitHub
organization can join through a link, and a way to remove somebody outright
rather than waiting for a GitHub membership to change.

Deleting an organization is a state machine rather than a cascade, and it runs
in this order: what is running is torn down and nothing new can be started, the
Stripe subscription is cancelled at the end of the period you have paid for,
nothing else happens until that period ends, credentials and the GitHub App
installation are revoked, a complete export is produced and you are given a link
to it, and only then is anything deleted. Every step is recorded as it happens,
so a deletion that is interrupted picks up where it stopped rather than starting
again, and it can be called off at any point before it finishes.

Closing your own account erases your name, address, identity and avatar, removes
your memberships and signs you out everywhere. It is called closing rather than
deleting because the audit log keeps what you did under the name you had at the
time: the log is a hash chain, so an entry cannot be rewritten, and those
entries go when the organization does.

Five new permissions: `organization.settings`, `sessions.manage` and
`data.export` for an admin, `organization.delete` for an owner alone, and
`account.close` for every role, because leaving is about you rather than about
the organization.
