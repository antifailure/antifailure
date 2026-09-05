# fixed

An image used only to execute isolated snippets was detected as a web server,
and a repository's Alembic migration was assigned to it even though the image
contained neither Alembic nor the application's schema. Initialization now
requires runtime evidence before treating a Dockerfile as a service. A
framework or Compose command can supply that evidence.
Images without an established runtime are named in both the summary and JSON
report, because a base image can inherit a command detection did not see.

Migration commands outside a unique service directory now require an explicit
image owner. An unattended run refuses to guess. Choosing manual setup reports
that migrations remain unconfigured; it does not claim the database is ready.
