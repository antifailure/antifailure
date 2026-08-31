# fixed

Four defects in the production Terraform, each of which only an apply could
find, and three of which looked like success.

Every `terraform plan` of the control plane stack exited non-zero, on staging as
well as production. The stack's `custom_domain_verification_id` output returns a
value the Azure provider marks sensitive, and Terraform refuses to evaluate a
root module output carrying one unless the output declares it. Outputs are
evaluated after the resource diff is printed, so the run showed the whole plan,
ended with its own "0 to destroy" line, and then failed. The output is now
declared sensitive; read it with `terraform output -raw`.

The custom domain and its managed certificate were ordered the way it reads
rather than the way Azure accepts it. Azure will not issue a managed certificate
for a hostname that is not already bound to an app in the environment, and says
so with `RequireCustomHostnameInEnvironment`. The hostname is now added first
with no certificate, the certificate is issued against it, and the guide has the
one command that binds the two, which is the part Terraform cannot own because
naming the certificate on the binding is a dependency cycle rather than a
mistake.

The certificate expiry probe was created disabled and nothing said so. The
provider defaults a standard web test to `enabled = false`, the availability
test set it and this one did not, so the rule watching it was enabled, wired to
the action group, and permanently healthy over a probe that never ran. It is the
alert guarding the one failure that is otherwise silent, a managed certificate
that stops renewing.

The standby's availability zone is now ignored the way the primary's already
was. Azure assigns it and this configuration never will, so every plan after the
first apply proposed one in-place change forever, and a plan that is never empty
is one people stop reading.

`backend.production.hcl` is git-ignored, which the guide to standing up
production already said it was. Only `backend.hcl` was, so the name of the
Terraform state storage account, which this repository deliberately carries
nowhere, was one `git add` away from being published. The plan files that the
same guide and the infra workflow both write inside the stack directory are
ignored too, because a plan file carries the values of sensitive input variables
and one of them is the GitHub OAuth client secret.
