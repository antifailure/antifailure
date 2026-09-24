# fixed

The v1.5.0 entry for the demo request page said self serve sign up was gated
off. It was not, and it never had been on either deployment.

`AF_SELF_SERVE_SIGNUP` comes from `self_serve_signup` in
`infra/terraform/stacks/control-plane/staging.tfvars` and `production.tfvars`,
and both have set it on since 2 September 2026. The variable declares a default
of off, and that default is what was read: the change that moved the site to a
demo request took the value from `variables.tf` rather than from either
environment, thirteen days after both had overridden it. So the page is a
deliberate choice about how the hosted plane is sold, and the note explained it
as a consequence of a closed door.

The published wording stays exactly as it was. A correction is appended to it
instead, dated, in both the changelog entry and the release notes, because
rewriting what was published would leave no trace that it had ever said
something else. Seven comments across six files in the site carried the same
claim and those are corrected outright, since a comment has no readership to
mislead retrospectively.

Nothing about the product changed and nothing user facing ever carried the
claim: no rendered copy on /signin or /request-demo said self serve was off,
checked against the live pages rather than the source.
