# added

Sign-in and webhook deliveries work on the hosted control plane. `github_app_id`
is set in `production.tfvars`, so the stack reads the App's private key and
webhook secret from Key Vault and hands them to the container app: the sign-in
redirect now carries the real OAuth client id instead of a placeholder, and
`POST /webhooks/github` verifies a signature instead of answering 503 to
everything.

# fixed

The `installation.created` delivery GitHub sent when the App was installed had
been refused with a 503, because the control plane had no App configured at that
moment and GitHub does not retry a webhook. `github_installations` was therefore
empty, which is the state in which everybody who signs in lands with no
organization and an empty screen. The delivery was redelivered and accepted, and
the standing-up guide now says to check the delivery log rather than assume the
install wrote a row.
