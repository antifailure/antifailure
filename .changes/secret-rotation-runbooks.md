# added

A runbook for every secret in the control plane's Key Vault, at
`/docs/self-hosting/rotating-secrets/`. There are eight rather than the six
Terraform writes, because the GitHub App's private key and webhook secret live
there too and are read rather than managed.

Each says what breaks while the secret is being replaced, and two carry a
warning that is not about rehearsal. Rotating the provider key sealing secret
destroys every stored provider key and there is no re-sealing tool. Rotating
`database-url` needs an `ALTER ROLE` that nothing in this repository runs for
you: the bootstrap job creates the application role only when it is absent, so
changing the vault value alone hands the application a password Postgres has
never seen.
