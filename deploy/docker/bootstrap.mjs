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
// THE OPERATOR PORTAL'S CREDENTIAL HAS THE SAME SHAPE AND THE SAME GAP.
// `antifailure_admin` is created by the migrations, NOLOGIN and with no
// password, and nothing here gave it one either. AF_ADMIN_DATABASE_URL
// therefore named a role that could not be connected as, and the application
// refuses to START when the operator pool cannot connect: delivering that
// variable without step 5 below would not produce a broken portal, it would
// produce no control plane at all. It is optional, exactly as it is in the
// application, and unset means this installation has no operator portal.
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

// A password cannot be a bind parameter either: CREATE ROLE and ALTER ROLE are
// DDL and Postgres does not parameterise them. It is escaped as a standard
// string literal instead, by doubling single quotes, which is complete under
// standard_conforming_strings (on by default since 9.1). A password carrying a
// NUL byte is refused rather than truncated at it.
//
// One function rather than one expression per call site: two roles now get a
// password here, and two spellings of the same escape is one spelling nobody
// checks.
function passwordLiteral(password, where) {
  if (password.includes('\u0000')) {
    console.error(`the password in ${where} contains a NUL byte`)
    process.exit(2)
  }
  return `'${password.replaceAll("'", "''")}'`
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

// The operator portal's credential, and it is OPTIONAL here in the same way it
// is optional in the application.
//
// Unset means this installation has no operator portal, which is the right
// default for a single team running the control plane for themselves. Set, it
// is the connection string the portal will use, and step 5 below is what makes
// it usable: the migrations create `antifailure_admin` NOLOGIN with no
// password, so until something gives it a login the variable names a role that
// cannot be connected as at all.
const operatorUrl = process.env.AF_ADMIN_DATABASE_URL

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
    const literal = passwordLiteral(appPassword, 'AF_DATABASE_URL')
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
  // 5. The operator portal's login, when this installation has one.
  //
  // WHY THIS STEP EXISTS AT ALL. `antifailure_admin` is created by the
  // migrations -- 0023 creates it and 0031 recreates it if a database somehow
  // reaches that file without it -- as NOLOGIN with no password, exactly the
  // way 0001 creates `antifailure_app`. 0031 says why in as many words: NOLOGIN,
  // so a password has to be set deliberately by whoever operates the
  // installation. Nothing in this repository was that "something", so
  // AF_ADMIN_DATABASE_URL named a role no client could authenticate as, and
  // the control plane refuses to START when the operator pool cannot connect.
  // Delivering the variable without this step would take the whole plane down.
  //
  // THE THREE THINGS IT PROVES BEFORE IT WRITES ANYTHING, because each one is a
  // failure that otherwise surfaces as an operator portal full of empty states:
  //
  //   the role EXISTS. Absent means the migrations did not run, and creating it
  //   here would produce a role with none of the grants 0029, 0030 and 0031
  //   hand it -- a credential that connects and reads nothing.
  //
  //   it holds BYPASSRLS. This is the attribute, not a grant, and it is the
  //   only mechanism that reads across tenants: every table carries FORCE ROW
  //   LEVEL SECURITY, so a role without it reads exactly zero rows through
  //   policies that are working perfectly. That is the misconfiguration which
  //   looks most like working software, which is why it is refused here as well
  //   as by createAdminPool's ensureBypass at start-up.
  //
  //   it has the PRIVILEGES of antifailure_admin. BYPASSRLS exempts a role from
  //   policies and grants it nothing; a role holding the attribute and no
  //   GRANTs still cannot SELECT. pg_has_role answers this for the group role
  //   itself and for any login role somebody made a member of it.
  //
  // WHY THE PASSWORD IS SET ON EVERY RUN, unlike the application role above.
  // That role is left alone when it exists because an operator may have created
  // it themselves and resetting a running system's credential is worse than
  // refusing to. Nothing else ever gives THIS role a password: the migrations
  // deliberately do not, so the vault the URL comes from is the only source of
  // truth there is, and converging on it is the behaviour that makes rotating
  // the secret and running this job a complete rotation.
  if (operatorUrl) {
    const operator = new URL(operatorUrl)
    const operatorRole = identifier(
      decodeURIComponent(operator.username),
      'the username in AF_ADMIN_DATABASE_URL',
    )
    const operatorPassword = decodeURIComponent(operator.password || '')
    if (!operatorPassword) {
      console.error('AF_ADMIN_DATABASE_URL carries no password, so the operator role cannot be given a login.')
      process.exit(2)
    }
    if (operatorRole === appRole) {
      // The one mistake worth naming: pointing the operator URL at the
      // application's own credential. Every operator page would then be subject
      // to the tenant policies and render its empty state, which reads as a
      // platform with no customers on it.
      console.error(
        `AF_ADMIN_DATABASE_URL names ${operatorRole}, which is the application's own role. ` +
          'The operator portal needs a separate credential.',
      )
      process.exit(2)
    }

    const [role] = await sql`
      SELECT rolbypassrls AS bypass FROM pg_roles WHERE rolname = ${operatorRole}
    `
    if (!role) {
      console.error(
        `role ${operatorRole} does not exist. The migrations create antifailure_admin with the ` +
          'grants the operator portal needs; creating it here would produce a credential that ' +
          'connects and reads nothing.',
      )
      process.exit(2)
    }
    if (!role.bypass) {
      console.error(
        `role ${operatorRole} does not hold BYPASSRLS, so every operator page would read zero ` +
          'rows through the tenant policies and show an empty state. Run ALTER ROLE ' +
          `${operatorRole} BYPASSRLS as a role that may set it, or point AF_ADMIN_DATABASE_URL ` +
          'at antifailure_admin.',
      )
      process.exit(2)
    }
    const [privileged] = await sql`
      SELECT pg_has_role(${operatorRole}, 'antifailure_admin', 'USAGE') AS ok
    `
    if (!privileged?.ok) {
      console.error(
        `role ${operatorRole} does not have the privileges of antifailure_admin. BYPASSRLS ` +
          'exempts a role from row level security and grants it nothing, so this credential ' +
          `would connect and still be refused every SELECT. GRANT antifailure_admin TO ${operatorRole}.`,
      )
      process.exit(2)
    }

    const literal = passwordLiteral(operatorPassword, 'AF_ADMIN_DATABASE_URL')
    await sql.unsafe(`ALTER ROLE ${operatorRole} LOGIN PASSWORD ${literal}`)
    const operatorDatabase = decodeURIComponent(operator.pathname.replace(/^\//, ''))
    if (operatorDatabase) {
      await sql.unsafe(
        `GRANT CONNECT ON DATABASE "${operatorDatabase.replace(/"/g, '""')}" TO ${operatorRole}`,
      )
    }
    console.log(`operator role ${operatorRole} can log in and holds BYPASSRLS`)
  } else {
    console.log('AF_ADMIN_DATABASE_URL is not set: this installation has no operator portal')
  }

  console.log('bootstrap complete')
} finally {
  await sql.end({ timeout: 10 })
}
