# added

A manifest could not say where its infrastructure as code lives.

Every section of the manifest described the copy: what to build, what to run,
what it may reach. Nothing described production. So the one thing a twin is
supposed to be measured against, the declaration of what production actually
is, was a file in the repository nothing in this product had an address for.

`infrastructure` is that address. It names the stacks that declare production,
one entry each, with the workspace and variable files that stack is read
through:

```yaml
infrastructure:
  stacks:
    - source: terraform
      path: infra/terraform
      workspace: production
      var_files:
        - infra/terraform/production.tfvars
```

Nothing in it changes what an environment builds or starts. Every key belongs
to one stack, because a workspace is selected inside a single root module and a
variable file is passed to a single invocation, and because a repository that
declares its cloud in one tool and its workloads in another is the ordinary
case rather than the exotic one.

`af init` drafts `source` and `path` by reading the tree. It names root
modules rather than every directory with a `.tf` file in it, because a module
a stack calls is a building block: naming it would point a comparison at a
library rather than at the thing built from it. It never drafts `workspace` or
`var_files`, and says so, because which workspace holds production and which of
`production.tfvars`, `staging.tfvars` and `dev.tfvars` describes it is stated
nowhere in a repository. This is the one section nothing downstream can check,
so a guess there would compare a copy against staging and report a number that
looked right.

Validation refuses a path that is absolute, that climbs out of the repository,
that two stacks name, that is not there, that is a file where a stack is a
directory, or that is a real directory holding no file the source can read.
Those refusals exist because nothing downstream would report any of them: a
directory with nothing in it declares no resources, and no resources is the
same answer an application with no infrastructure gives. The last one is the
one a deep typo actually lands on, because mistyping the final segment of a
long path usually names a directory that really is there.
