# MCP management

The operator page will show the configured hosted endpoint, registered OAuth
clients, and the credentials issued to people and organizations. Counts come
from the database. Last authentication is not called a connection or a tool
run. Local checkout tools remain documented separately.

The existing audited credential revocation route is the chosen write path.
A second MCP-only revocation implementation would duplicate authorization and
audit behavior. A documentation-only page would leave the new endpoint
unmanageable. The page uses the existing confirmation dialog and requires an
operator reason and the credential prefix before revocation.

The query returns the newest fifty credentials, an explicit truncation flag,
and whole-installation counts. The page provides the existing searchable key
directory for the rest. Database errors render the shared retry state; an
empty installation explains how a client connects. Browser verification covers
desktop, mobile, empty and populated states, refusal, and successful revocation.
Database assertions are independently mutation tested before integration.
