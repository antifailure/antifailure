# added

The operator portal can see the repositories, credentials and deliveries on an
installation, and revoke a credential that has leaked.

Four sections. **Repositories & Pull Requests** lists every repository connected
to the installation and, for one of them, its pull requests with the newest
check generation on each, so a customer reporting that a check never appeared is
answered from the row rather than from a log. A fork approval that no longer
covers the head is reported as a stale approval rather than as an approval.

**API Keys** lists every credential that can act as a customer, with its prefix,
what created it, what it acts as and when it was last used. An operator holding
`admin.keys.revoke` can revoke one, and revoking a GitHub workflow identity
binding also revokes every token that binding has minted. There is no rotate:
only a hash is stored, so nothing in this product can produce a replacement, and
the page says so rather than offering a button that could not work.

**Integrations & Webhooks** shows the GitHub App installations and every
delivery that arrived from GitHub or Stripe, including the ones that resolved to
no organization and the ones nothing handled. It is inbound only, because that
is what this product records.

**MCP Management** reports that this control plane holds no MCP record, because
`af mcp` runs on the developer's machine and never speaks to it, and lists the
four tools the engine serves with the file and symbol each claim comes from.
