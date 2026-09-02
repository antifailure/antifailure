# added

A job in GitHub Actions can now get an engine token from its own workflow
identity, so nothing has to be pasted into a repository secret.

Until now, running Antifailure in CI meant creating an engine token by hand and
storing it as a secret. That credential is readable by every workflow in the
repository, has to exist before anything works at all, and never expires, which
makes it the thing most likely to still be valid a year after the person who
created it has left. The job now asks GitHub for a workflow identity, posts it
to `POST /v1/auth/github-oidc`, and gets back a token that expires in fifteen
minutes and works on `POST /v1/events` exactly as a static one does.

The part worth reading is what the control plane does NOT conclude from that
identity. A verified token says, truthfully and with a signature nobody can
forge, that this job runs in repository R. It says nothing about who R belongs
to. Anybody with a GitHub account can create a repository, put `id-token: write`
in a workflow, and mint a genuine token naming it. A verifier that read the
repository owner and looked up the organization for that owner would have
authenticated a stranger perfectly and then authorized them anyway, one workflow
file away from writing events into somebody else's tenant.

So the claim is an identity and never a permission. An organization claims a
repository in advance and an unclaimed repository is refused. Three things make
that claim mean something: at most one live claim per repository across the
whole installation, so a verified claim resolves to exactly one organization or
to none; creating one needs the same role as minting an engine token by hand,
because it grants a workflow that ability standing; and the organization has to
hold a live GitHub App installation on the repository's owner, checked in the
application where it can produce a sentence somebody can act on and again in the
database where a bug in the application cannot get past it.

Revoking a claim revokes its live tokens in the same transaction. A revocation
that leaves fifteen minutes of working credentials behind has not revoked
anything, and fifteen minutes is exactly the window somebody revoking in a hurry
cares about.
