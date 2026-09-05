# fixed

Enabling Stripe no longer makes a production Terraform plan read the payment
credentials. It constructs their Key Vault references and lets the application's
managed identity resolve them during deployment. The planning identity can keep
its existing permissions, which do not include reading production secrets.

Credentials must still exist before billing is enabled. Azure reports a missing
reference during deployment rather than Terraform reading it during planning.
