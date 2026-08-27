---
title: DBLab
description: Using a self hosted Database Lab Engine as the database provider, how to stand one up, and what it does with your data.
sidebar:
  order: 3
---

A Database Lab Engine holds one full size copy of production on ZFS and hands
out thin clones of it. A clone is a copy on write snapshot plus a Postgres
container, so it takes about as long for a terabyte as for a megabyte. That is
the same property Neon has, with the difference that you run it, on your
hardware, and nothing leaves your network.

It sits between the two providers that already exist. Like `neon` it is an HTTP
API handing back connection strings to databases `af` does not run. Like
`docker` it needs no account and no bill.

## Configuration

```yaml
database:
  provider: dblab
  version: 17
  project: http://127.0.0.1:2345   # the engine's API root
  api_key_env: DBLAB_VERIFICATION_TOKEN
```

`project` is the engine's API root. The field is called `project` because that
is what the manifest schema calls "which instance of a hosted provider", and
for a self hosted engine the instance is a URL. It is not a secret and lives in
the manifest.

`api_key_env` names the variable holding the engine's verification token. It
defaults to `DBLAB_VERIFICATION_TOKEN`. The value is looked up through the same
chain as everything else: an exported variable, then `.env`, then the local
store.

A token is required, even though the engine itself will run without one. An
engine with no verification token is one that anybody who can reach the port
can create clones of production data on.

## How Antifailure uses the engine

| Antifailure | Database Lab Engine |
| --- | --- |
| A golden version | A snapshot whose commit message says Antifailure wrote it |
| `af-cand-<version>` | The clone a golden is built in, deleted once committed |
| `af-env-<environment>` | One environment's clone |

The engine's own data retrieval is what brings production in, on the schedule
its configuration sets. It arrives **unmasked**, which is the point: it is a
copy of production, and the engine is not a masking tool.

A refresh therefore does this, and the order is not negotiable:

1. Clone the newest snapshot the engine's retrieval produced.
2. Apply the masking rules to that clone.
3. Scan it, and stop if anything is found.
4. Commit the clone into a new snapshot, with a message recording the version,
   the rules hash and the attestation's digest.
5. Delete the clone.

Step 4 is the publish. Everything before it can fail and leave nothing
branchable, because a snapshot is the only thing `Branch` will use and a
candidate clone is never one.

### A refresh never starts from a golden

The base for a refresh is the newest snapshot **that Antifailure did not
create**. Cloning the newest snapshot of any kind would be wrong in a way that
is easy to miss: the second refresh would start from the first refresh's
golden, masking would run over already masked data, and every golden after the
first would be a descendant of one rather than an independent copy of
production.

If you want to pin the base, name a snapshot explicitly rather than relying on
recency.

### Branching a snapshot Antifailure did not verify is refused

This matters more here than on any other provider. A Database Lab Engine is
full of snapshots holding unmasked production, and they are named in plain
sight in the engine's own interface, where somebody can copy one. Naming one of
those as a golden fails with `AF-MSK-001`, not because the snapshot is missing,
but because nothing has masked or scanned it.

## Where the attestation lives

Inside the golden, in a table:

```sql
SELECT version, rules_hash, created_at, attestation
FROM _antifailure.golden;
```

In the database rather than beside it, because a verification statement is
about that data and should travel with it. A clone of a golden inherits the
row, so anyone holding an environment can read what was scanned and what was
found without asking the engine.

The snapshot's commit message carries a compact record of the same thing: the
version, the rules hash, and a SHA-256 of the attestation. It is stored as a
ZFS user property, which is bounded, so the attestation itself is not put
there.

## Connections

There is no pooler. A clone is a plain Postgres container with one published
port, so this provider does not declare pooled endpoints and everything gets
the direct string. That is not a limitation in practice: the reason to want a
pooled endpoint is a serverless compute that opens a connection per request,
and a Database Lab Engine is not that.

Two things about a clone's connection are worth knowing.

**The engine never gives a clone's password back.** It records the ephemeral
role's name, database and owner, and deliberately not its password, so reading
a clone answers with an empty one. Keeping the password in memory would work
until the process exited, and `af up` and `af test` are separate processes. So
the password is derived instead, as an HMAC of the engine's verification token
and the clone's identifier. It is stable across processes, unique per clone,
written nowhere, and grants nothing the token did not already grant.

**A loopback host is rewritten.** The engine reports a clone's host from its own
point of view, and its default configuration binds clones to `127.0.0.1`. An
engine on another machine therefore reports `127.0.0.1`, and connecting there
would reach your own machine. When the engine reports a loopback address or
none, the host from `project` is used, because that is by definition a host
that reaches the engine.

## The engine must be reachable from inside your services

A clone is not a container on the machine running `af`, so unlike the `docker`
provider there is nothing for the runtime to attach to an environment's
network. The connection string handed to a service is the same one `af` uses,
which means the host in `project` has to be a host that **a container in the
environment can resolve and reach**, not just one this machine can.

Practically: `project: http://dblab.internal:2345` or a LAN address is right.
`project: http://127.0.0.1:2345` is right for running the conformance suite and
for `af golden refresh`, both of which connect from the host process, and wrong
for `af up`, because inside a service container `127.0.0.1` is that container.

This provider does not refuse a loopback endpoint, because refusing would also
block the host side operations that legitimately work. Point it at a named host
before you run an environment against it.

## Idle clone deletion will delete your environment's database

The engine's `cloning.maxIdleMinutes` defaults to **120**. A clone with no
connections for two hours is deleted, and a clone is an environment's database.
An environment left up overnight comes back to a database that is gone.

Set it to `0` on any engine Antifailure points at:

```yaml
cloning:
  maxIdleMinutes: 0
```

Antifailure decides when an environment ends. Two systems with independent
opinions about that is one system too many.

## Standing one up

The engine requires **ZFS** (or LVM). That is not a preference; thin cloning is
the copy on write filesystem doing the work. It also requires a Postgres image
built to its contract, which is not the official one.

### Linux

Follow the project's own instructions. You need a ZFS pool, `/dev/zfs`, the
Docker socket, and a config file. The published images are `linux/amd64`, which
is what you want.

### macOS on Apple Silicon

Neither requirement is met out of the box, and both are solvable. The whole
thing runs locally and costs nothing but disk.

**ZFS.** macOS has no ZFS and Docker Desktop's LinuxKit kernel has no `zfs`
module (`modprobe zfs` reports the module is not in
`/lib/modules/6.10.14-linuxkit`). So the engine runs in a Linux VM. Colima is
what the project's own macOS guide uses:

```sh
brew install colima
colima start --profile dblab --cpu 4 --memory 8 --disk 60
```

Then create the pool inside it, using the script in the engine's repository,
which installs `zfsutils-linux`, makes a file backed pool and three datasets:

```sh
git clone https://gitlab.com/postgres-ai/database-lab.git
cd database-lab && git checkout v4.1.3
colima ssh --profile dblab < engine/scripts/init-zfs-colima.sh
```

:::caution[colima start takes the machine's default Docker context]
It switches `docker context` to `colima-<profile>` for every shell on the
machine, so anything else pointed at Docker Desktop silently starts talking to
an empty daemon. Put it back and address the VM explicitly instead:

```sh
docker context use desktop-linux
export DOCKER_HOST=unix://$HOME/.colima/dblab/docker.sock
```
:::

**Architecture.** Every `postgresai/*` image is `linux/amd64` only, and
emulating a Postgres cluster is slow enough to make the conformance suite time
out. Build both pieces natively instead.

The engine itself builds from source:

```sh
cd database-lab/engine
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o bin/dblab-server ./cmd/database-lab/main.go
docker build -t dblab_server:local-arm64 -f Dockerfile.dblab-server .
```

The Postgres image needs replacing too, and the reason is specific. The engine
starts a clone container with **no command override**, passing `PGDATA`,
`PG_UNIX_SOCKET_DIR` and `PG_SERVER_PORT`, and then waits for Postgres to
answer, so the image's own command has to start Postgres. It also creates a
short lived container to inspect the image, giving it none of those variables
and an empty data directory, and runs `initdb` and `pg_ctl` inside it by hand,
so the container has to stay alive when Postgres cannot start.

The official `postgres:17` image satisfies neither: its command is `postgres`,
which makes its entrypoint run `initdb` itself and race the engine's. The
failure is an exec that dies with exit code 137 while `initdb` is choosing
`max_connections`, which reads like memory pressure and is not.

`postgresai/extended-postgres` handles both with a three line command. This is
the same shape, on the official image:

```dockerfile
FROM postgres:17
COPY pg_start.sh /pg_start.sh
RUN chmod +x /pg_start.sh
CMD ["/pg_start.sh"]
```

```sh
#!/bin/bash
chown -R postgres:postgres ${PGDATA} ${PG_UNIX_SOCKET_DIR} 2>/dev/null
su postgres -s /bin/bash -c "/usr/lib/postgresql/${PG_MAJOR}/bin/postgres -D ${PGDATA} -k ${PG_UNIX_SOCKET_DIR} -p ${PG_SERVER_PORT}" >& /proc/1/fd/1
/bin/bash -c "trap : TERM INT; sleep infinity & wait"
```

Postgres runs in the foreground for a clone; when it cannot start, the third
line keeps the container alive for the engine to drive.

What you lose relative to `extended-postgres` is its extra extensions. Set
`shared_preload_libraries` to what the official image actually has, or the
engine will start a Postgres that immediately exits:

```yaml
databaseConfigs: &db_configs
  configs:
    shared_preload_libraries: "pg_stat_statements"
```

**The verification token is not read from the environment.** The shipped
example config writes `verificationToken: "${DBLAB_VERIFICATION_TOKEN}"` and
the engine does not expand it; it authenticates against that literal string and
every request fails with `UNAUTHORIZED`. Put the value in the file, and keep
the file outside any repository.

**Then run it**, with the config directory mounted and the pool bind mounted
shared:

```sh
docker run -d --name dblab_server --privileged --device /dev/zfs \
  -v /tmp:/tmp \
  -v /var/lib/dblab/dblab_pool:/var/lib/dblab/dblab_pool:rshared \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "$HOME/dblab/configs:/home/dblab/configs:rw" \
  -v "$HOME/dblab/meta:/home/dblab/meta" \
  -p 2345:2345 \
  dblab_server:local-arm64
```

The first start runs the whole retrieval: dump the source, restore it into the
pool, snapshot it. Watch `docker logs -f dblab_server`. Until it finishes there
is nothing to build a golden from, and a refresh fails with `AF-DB-009` saying
so rather than with a decoding error.

## Limits

The engine imposes no clone ceiling of its own. What runs out is the configured
port range (`provision.portPool`, a hundred ports by default) and the pool's
free space. Set `max_branches` in the manifest if you want a lower number
refused early with `AF-DB-006` rather than a clone that fails to start.

## Cleaning up after a killed run

Environments and goldens are removed by `af down` and `af golden gc`, and
`af env prune --older-than 24h` does the first in bulk.

Candidates are the one thing removed without being asked. A candidate exists
for the minutes between cloning the base and committing it, and nothing ever
branches from one, so a candidate older than two hours can only be the remains
of a process that died. The next refresh removes it.

If a run was killed in a way that left an environment clone behind, it is still
named `af-env-<environment>`, so `af env list` and `af down` reach it.

## Conformance

This provider passes the shared database conformance suite against a real
Database Lab Engine, not a fake. Because the engine is self hosted, you can run
that yourself:

```sh
export AF_DBLAB_URL=http://127.0.0.1:2345
export AF_DBLAB_TOKEN=...
go test ./engine/internal/db/dblab -run TestConformance -v -timeout 40m
```

It creates and deletes clones and snapshots on that engine and asserts at the
end that it left nothing behind. Without those two variables it skips, and says
which are missing, so a run that was meant to include it does not look like a
run that passed.

Point it at an engine that holds nothing you care about. Everything it creates
is named `af-`, and it ignores clones and snapshots that are not, but an engine
shared with somebody's real work is one where a leak report eventually gets
ignored.
