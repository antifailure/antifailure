---
title: Next.js
description: What a Next.js service needs in an environment, and the four things that go wrong.
sidebar:
  order: 15
---

A Next.js application needs nothing Antifailure specific. It reads
`DATABASE_URL` from its environment like any other service, and the manifest
names the port and a health path:

```yaml
services:
  - name: web
    kind: web
    path: .
    port: 3000
    health_path: /api/health
    migrate: "psql $DATABASE_URL -v ON_ERROR_STOP=1 -f migrations/0001_init.sql"
```

The working version of everything below is
[`examples/next-app`](https://github.com/antifailure/antifailure/tree/main/examples/next-app),
and every one of these was found by running it rather than by reading it.

## The build must not need a database

`next build` runs inside the image, where there is no database and no
`DATABASE_URL`. Two habits from ordinary development break there.

A page that reads the database is rendered at build time unless it says
otherwise, and rendering it then means connecting to one:

```ts
export const dynamic = "force-dynamic";
```

A connection pool created at module scope is opened when the module is
imported, and `next build` imports every module it can reach. Create it on
first use instead:

```ts
let pool: Pool | undefined;

export function db(): Pool {
  if (!pool) pool = new Pool({ connectionString: process.env.DATABASE_URL });
  return pool;
}
```

Without either one the image fails to build, with a connection error that
reads like a configuration problem and is not one.

## Standalone output leaves the static files behind

`output: "standalone"` produces a server and a pruned `node_modules`, which is
what makes the runtime image worth scanning. It does not include the static
assets. They are a second copy:

```dockerfile
COPY --from=build /app/.next/standalone ./
COPY --from=build /app/.next/static ./.next/static
```

Leaving the second line out is the mistake everyone makes once. The page
renders, arrives with no CSS, and looks like a styling bug.

## Standalone picks its own root, and picks wrong in a monorepo

Next decides where to write `server.js` by walking up from the project looking
for lockfiles. In a repository that has its own above your application, it
picks a root several directories too high and writes
`.next/standalone/<the whole path back down>/server.js` rather than
`.next/standalone/server.js`.

Inside a Docker build the context is one directory, so the inference is right
and the Dockerfile works. On a laptop it is wrong. The artifact shape then
depends on where somebody cloned the repository, which is not a thing anyone
should have to know:

```ts
import path from "node:path";

const config: NextConfig = {
  output: "standalone",
  outputFileTracingRoot: path.join(__dirname),
};
```

## HOSTNAME decides whether anything can reach it

The standalone server binds to whatever `HOSTNAME` says. Left unset it has
bound to localhost in some versions, and inside a container localhost means
that container. The port is open and nothing outside can reach it, so the
service starts cleanly and never becomes ready:

```dockerfile
ENV HOSTNAME=0.0.0.0
```

## Give it a health path that touches the database

```ts
export async function GET() {
  try {
    await db().query("SELECT 1");
  } catch {
    return Response.json({ status: "database unreachable" }, { status: 503 });
  }
  return Response.json({ status: "ok" });
}
```

The engine waits for `health_path` before it calls the service ready, so this
is what makes `ready` mean the page will render. A health check that only
proves a process is listening reports ready and then serves a stack trace.

Related: [building services](/docs/guides/build/), [the local
runtime](/docs/guides/local-runtime/).
