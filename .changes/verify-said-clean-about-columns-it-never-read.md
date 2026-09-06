# security

`af mask verify` said clean, with zero findings, about a database in which
`af mask plan` listed 145 columns as copied unchanged, nine of them Stripe
identifiers and two of them sealed private key material. Three things let it.

The scan read six text types and nothing else. A `bytea`, an array or an enum
was not read, not skipped and not counted, so a column holding a sealed private
key was invisible to the one check that gates publication, and a comment in
the detector file claimed a source value check covered what the shape
detectors missed. No such check existed.

The credential detector knew Stripe's secret key prefixes and not its object
identifiers, so even the `text` columns holding `cus_` and `sub_` values, which
the scan did read, tripped nothing. A Stripe customer id is a live pointer into
a real account, the same in every environment, and it was copied into every
golden.

The 145 count was printed once, at the very end of `af mask plan`, and nothing
downstream carried it: not `af mask apply`, not `af golden refresh`, not the
attestation, not `af golden list`, which said `verified`.

Now the scan reads every column it can read as text, decodes `bytea` as UTF-8
where it decodes, lists every column it could not read by type, and fails when
such a column has no rule and a name that says it holds a secret (`AF-MSK-013`).
A `provider-identifier` detector recognises Stripe, PostHog and Resend id
families. The count of columns copied unchanged with no rule is printed at the
top of the plan, beside the verdict of `af mask verify`, `af mask apply`,
`af golden refresh` and `af golden pull`, in a `NO RULE` column of
`af golden list`, in the attestation, and in `inspect_goldens`. Two transforms
were added for the rules that were missing: `prefixed_id`, which keeps a
`cus_` prefix and hashes the body, and `repository`, which masks an
`owner/name` the way `username` masks a handle. This repository's own
`masking.yaml` names every Stripe identifier, every `bytea` column, and
`repositories.full_name`, which was preserved by rule and is the customer's own
GitHub organization.
