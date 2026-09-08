# fixed

Single sign-on and directory provisioning are now refused for an organization
that is not entitled to them, and an organization can now refuse operator
support access to its own account. All three were built, tested end to end and
enforced by nothing: a bearer token naming an organization was the whole of the
authorisation for SCIM, a handle in a URL was the whole of it for single sign-on,
and an operator could act as a member of any customer with no way for that
customer to say no.

Losing the single sign-on entitlement relaxes the requirement to use it rather
than closing the account: the enforced flag stays on the connection, GitHub
sign-in works again, and restoring the entitlement restores the requirement
unchanged.

A licence can no longer be issued for `billing` or `enterprise_dashboard`.
Nothing in the product enforces either, so a licence naming one verified,
reported itself active, printed the feature, and changed nothing.
`tools/licensegen` refuses to sign one and the verifier never permits one.
