# security

The suite that guards operator mutations against cross-site requests now runs
against both cookie configurations, not just the one nobody deploys.

`startApi` defaults `secureCookies` to false, and the fixture built the cookie
name by hand, so every assertion ran against a plain HTTP server holding a bare
`af_admin_session`. Under Secure, which is every real deployment, the server
writes `__Host-af_admin_session` instead. That is the configuration in which the
guard had never run, and the suite could not see it. The questions are asked
twice now, with the name each configuration actually writes.
