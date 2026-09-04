# Initialization must identify the process that owns a migration

The reported repository contains a Next.js dashboard, a Python dependency
project with Alembic, and a minimal Python image used by an isolated snippet
executor. The snippet image declares no command or port. Its caller supplies
the command when it creates a disposable container. Starting that image as a
web service invents a process; attaching root Alembic migrations to it invents
dependencies that were never installed.

Keep Docker build evidence available to framework and Compose detection, but
do not declare a service from a Dockerfile with no command or exposed port.
After evidence is folded by directory, omit candidates no analyzer declared
as a service. Compose or a framework may still identify the runtime.

Only transfer an orphan migration automatically when exactly one declared
service shares its directory. Otherwise ask which image contains the tool and
migration files. There is no default. An unattended caller must answer the
question explicitly. A manual choice must disclose missing migration setup.
Unknown owners are errors, and named owners must receive the command in the
written manifest.

This correction does not provide a dedicated migration image. The existing
runtime runs each migration in its service image before starting that service.
A complete automatic solution for a Python schema beside a Node dashboard
needs an independent migration build and execution contract across the local
and Kubernetes runtimes. The application's trading image is not an acceptable
substitute: its entrypoint runs trading logic and its build does not copy the
migration files. No trading or broker path belongs in an onboarding test.

Verification uses a minimal credential-free repository fixture, exercises the
real initialization command, and mutates each assertion's production path.

The ten regression behaviors all passed their isolated production mutations:
helper image exclusion, orphan owner question, no sort-order assignment,
Compose-supplied runtime, unique local owner, unattended refusal, written owner
command, manual disclosure, unknown owner refusal, and persistent manual note.
Twelve mutation cells covered those assertions, including separate declaration
and merge filters and separate migration-directory and transfer lines. Every
mutation failed the named test and passed again after restoration. The complete
detection and CLI packages passed uncached, focused lint reported zero issues,
and prosecheck reported zero violations.

Further source tracing found that the dashboard ignores DATABASE_URL. It reads
separate POSTGRES_HOST and POSTGRES_PASSWORD values, hardcodes its database and
user, and requires SSL. It also refuses every login when DASHBOARD_ACCESS_KEY
is absent. Therefore a listening HTTP port, or even successful Alembic, does
not establish that this application's onboarding is complete. Component
database configuration and real sign-in remain observable acceptance work.
