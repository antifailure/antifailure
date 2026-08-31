# fixed

The enterprise binary now registers the organization policy hook, which it
never did. `policyenforce.Hook` was written, tested and property tested, and no
binary ever constructed one, so an installation licensed for
`policy_enforcement` refused no environment at all and its compliance report
said no policy was configured. Point `AF_ORG_POLICY_FILE` at a YAML policy and
the engine says which rules are in force at startup and refuses an environment
that violates one. A policy file that cannot be read stops the engine rather
than starting with the policy silently off.
