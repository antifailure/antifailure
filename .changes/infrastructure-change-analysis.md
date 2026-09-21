# changed

A pull request that changed nothing but your infrastructure as code selected no
check, and was not rehearsed at all.

The analyser mapped the infrastructure surface to nothing, in the same line as
prose, your own test suite and your continuous integration configuration, under
one true sentence: the environment is built from `antifailure.yaml` rather than
from your Terraform. Read the fail safe this package is built on and the
grouping is the mistake. A path wrongly treated as inert skips work that should
have happened and nobody finds out; a path wrongly treated as unknown costs a
run nobody needed and is visible in the report. Prose, a test file and a
workflow file do not run in production. Your infrastructure as code does. It was
the one of the four that is not inert, and it sat in the bucket for the three
that are.

The consequence was not a quieter report. The published action gates the whole
run on `steps.change.outputs.environment`, so a change to production's database,
its capacity or its firewall wrote `environment=false` and ran nothing.

An infrastructure change now brings the environment up and drives the
application inside it, and the added lines are read for what they say. A
database engine version or a server parameter selects the migration rehearsal,
because the rehearsal is what applies your migrations to a Postgres and measures
what they lock. A replica count, an instance size or an autoscaling bound
selects load. A firewall or security group rule selects the egress decisions.
Each fact names the file, the line number and the rule behind it, the same as
every other conclusion this command draws.

The sharpest of them is new rather than renamed. When an added line sets a
database engine version and `database.version` in the manifest is a different
major, the report says which one the rehearsal will actually use, naming both
numbers. That pair lives in two files and nothing used to hold both, so a
repository could move production to Postgres 18 and go on proving its migrations
against 17 with every check green.

Read the claim precisely, because the report now repeats it on every run that
touches one of these files: nothing applies your infrastructure as code. The
environment stands in for the runtime the change describes, and standing in for
it is not being it.

Three limits, stated rather than discovered. A bare `version` key is not read as
a database version, because it is also how every provider pin and every chart
states its own, so an Azure Postgres version is in a key this cannot see. A URL
inside an infrastructure file is not read as an outbound host, because it is
usually a module source, which is a download the build makes rather than a call
the application makes. And nothing parses HCL: the added lines are read as text,
so Kubernetes manifests, Helm values, Bicep and CloudFormation get the same
reading rather than the one format a parser was written for.

The published action also exports an output per check now, rather than the two
it happened to name, so a job calling it directly can skip its own work on the
same answer the action already computed.

Two smaller things fell out of writing the tables down once. The surface table
on the concepts page is now compared against the table the analyser plans from,
rather than kept in step by hand, and the first thing that comparison found was
that `auth` had never been published at all: the surface existed, it selected
the environment and the workflows, and the page a customer reads did not list
it. And a check reads the example on that page back out of the binary, so the
worked output there is what `af change` actually prints rather than what it
printed when somebody pasted it.
