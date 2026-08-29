import { Pool } from "pg";

let pool: Pool | undefined;

/**
 * The connection pool, created on first use rather than when this module is
 * imported.
 *
 * The distinction matters more here than it looks. `next build` imports every
 * module it can reach, and the build runs inside the image, where there is no
 * database and no DATABASE_URL. A pool constructed at import time turns that
 * into a build failure that reads like a configuration problem. Creating it on
 * the first request keeps the build a build and the connection a runtime
 * concern.
 */
export function db(): Pool {
  if (!pool) {
    const connectionString = process.env.DATABASE_URL;
    if (!connectionString) {
      throw new Error(
        "DATABASE_URL is not set. Antifailure injects it; outside an environment, export one.",
      );
    }
    pool = new Pool({ connectionString, max: 5 });
  }
  return pool;
}
