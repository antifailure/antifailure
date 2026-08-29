// Backup and restore for the control plane database, and the drill that proves
// the backup is one.
//
// A backup nobody has restored is a file. Every part of this module exists
// because of a way a restore can appear to succeed and leave the control plane
// broken, and each of those ways is checked rather than assumed.
//
// The dangerous ones, in the order they bite:
//
// Roles are not in the dump. pg_dump works on one database; roles live in the
// cluster. Restore into a fresh cluster in a second region and antifailure_app
// does not exist, so every GRANT in the dump fails and the application cannot
// connect to the database it just restored. That is the whole failover, lost to
// something invisible in the backup file. So the roles are dumped separately
// and the restore creates them.
//
// Row level security can survive as text and not as behaviour. The control
// plane isolates tenants with policies rather than with a WHERE clause, so a
// restore that loses ENABLE ROW LEVEL SECURITY, or loses FORCE, or restores the
// policies but not the grants, produces a database that answers every query and
// isolates nothing. Nothing about it looks wrong. So the manifest records every
// policy, every table with row level security enabled and forced, and every
// grant, and the restore refuses unless all three match.
//
// A checked restore is still not a proof. The last check is behavioural: the
// restored database is asked, through the unprivileged application role, to
// read another tenant's rows, and it has to refuse. Structure can match while
// behaviour does not, and behaviour is the thing the customer has.
//
// The audit log is a hash chain, so the head hash is recorded and re-derived
// after the restore. A restore that silently dropped or reordered entries
// changes the head, which is the one tamper-evident thing in the system and is
// free to check here.
//
// Nothing in here is clever. It is pg_dump, pg_dumpall and pg_restore, driven
// so that the drill can be run on a schedule and its result believed.

import { execFile } from 'node:child_process'
import { createHash } from 'node:crypto'
import { createReadStream } from 'node:fs'
import { mkdir, readdir, readFile, stat, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { promisify } from 'node:util'
import postgres from 'postgres'

const run = promisify(execFile)

/** The role the application connects as. Grants and policies are checked for
 *  this role specifically, because it is the one whose access is constrained. */
export const APP_ROLE = 'antifailure_app'

export interface BackupManifest {
  createdAt: string
  database: string
  serverVersion: string
  clientVersion: string
  schemaVersion: number
  /** Of the dump file, so a corrupt copy is caught before a restore, not
   *  during one. */
  sha256: string
  bytes: number
  /** Table to row count. A restore with a table missing or short is a restore
   *  that did not happen, whatever pg_restore's exit code said. */
  rowCounts: Record<string, number>
  /** Table to policy names. Sorted, so the comparison does not depend on the
   *  order the catalogue happens to return. */
  policies: Record<string, string[]>
  /** Tables with row level security enabled, and separately, forced. Both
   *  matter: without FORCE the table owner bypasses every policy, and a restore
   *  that makes the application role the owner would then isolate nothing. */
  rlsEnabled: string[]
  rlsForced: string[]
  /** Table to the privileges the application role holds. */
  grants: Record<string, string[]>
  /** Organization to audit chain head hash. */
  auditHeads: Record<string, string>
  /** Tables this verification did NOT look at, as `schema.table`.
   *
   *  Everything above is scoped to the `public` schema, which is where all 21
   *  of the control plane's tables live today. That scope is an assumption,
   *  and an assumption that silently stops being true is how a verification
   *  ends up checking a subset of the database and reporting success. So the
   *  ones outside it are counted rather than ignored, and `compareRestored`
   *  reports them as a problem: not because they failed to restore, but
   *  because nobody can say whether they did. */
  unverifiedTables: string[]
}

export interface BackupOptions {
  /** A role that can read every table. Not the application role. */
  adminUrl: string
  /** Where the dump, the roles file and the manifest are written. */
  outDir: string
  /** Directory holding pg_dump and pg_dumpall, when they are not on PATH. */
  binDir?: string
  /** Stamped into the file names. Injected so a drill is reproducible. */
  label?: string
}

export interface BackupResult {
  dumpPath: string
  rolesPath: string
  manifestPath: string
  manifest: BackupManifest
  seconds: number
}

function bin(name: string, dir?: string): string {
  return dir ? path.join(dir, name) : name
}

/**
 * Finds a client at least as new as the server, when the one on PATH is not.
 *
 * This is not a convenience. Debian and Ubuntu install every Postgres client
 * under /usr/lib/postgresql/<major>/bin and put exactly one of them on PATH,
 * which is routinely not the one matching the server you are backing up: a
 * GitHub runner ships pg_dump 16 while the Postgres it starts for you is 17.
 * The failure that produces without this is a refusal at three in the morning
 * on a box where the right binary is installed and forty characters away.
 *
 * It searches downward from the newest, and returns the first directory holding
 * a client at least the server's major. Nothing is executed to find it; the
 * version is read from the directory name, and the caller verifies it properly.
 */
async function findClientFor(serverMajor: number): Promise<string | undefined> {
  const root = '/usr/lib/postgresql'
  let majors: number[]
  try {
    majors = (await readdir(root))
      .map((name) => Number(name))
      .filter((n) => Number.isInteger(n) && n >= serverMajor)
      .sort((a, b) => b - a)
  } catch {
    return undefined
  }
  for (const major of majors) {
    const dir = path.join(root, String(major), 'bin')
    try {
      await stat(path.join(dir, 'pg_dump'))
      return dir
    } catch {
      // The directory exists and the binary does not. Keep looking.
    }
  }
  return undefined
}

/**
 * Settles on a bin directory holding a client new enough for this server.
 *
 * Returns the directory to use, or undefined to mean "PATH is fine". Throws
 * when nothing new enough exists anywhere, because continuing would mean
 * dumping a newer server with an older client, which is unsupported and does
 * not always fail loudly: it can produce a dump missing objects it did not know
 * to look for, and a backup missing objects is worse than no backup, because
 * you will not find out until the restore.
 */
async function resolveBinDir(explicit: string | undefined, serverMajor: number): Promise<string | undefined> {
  if (explicit) return explicit

  const onPath = majorOf(await toolVersion('pg_dump').catch(() => '0'))
  if (onPath >= serverMajor) return undefined

  const found = await findClientFor(serverMajor)
  if (found) return found

  throw new Error(
    `pg_dump on PATH is version ${onPath || 'unknown'} and the server is ${serverMajor}, and no ` +
      `client of at least ${serverMajor} was found under /usr/lib/postgresql. Dumping a newer ` +
      `server with an older client is unsupported and can silently omit objects. Install ` +
      `postgresql-client-${serverMajor}, or pass a binDir pointing at one.`,
  )
}

async function toolVersion(tool: string): Promise<string> {
  const { stdout } = await run(tool, ['--version'])
  return stdout.trim()
}

function majorOf(version: string): number {
  const m = version.match(/(\d+)/)
  return m ? Number(m[1]) : 0
}

/**
 * Refuses a client older than the server.
 *
 * pg_dump against a newer server is not supported and does not always fail
 * loudly: it can produce a dump that is missing objects it did not know to
 * look for. Newer client against an older server is supported and is the
 * normal case. This is the check an operator will be glad of at three in the
 * morning, when the only Postgres client on the box is whatever the base image
 * shipped.
 */
async function requireUsableClient(tool: string, sql: postgres.Sql): Promise<{
  client: string
  server: string
}> {
  const client = await toolVersion(tool)
  const rows = await sql<{ server_version: string }[]>`SHOW server_version`
  const version = rows[0]?.server_version ?? ''
  if (majorOf(client) < majorOf(version)) {
    throw new Error(
      `${tool} is version ${majorOf(client)} and the server is ${majorOf(version)}. ` +
        `Dumping a newer server with an older client is unsupported and can silently ` +
        `omit objects. Install a client of at least version ${majorOf(version)}, or set ` +
        `binDir to one.`,
    )
  }
  return { client, server: version }
}

async function sha256Of(file: string): Promise<string> {
  const hash = createHash('sha256')
  const stream = createReadStream(file)
  for await (const chunk of stream) hash.update(chunk as Buffer)
  return hash.digest('hex')
}

/** Everything the manifest records, read from a live database. */
export async function describe(sql: postgres.Sql): Promise<Omit<BackupManifest,
  'createdAt' | 'database' | 'serverVersion' | 'clientVersion' | 'sha256' | 'bytes'>> {
  const tables = await sql<{ name: string }[]>`
    SELECT c.relname AS name
      FROM pg_class c
      JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE n.nspname = 'public'
       -- 'r' is an ordinary table and 'p' is a partitioned one. A partition
       -- itself is 'r' with relispartition set, and counting both the parent
       -- and its partitions would double every row.
       AND c.relkind IN ('r', 'p')
       AND NOT c.relispartition
     ORDER BY c.relname`

  const rowCounts: Record<string, number> = {}
  for (const { name } of tables) {
    const rows = await sql<{ n: string }[]>`SELECT count(*)::text AS n FROM ${sql(name)}`
    rowCounts[name] = Number(rows[0]?.n ?? 0)
  }

  const policyRows = await sql<{ tablename: string; policyname: string }[]>`
    SELECT tablename, policyname FROM pg_policies WHERE schemaname = 'public'
     ORDER BY tablename, policyname`
  const policies: Record<string, string[]> = {}
  for (const r of policyRows) {
    ;(policies[r.tablename] ??= []).push(r.policyname)
  }

  const rls = await sql<{ name: string; enabled: boolean; forced: boolean }[]>`
    SELECT c.relname AS name, c.relrowsecurity AS enabled, c.relforcerowsecurity AS forced
      FROM pg_class c
      JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')
     ORDER BY c.relname`

  const grantRows = await sql<{ table_name: string; privilege_type: string }[]>`
    SELECT table_name, privilege_type
      FROM information_schema.role_table_grants
     WHERE grantee = ${APP_ROLE} AND table_schema = 'public'
     ORDER BY table_name, privilege_type`
  const grants: Record<string, string[]> = {}
  for (const r of grantRows) {
    ;(grants[r.table_name] ??= []).push(r.privilege_type)
  }

  const heads = await sql<{ org_id: string; entry_hash: string }[]>`
    SELECT DISTINCT ON (org_id) org_id, entry_hash
      FROM audit_entries ORDER BY org_id, seq DESC`
  const auditHeads: Record<string, string> = {}
  for (const h of heads) auditHeads[h.org_id] = h.entry_hash

  const applied = await sql<{ version: number }[]>`
    SELECT COALESCE(count(*), 0)::int AS version FROM schema_migrations`

  // Everything above reads the public schema. This asks what it did not read.
  const outside = await sql<{ name: string }[]>`
    SELECT n.nspname || '.' || c.relname AS name
      FROM pg_class c
      JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE c.relkind IN ('r', 'p')
       AND NOT c.relispartition
       AND n.nspname NOT IN ('public', 'pg_catalog', 'information_schema')
       AND n.nspname NOT LIKE 'pg_toast%'
       AND n.nspname NOT LIKE 'pg_temp%'
     ORDER BY 1`

  return {
    schemaVersion: Number(applied[0]?.version ?? 0),
    rowCounts,
    policies,
    rlsEnabled: rls.filter((r) => r.enabled).map((r) => r.name),
    rlsForced: rls.filter((r) => r.forced).map((r) => r.name),
    grants,
    auditHeads,
    unverifiedTables: outside.map((r) => r.name),
  }
}

/** Takes a backup and writes the manifest beside it. */
export async function backup(options: BackupOptions): Promise<BackupResult> {
  const started = process.hrtime.bigint()
  await mkdir(options.outDir, { recursive: true })

  const url = new URL(options.adminUrl)
  const database = url.pathname.replace(/^\//, '')
  const sql = postgres(options.adminUrl, { max: 1, onnotice: () => {} })

  try {
    const rows = await sql<{ server_version: string }[]>`SHOW server_version`
    const binDir = await resolveBinDir(options.binDir, majorOf(rows[0]?.server_version ?? ''))
    const versions = await requireUsableClient(bin('pg_dump', binDir), sql)
    const described = await describe(sql)

    const label = options.label ?? 'backup'
    const dumpPath = path.join(options.outDir, `${label}.dump`)
    const rolesPath = path.join(options.outDir, `${label}.roles.sql`)
    const manifestPath = path.join(options.outDir, `${label}.manifest.json`)

    // Custom format, so a restore can be parallel and selective. Owners are
    // dumped; the restore decides whether to apply them.
    await run(bin('pg_dump', binDir), [
      '--format=custom',
      '--no-password',
      `--file=${dumpPath}`,
      options.adminUrl,
    ])

    // Roles are cluster-level and are NOT in the dump above. Without this file
    // a restore into a fresh cluster has no antifailure_app, every GRANT fails,
    // and the application cannot connect to the database it just restored.
    const { stdout: roles } = await run(bin('pg_dumpall', binDir), [
      '--roles-only',
      '--no-password',
      `--dbname=${options.adminUrl}`,
    ])
    await writeFile(rolesPath, roles, 'utf8')

    const { size } = await stat(dumpPath)
    const manifest: BackupManifest = {
      createdAt: new Date().toISOString(),
      database,
      serverVersion: versions.server,
      clientVersion: versions.client,
      sha256: await sha256Of(dumpPath),
      bytes: size,
      ...described,
    }
    await writeFile(manifestPath, JSON.stringify(manifest, null, 2) + '\n', 'utf8')

    return {
      dumpPath,
      rolesPath,
      manifestPath,
      manifest,
      seconds: Number(process.hrtime.bigint() - started) / 1e9,
    }
  } finally {
    await sql.end({ timeout: 5 })
  }
}

export interface RestoreOptions {
  /** A superuser or role owner on the TARGET cluster, connected to a database
   *  that already exists. The target database is created through it. */
  adminUrl: string
  /** The database to create and restore into. Refused if it already exists,
   *  because a restore over a live database is not a recovery, it is an
   *  outage with a different cause. */
  targetDatabase: string
  dumpPath: string
  rolesPath?: string
  manifestPath?: string
  binDir?: string
  /** The password to give the application role on the target, so the restored
   *  database can actually be connected to. */
  appPassword?: string
}

export interface RestoreResult {
  seconds: number
  /** Empty when the restore is sound. Anything here means the database is up
   *  and must not be pointed at. */
  problems: string[]
  targetUrl: string
  /** The database this call created, and the only one a caller may destroy.
   *
   *  It exists so that dropping is authorised by evidence rather than by
   *  statement order. `restore` refuses a database that already exists, so
   *  anything reaching a caller's cleanup was made by this call; but that is a
   *  property of two functions being written in a particular sequence, and the
   *  first person to wrap the restore in a try/catch to make a drill "more
   *  robust" would delete a live database with no test going red. A caller
   *  that drops this field rather than the name it asked for cannot do that,
   *  because on the path where nothing was created there is no field. */
  created: string
}

function urlFor(base: string, database: string, user?: string, password?: string): string {
  const u = new URL(base)
  u.pathname = '/' + database
  if (user) u.username = user
  if (password) u.password = password
  return u.toString()
}

/**
 * Restores, then checks that what came back is the thing that went in.
 *
 * pg_restore exits zero over a great deal. It exits zero when a GRANT failed
 * because the role does not exist, and it exits zero when it restored the
 * policies onto a table whose row level security it could not enable. Both
 * produce a database that starts, answers, and does not isolate tenants. So the
 * exit code is not the result; the comparison against the manifest is.
 */
export async function restore(options: RestoreOptions): Promise<RestoreResult> {
  const started = process.hrtime.bigint()
  const problems: string[] = []

  const admin = postgres(options.adminUrl, { max: 1, onnotice: () => {} })
  let binDir: string | undefined
  try {
    const rows = await admin<{ server_version: string }[]>`SHOW server_version`
    binDir = await resolveBinDir(options.binDir, majorOf(rows[0]?.server_version ?? ''))
    await requireUsableClient(bin('pg_restore', binDir), admin)

    const exists = await admin<{ n: number }[]>`
      SELECT count(*)::int AS n FROM pg_database WHERE datname = ${options.targetDatabase}`
    if ((exists[0]?.n ?? 0) > 0) {
      throw new Error(
        `${options.targetDatabase} already exists. Restoring over a live database is not a ` +
          `recovery. Drop it deliberately, or restore into a new name and switch.`,
      )
    }

    // Roles first. They are cluster-level, so they have to exist before any
    // GRANT in the dump runs, and pg_dumpall's output is written to be applied
    // to a cluster that may already have some of them.
    if (options.rolesPath) {
      const script = await readFile(options.rolesPath, 'utf8')
      for (const statement of splitRoleStatements(script)) {
        try {
          await admin.unsafe(statement)
        } catch (err) {
          // A role that already exists is the ordinary case on a cluster that
          // has been restored into before. Anything else is reported.
          const message = err instanceof Error ? err.message : String(err)
          if (!/already exists/i.test(message)) {
            problems.push(`applying roles: ${message}`)
          }
        }
      }
    }
    if (options.appPassword) {
      await admin.unsafe(
        `ALTER ROLE ${APP_ROLE} LOGIN PASSWORD '${options.appPassword.replace(/'/g, "''")}'`,
      )
    }

    await admin.unsafe(`CREATE DATABASE "${options.targetDatabase.replace(/"/g, '""')}"`)
  } finally {
    await admin.end({ timeout: 5 })
  }

  const targetUrl = urlFor(options.adminUrl, options.targetDatabase)
  try {
    await run(bin('pg_restore', binDir), [
      '--no-password',
      '--exit-on-error',
      `--dbname=${targetUrl}`,
      options.dumpPath,
    ])
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err)
    problems.push(`pg_restore: ${message}`)
  }

  if (options.manifestPath) {
    const manifest: BackupManifest = JSON.parse(await readFile(options.manifestPath, 'utf8'))
    const restored = postgres(targetUrl, { max: 1, onnotice: () => {} })
    try {
      problems.push(...compareRestored(manifest, await describe(restored)))
    } finally {
      await restored.end({ timeout: 5 })
    }
  }

  return {
    seconds: Number(process.hrtime.bigint() - started) / 1e9,
    problems,
    targetUrl,
    // Only reachable past the CREATE DATABASE above, which is only reachable
    // past the refusal of a database that already exists.
    created: options.targetDatabase,
  }
}

/**
 * Splits pg_dumpall's roles output into statements.
 *
 * Naive on purpose: the file is generated rather than written, one statement
 * per line, and a parser that understood dollar quoting would be more code
 * standing between an operator and their data at the worst possible moment.
 */
function splitRoleStatements(script: string): string[] {
  return script
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line && !line.startsWith('--') && !line.startsWith('\\'))
    .filter((line) => /^(CREATE ROLE|ALTER ROLE|GRANT|COMMENT ON ROLE)/i.test(line))
}

/**
 * Every way the restored database can differ from what was backed up.
 *
 * Exported so the drill's own tests can break a restored database in each of
 * these ways and assert that this function notices. A comparison nobody has
 * seen fail is the same kind of thing as a backup nobody has restored.
 */
export function compareRestored(
  expected: BackupManifest,
  actual: Awaited<ReturnType<typeof describe>>,
): string[] {
  const problems: string[] = []

  if (actual.schemaVersion !== expected.schemaVersion) {
    problems.push(
      `schema version is ${actual.schemaVersion} and the backup was ${expected.schemaVersion}`,
    )
  }

  for (const [table, count] of Object.entries(expected.rowCounts)) {
    if (!(table in actual.rowCounts)) {
      problems.push(`table ${table} is missing`)
      continue
    }
    if (actual.rowCounts[table] !== count) {
      problems.push(`${table} has ${actual.rowCounts[table]} rows and the backup had ${count}`)
    }
  }

  // The three row level security checks, which are the ones that decide whether
  // this database isolates tenants or merely looks like it does.
  for (const table of expected.rlsEnabled) {
    if (!actual.rlsEnabled.includes(table)) {
      problems.push(
        `row level security is not enabled on ${table}, so every tenant can read every row`,
      )
    }
  }
  for (const table of expected.rlsForced) {
    if (!actual.rlsForced.includes(table)) {
      problems.push(
        `row level security is not FORCED on ${table}, so the table owner bypasses every policy`,
      )
    }
  }
  for (const [table, names] of Object.entries(expected.policies)) {
    const got = actual.policies[table] ?? []
    for (const name of names) {
      if (!got.includes(name)) {
        problems.push(`policy ${name} on ${table} did not come back`)
      }
    }
  }

  for (const [table, privileges] of Object.entries(expected.grants)) {
    const got = actual.grants[table] ?? []
    for (const privilege of privileges) {
      if (!got.includes(privilege)) {
        problems.push(
          `${APP_ROLE} has no ${privilege} on ${table}, so the application cannot use the ` +
            `database it just restored`,
        )
      }
    }
  }

  // Reported from both sides. A table outside public in the backup was never
  // verified; one that appears only after the restore was never verified
  // either, and is additionally a surprise.
  for (const name of expected.unverifiedTables) {
    problems.push(
      `${name} is outside the public schema, so this check never looked at it and ` +
        `cannot say whether it came back`,
    )
  }
  for (const name of actual.unverifiedTables) {
    if (!expected.unverifiedTables.includes(name)) {
      problems.push(
        `${name} is outside the public schema and was not in the backup, so the restored ` +
          `database has a table this check cannot account for`,
      )
    }
  }

  for (const [org, head] of Object.entries(expected.auditHeads)) {
    if (actual.auditHeads[org] !== head) {
      problems.push(
        `the audit chain head for ${org} is ${actual.auditHeads[org] ?? 'missing'} and the ` +
          `backup recorded ${head}, so entries were lost or reordered`,
      )
    }
  }

  return problems
}

export interface RehearsalOptions extends BackupOptions {
  /** The database the drill restores into, and then drops. */
  targetDatabase: string
  appPassword?: string
  /** Set false to leave the restored database standing, for a person to look
   *  at after a drill that found something. */
  drop?: boolean
  log?: (line: string) => void
}

export interface Rehearsal {
  backupSeconds: number
  restoreSeconds: number
  totalSeconds: number
  bytes: number
  problems: string[]
  /** The recovery time this drill actually measured, which is the number the
   *  runbook is allowed to quote. */
  recoveryTimeSeconds: number
}

/**
 * The drill: back up, restore into a throwaway database, check it, drop it,
 * and report how long the restore took.
 *
 * The reported recovery time is the RESTORE, not the whole run. Recovery starts
 * from a backup that already exists; including the time to take one would
 * flatter the number by measuring work that has already happened when it
 * matters.
 */
export async function rehearse(options: RehearsalOptions): Promise<Rehearsal> {
  const log = options.log ?? (() => {})

  log('taking a backup')
  const taken = await backup(options)
  log(`backed up ${taken.manifest.bytes} bytes in ${taken.seconds.toFixed(1)}s`)

  log(`restoring into ${options.targetDatabase}`)
  const restored = await restore({
    adminUrl: options.adminUrl,
    targetDatabase: options.targetDatabase,
    dumpPath: taken.dumpPath,
    rolesPath: taken.rolesPath,
    manifestPath: taken.manifestPath,
    binDir: options.binDir,
    appPassword: options.appPassword,
  })
  log(`restored in ${restored.seconds.toFixed(1)}s`)

  if (restored.problems.length === 0) {
    log('the restored database matches the backup')
  } else {
    for (const p of restored.problems) log(`PROBLEM: ${p}`)
  }

  if (options.drop !== false) {
    // restored.created, never options.targetDatabase. The two hold the same
    // string on every path that gets here, and that is the point: the one that
    // is only present when this run actually created a database is the one
    // that is safe to destroy. Asking for a name is not evidence of owning it.
    const admin = postgres(options.adminUrl, { max: 1, onnotice: () => {} })
    try {
      await admin.unsafe(
        `DROP DATABASE IF EXISTS "${restored.created.replace(/"/g, '""')}" WITH (FORCE)`,
      )
    } finally {
      await admin.end({ timeout: 5 })
    }
  }

  return {
    backupSeconds: taken.seconds,
    restoreSeconds: restored.seconds,
    totalSeconds: taken.seconds + restored.seconds,
    bytes: taken.manifest.bytes,
    problems: restored.problems,
    recoveryTimeSeconds: restored.seconds,
  }
}
