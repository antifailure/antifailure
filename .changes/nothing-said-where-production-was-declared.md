# added

A manifest could not say where its infrastructure as code lives.

Every section of the manifest described the copy: what to build, what to run,
what it may reach. Nothing described production. So the one thing a twin is
supposed to be measured against, the declaration of what production actually
is, was a file in the repository nothing in this product had an address for.

`infrastructure` is that address. It names the Terraform root modules that
declare production, and optionally the workspace and variable files production
is read through:

```yaml
infrastructure:
  source: terraform
  paths:
    - infra/terraform
  workspace: production
  var_files:
    - infra/terraform/production.tfvars
```

Nothing in it changes what an environment builds or starts.

`af init` drafts `source` and `paths` by reading the tree. It names root
modules rather than every directory with a `.tf` file in it, because a module
a stack calls is a building block: naming it would point a comparison at a
library rather than at the thing built from it. It never drafts `workspace` or
`var_files`, and says so, because which workspace holds production and which of
`production.tfvars`, `staging.tfvars` and `dev.tfvars` describes it is stated
nowhere in a repository. This is the one section nothing downstream can check,
so a guess there would compare a copy against staging and report a number that
looked right.

Validation refuses a path that is absolute, that climbs out of the repository,
that is named twice, that is not there, or that is a file where a root module
is a directory. Those refusals exist because nothing downstream would report
any of them: a directory that is not there declares no resources, and no
resources is the same answer an application with no infrastructure gives. It
also refuses a `workspace` or a `var_files` beside more than one root module,
because both are arguments to a single one and there would be no way to say
which.
