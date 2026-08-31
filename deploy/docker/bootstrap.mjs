// Bringing a control plane database up from nothing, in the one order that works.
//
// WHY THIS EXISTS, because it is not obvious and it cost an afternoon to find.
//
// Migration 0001 creates `antifailure_app` as `NOLOGIN`. It is a GROUP role
// that carries the grants; it is not an account. The process serving requests
// connects as some other role that is a MEMBER of it. Nothing in the
// repository performs that membership grant: not the migrations, which cannot
// know what the operator called their login role, and not the application,
// which connects as the unprivileged role and so could not grant itself
// anything even if it tried.
//
// The result, reproduced against a real Postgres before this file was written:
// a fresh install migrates cleanly, the server starts, /health returns 200, and
// every table is invisible to the role serving traffic. `SELECT ... FROM
// organizations` comes back "relation does not exist", because a role with no
// USAGE on the schema cannot see that the relation is there at all. The
// container looks healthy and cannot answer a single request.
//
// So the order is: migrate (which creates the group role), then ensure the
// login role, then grant it membership. Any other order fails, because you
// cannot grant membership in a role that does not exist yet.
//
// Run as a Job that completes, before the Deployment rolls. That is the shape
// both the Helm chart and the Terraform use, so there is one path rather than
// two that drift.

import postgres from 'postgres'
import { migrate } from '@antifailure/db'

function required(name, ...fallbacks) {
  for (const n of [name, ...fallbacks]) {
    const v = process.env[n]
    if (v) return v
  }
  const names = [name, ...fallbacks].join(' or ')
  console.error(`${names} is not set. The bootstrap needs one of them.`)
  process.exit(2)
}

// Identifiers cannot be parameterised, so the role name is validated against a
// strict pattern and rejected rather than escaped. A name that does not match
// stops the job; it does not get quoted and run anyway.
function identifier(name, where) {
  if (!/^[a-z_][a-z0-9_]{0,62}$/.test(name)) {
    console.error(
      `${where} is not a usable Postgres role name: ${JSON.stringify(name)}. ` +
        'Lower case letters, digits and underscores only, starting with a letter or underscore.',
    )
    process.exit(2)
  }
  return name
}

// Two names, and a fallback to the one the engine injects.
//
// A Kubernetes Job and a Terraform deployment both set the AF_ names, because
// there they are two different connection strings: an administrative one that
// may run DDL and create a role, and the unprivileged one the server will use.
// `af up` sets neither. It gives a migration command an elevated DATABASE_URL
// and gives the service an unprivileged DATABASE_URL, so inside a preview
// environment there is one name that means both things depending on which
// container is reading it.
//
// Falling back rather than failing is what lets the published image run under
// `af up` unchanged, which is the whole point of a preview being made of the
// real artifact. It is not a silent downgrade: if that URL cannot run DDL the
// migration fails on the first statement, which is louder and more specific
// than this file refusing up front.
const adminUrl = required('AF_MIGRATION_DATABASE_URL', 'DATABASE_URL')
const appUrl = required('AF_DATABASE_URL', 'DATABASE_URL')

// The login role and its password are taken from the URL the application will
// actually use, rather than configured a second time. Two places to write the
// same role name is two places for them to disagree.
const parsed = new URL(appUrl)
const appRole = identifier(decodeURIComponent(parsed.username), 'the username in AF_DATABASE_URL')
const appPassword = decodeURIComponent(parsed.password || '')
const appDatabase = decodeURIComponent(parsed.pathname.replace(/^\//, ''))

const sql = postgres(adminUrl, { max: 1, onnotice: () => {} })

try {
  // 1. Schema. Creates `antifailure_app` on the way through.
  const result = await migrate(sql, { log: (line) => console.log(line) })
  console.log(
    result.applied.length
      ? `applied ${result.applied.length} migrations`
      : 'schema is up to date',
  )

  // 2. The login role. Created only when absent: an install that already has
  // one keeps its password, because silently resetting the credential of a
  // running system is a worse failure than refusing to.
  const [existing] = await sql`SELECT 1 FROM pg_roles WHERE rolname = ${appRole}`
  if (existing) {
    console.log(`role ${appRole} already exists, leaving it alone`)
  } else {
    if (!appPassword) {
      console.error(
        `role ${appRole} does not exist and AF_DATABASE_URL carries no password, ` +
          'so it cannot be created. Create the role yourself, or put the password in the URL.',
      )
      process.exit(2)
    }
    // The password cannot be a bind parameter: CREATE ROLE is DDL and Postgres
    // does not parameterise it. It is escaped as a standard string literal
    // instead, by doubling single quotes, which is complete under
    // standard_conforming_strings (on by default since 9.1). A password
    // carrying a NUL byte is refused rather than truncated at it.
    if (appPassword.includes('\u0000')) {
      console.error('the password in AF_DATABASE_URL contains a NUL byte')
      process.exit(2)
    }
    const literal = `'${appPassword.replaceAll("'", "''")}'`
    // NOBYPASSRLS is the point of the whole arrangement and is stated
    // explicitly rather than relied on as a default.
    await sql.unsafe(
      `CREATE ROLE ${appRole} LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS ` +
        `PASSWORD ${literal}`,
    )
    console.log(`created role ${appRole}`)
  }

  // 3. Membership, which is the step nothing else does. Idempotent: granting a
  // membership that is already held is not an error in Postgres.
  await sql.unsafe(`GRANT antifailure_app TO ${appRole}`)
  if (appDatabase) {
    await sql.unsafe(`GRANT CONNECT ON DATABASE "${appDatabase.replace(/"/g, '""')}" TO ${appRole}`)
  }
  console.log(`granted antifailure_app to ${appRole}`)

  // 4. Proof, not assumption. The bootstrap asserts the end state it exists to
  // produce, so a run that silently achieved nothing fails here rather than
  // being discovered by the first request in production.
  const [check] = await sql`
    SELECT pg_has_role(${appRole}, 'antifailure_app', 'MEMBER') AS ok
  `
  if (!check?.ok) {
    console.error(`${appRole} is still not a member of antifailure_app. Refusing to report success.`)
    process.exit(1)
  }
  console.log('bootstrap complete')
} finally {
  await sql.end({ timeout: 10 })
}
