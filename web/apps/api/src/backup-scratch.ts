#!/usr/bin/env node
// The database the drill is run against when there is no production one.
//
// The drill's real subject is the control plane's own database, and on a
// continuous integration runner there is no such thing. Running it against an
// empty Postgres would be worse than not running it: the restore would be
// clean, every comparison would pass over nothing, and the cross-tenant read
// would have no other tenant to be refused. A green run that examined nothing
// is the failure this repository keeps finding.
//
// So this makes a database the drill can say something about: the real schema,
// applied by the real migration runner, with two organizations that each own
// rows. Two rather than one, because one tenant cannot be isolated from
// anybody, and an isolation check with a single tenant refuses nothing and
// passes forever.
//
// It refuses a database that is not empty. That refusal is the whole of its
// safety: this thing writes fabricated organizations, and the way it can hurt
// somebody is by being pointed at a database that already holds real ones. It
// does not ask whether the name looks like a scratch database, because a name
// is a claim; it asks the database what it contains.

import { randomUUID } from 'node:crypto'
import { pathToFileURL } from 'node:url'
import postgres from 'postgres'
import { migrate } from '@antifailure/db'
import { APP_ROLE } from './backup.ts'

function usage(): never {
  console.error(`af-control-plane-scratch --url <admin connection string>
                          [--app-password <password>] [--orgs <n>]

Applies every migration to an EMPTY database and seeds organizations that own
rows, so that a drill run against it has two tenants to keep apart.

It refuses a database with anything in its public schema. Exit codes: 0 ready,
1 the work failed, 2 the arguments are wrong or the database is not empty.`)
  process.exit(2)
}

function flag(argv: string[], name: string): string | undefined {
  const i = argv.indexOf(`--${name}`)
  if (i === -1) return undefined
  const value = argv[i + 1]
  if (value === undefined || value.startsWith('--')) {
    console.error(`--${name} needs a value.`)
    process.exit(2)
  }
  return value
}

/**
 * Applies the schema and seeds tenants into an empty database.
 *
 * Exported and separate from the argument handling so the suite can run the
 * same code the workflow runs. A seeder that only exists inside a command is a
 * seeder nothing can test, and this one decides whether the drill has anything
 * to look at.
 */
export async function prepareScratch(
  adminUrl: string,
  options: { appPassword?: string; orgs?: number; log?: (line: string) => void } = {},
): Promise<{ orgIds: string[] }> {
  const log = options.log ?? (() => {})
  const wanted = options.orgs ?? 2
  if (wanted < 2) {
    throw new Error(
      `a scratch database needs at least two organizations, not ${wanted}. One tenant cannot ` +
        `be isolated from anybody, and the drill's cross-tenant read would refuse nothing.`,
    )
  }

  const sql = postgres(adminUrl, { max: 1, connect_timeout: 30, onnotice: () => {} })
  try {
    const occupied = await sql<{ name: string }[]>`
      SELECT c.relname AS name
        FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
       WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')
       ORDER BY c.relname LIMIT 5`
    if (occupied.length > 0) {
      throw new Error(
        `this database already holds ${occupied.map((r) => r.name).join(', ')} and possibly ` +
          `more. Seeding fabricated organizations into a database that already has some is ` +
          `not a drill, it is corruption. Point this at an empty database.`,
      )
    }

    const result = await migrate(sql)
    log(`applied ${result.applied.length} migrations`)

    // The role exists after 0001 and cannot log in, which is right for a
    // control plane whose application role is handed a password by the
    // deployment. The drill has to be able to connect as it, so the scratch
    // database gives it one.
    if (options.appPassword) {
      await sql.unsafe(
        `ALTER ROLE ${APP_ROLE} LOGIN PASSWORD '${options.appPassword.replace(/'/g, "''")}'`,
      )
    }

    const orgIds: string[] = []
    for (let i = 0; i < wanted; i++) {
      const slug = `drill-${i + 1}-${randomUUID().slice(0, 8)}`
      const [org] = await sql<{ id: string }[]>`
        INSERT INTO organizations (slug, name, github_login)
        VALUES (${slug}, ${slug}, ${slug}) RETURNING id`
      const orgId = org!.id
      const [repo] = await sql<{ id: string }[]>`
        INSERT INTO repositories (org_id, full_name) VALUES (${orgId}, ${`${slug}/app`})
        RETURNING id`
      await sql`
        INSERT INTO environments (org_id, repository_id, env_id, branch, state)
        VALUES (${orgId}, ${repo!.id}, ${`env-${slug}`}, 'main', 'running')`
      // An audit entry as well, so the manifest records a chain head per
      // organization and the restore has something to re-derive. Without one
      // the audit comparison is a loop over an empty object, which passes.
      await sql`
        INSERT INTO audit_entries (org_id, actor_label, action, target_type, origin, entry_hash)
        VALUES (${orgId}, 'drill', 'environment.created', 'environment', 'web',
                ${`scratch-${slug}`})`
      orgIds.push(orgId)
    }
    log(`seeded ${orgIds.length} organizations, each owning rows`)
    return { orgIds }
  } finally {
    await sql.end({ timeout: 5 })
  }
}

// Run only as a command. Imported by the suite, which calls prepareScratch
// directly against a database it made itself.
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const argv = process.argv.slice(2)
  const url = flag(argv, 'url')
  if (!url) usage()
  const orgs = flag(argv, 'orgs')
  try {
    const { orgIds } = await prepareScratch(url, {
      appPassword: flag(argv, 'app-password'),
      orgs: orgs === undefined ? undefined : Number(orgs),
      log: (line) => console.log(line),
    })
    console.log(`ready: ${orgIds.join(' ')}`)
  } catch (err) {
    console.error(err instanceof Error ? err.message : String(err))
    process.exit(2)
  }
}
