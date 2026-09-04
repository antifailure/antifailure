# Hosted MCP

The hosted endpoint complements the local standard-input server. It does not
pretend that a cloud service can read a developer's checkout. Remote tools read
reported projects, environments, runs and network events, or dispatch work through
the existing customer-owned execution path.

Use the official TypeScript SDK's stateless JSON Streamable HTTP transport. A
server process per request avoids routing session memory between replicas.
Standalone event streams are not supported and answer 405. Authentication is
required for every protocol request, not only initialization.

Browser authorization uses dynamic public-client registration, exact redirect URI
matching and PKCE S256. The existing product session approves a specific client,
organization, resource and scope. Codes expire after five minutes. Claiming a code
and minting its access token share a transaction so a failed write cannot consume
the grant and concurrent exchanges cannot issue two credentials.

MCP tokens are distinct from engine and CLI credentials, stored only as hashes,
bound to the configured endpoint, and checked for expiry and revocation on every
request. Membership and role are read again. The current grant lasts ninety days,
is revocable, and does not issue refresh tokens. The consent screen states this
lifetime before approval.

The management page must show only recorded facts: registrations, issued grants,
their expiry, revocation and last authenticated request. Stateless HTTP requests
are not persistent connections. Existing audited credential revocation is reused.
Local tool invocations are not included in hosted counts.

Validation includes real PostgreSQL policies, actual mounted HTTP endpoints,
independent mutation tests, browser consent, a real SDK client and explicit negative
controls for tenant, role, scope, audience, expiry, replay and CSRF boundaries.
Production delivery remains subject to the release and deployment gates.
