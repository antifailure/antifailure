# security

Hosted persona credentials were derived only from values that are not secret, so
they were not secret either, and one environment's `af up` locked every other
environment out of the persona it shared.

A persona created through Clerk, Auth0, WorkOS or Supabase's admin API lives in
the provider's tenant, and the provider holds one account per address, so every
environment that reaches the tenant uses the same account. Its password and
second factor differed between environments, so each `af up` reset the shared
account and signed every other environment out. And they were derived only from
values that are not secret, so they were not secret either.

A hosted persona's credentials are now derived from the tenant's admin token,
which every environment that reaches the tenant already holds. Every
environment arrives at the same values, and they are as secret as that token.
An empty admin token is refused with AF-DB-025.

What to do: upgrade. Rotate nothing. The next `af up`, `af test` or
`af explore` against a hosted tenant replaces every hosted persona's password
and second factor with the new values. A person who enrolled an authenticator
app by hand against a persona's old second factor has to enrol it again.

Two rules are documented with it. Environments that share a tenant share its
admin token, because two environments reaching one tenant with different tokens
derive different passwords. And `af down` does not delete a hosted persona,
because another environment may be signed in with it, so one account per
persona address stays in the tenant; the personas guide says how to remove it.
