# security

The control plane's API moves to version 1.32.0 of the MCP TypeScript SDK,
which fixes four advisories published against every earlier 1.x release. The
one that reaches code the API runs is GHSA-46jr-4wx8-v453: a single HTTP
request inside the default size limit could exhaust server memory while the
SDK validated it, and the API serves MCP through the SDK's server and its web
standard transport. The other three are in parts of the SDK the API does not
use: experimental tasks not tied to the session that created them, the
bearer authentication helper accepting tokens issued for another service, and
the client transports following cross origin redirects with their headers.
