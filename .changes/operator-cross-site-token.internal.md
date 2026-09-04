# security

The cross-site guard on operator mutations did not run on any deployment where
the session cookie is Secure.

The operator cookie is written as `__Host-af_admin_session` when Secure and
under the bare name when not. The tRPC context read it with a reader that knows
both names; the guard in front of it read only the bare name, found nothing, and
skipped the origin check and the token check entirely. The request then reached
the mutation fully authenticated. Every deployment that serves the portal over
HTTPS was affected, and no deployment that serves it over plain HTTP was, which
is why the existing suite passed: it builds the bare name by hand against a test
server that speaks plain HTTP.

The guard now uses the reader that knows both names, and the suite asks its four
questions again with the name a browser actually sends. The console sends the
token it was already able to fetch.
