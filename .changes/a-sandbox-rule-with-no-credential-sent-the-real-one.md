# security

A sandbox rule whose credential was never configured sent the application's own
credential to the provider.

Substitution only happens when a value exists for the name the rule refers to.
When none did, the sidecar forwarded whatever the application sent, and in
every other column that request was indistinguishable from a working sandbox
call: allowed, mode sandbox, the rule named, a normal status. The only evidence
was a count that said zero, and nothing computed that count.

`inspect_egress_firewall` now reports it as
`sandbox_credential_not_substituted`, and it always fails the check. It has no
manifest level and there is deliberately no way to turn it down: a threshold
expresses how much of something a project will tolerate, and there is no
tolerable quantity of a live credential leaving an environment that is running
unreviewed code against a copy of production data.
