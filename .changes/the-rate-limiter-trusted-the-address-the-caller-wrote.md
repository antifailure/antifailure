# security

Every rate limit on sign-in could be walked around with one request header,
and the sign-in audit trail recorded whatever that header said.

The control plane read the client's address from the FIRST entry of
`X-Forwarded-For`. Every proxy appends the peer it saw to the END of that
header and leaves whatever the caller sent in front, and Azure Container Apps
ingress, the only proxy in front of the hosted control plane, documents
exactly that: only the rightmost entry is its own, and everything else has to
be treated as the caller's. So a caller who sent `X-Forwarded-For: 10.0.0.1`
arrived as `10.0.0.1, <their real address>`, the limiter on `/auth/github`,
the OAuth callback, the magic link, the device code, the invitation and the
operator sign-in keyed on `10.0.0.1`, a new value on each request opened a new
bucket each time, and the address written beside the session was the one they
typed. The operator sign-in route passed the header to the database raw as
well, so a request through two proxies failed the `inet` cast and answered
500.

The trusted entry is now counted from the right, and both the limiter key and
the audited address come from the same selection. An entry that is missing or
is not an address puts the request in one shared bucket rather than exempting
it, so a header full of garbage is limited harder, never softer.

For an operator there is one new variable, `AF_TRUSTED_PROXY_HOPS`, the number
of proxies every request passes through before the process, default `1`,
which is the Container Apps ingress alone and the Helm chart's one ingress
controller. Set `2` when a Front Door, an Application Gateway or a WAF that
also appends to the header sits in front of the ingress. The Terraform module
and stack take it as `trusted_proxy_hops` and the Helm chart as
`config.trustedProxyHops`; a value that is not a whole number from 1 to 16
stops the process at startup, and the startup log says which entry is being
read.
