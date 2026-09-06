#!/usr/bin/env node
// The identities a preview environment can be signed into as.
//
// Masking does its job: every address in a branched database is a synthetic
// one at example.test, which is reserved and can never receive mail. That is
// correct and it leaves nobody to sign in as, because an agent needs an
// address it knows before the environment exists.
//
// So this creates them, one persona per run, as the seed command the manifest
// names under `auth.adapter: seed`. The engine runs it once per persona
// against the branch that has just been made, with the persona in the
// environment, and that contract is what decides which of two tables a
// persona goes in:
//
//   A persona that signs in by MAGIC LINK is a customer. The console has no
//   password sign-in at all, so it gets a row in `users` and a membership of
//   the oldest organization, and the runner reads its link out of the inbox.
//
//   A persona that signs in with a PASSWORD is an operator. The only password
//   form on this origin is the operator portal's, so it gets a row in
//   `admin_users` with the password the engine derived for it, hashed here
//   the way the API hashes one, and the runner types that password at /admin.
//
// WHY THE ENGINE HANDS THE PASSWORD IN. It used to run from the image's
// migrate step with a fixed list of two magic-link personas, which needed no
// password and could be written from a list. An operator needs one, the
// runner types the value the engine derived from the environment, and the
// only way for the hash written here and the password typed there to agree is
// for the same process to hand both out. AF_PERSONA_PASSWORD is that hand-off,
// which is what the seed adapter exists to provide and what a hard-coded list
// in a container could never receive.
//
// It is deliberately additive: it creates nothing that was not asked for,
// removes nothing, and changes no existing row except the one persona it was
// given. Running it twice is the same as running it once, and it exits non
// zero when it did not create the account, both of which the seed contract
// requires.
//
// It refuses to run against anything that is not a preview. The check is not a
// formality: this file creates an account that a known address can sign in
// as, and running it against production would be handing that address the
// organization, or worse, the operator portal.

import { createRequire } from 'node:module'
import { scrypt as scryptCb, randomBytes } from 'node:crypto'
import path from 'node:path'

// `postgres` is resolved from the working directory rather than from beside
// this file, because this file has no package of its own and is run from the
// repository root by the engine, where the dependency lives under web. Node's
// own resolution walks up from the importing FILE, which from deploy/docker
// reaches nothing.
function loadPostgres() {
  for (const dir of [path.join(process.cwd(), 'web'), process.cwd()]) {
    try {
      return createRequire(path.join(dir, 'personas.cjs'))('postgres')
    } catch (err) {
      if (err?.code !== 'MODULE_NOT_FOUND') throw err
    }
  }
  console.error(
    'The postgres package could not be found under web/node_modules or node_modules. ' +
      'Run `npm ci` in web first; the seed runs on the machine `af` runs on.',
  )
  process.exit(2)
}

// AF_DATABASE_URL is what a deployment sets. DATABASE_URL is what the engine
// injects into a seed command, and inside a preview those are the same
// database, so either is accepted and neither is required to be spelled twice
// in a manifest.
const url = process.env.AF_DATABASE_URL || process.env.DATABASE_URL
if (!url) {
  console.error('Neither AF_DATABASE_URL nor DATABASE_URL is set; there is nothing to seed.')
  process.exit(2)
}

// Two independent signals, and one of them has to say preview. AF_PERSONA_NAME
// is what the engine's seed adapter sets and nothing else does; a person who
// sets it by hand has read this file. AF_ALLOW_PERSONA_SEED is the deliberate
// opt-in for running this by hand against a local database.
const persona = {
  name: process.env.AF_PERSONA_NAME ?? '',
  email: (process.env.AF_PERSONA_EMAIL ?? '').trim().toLowerCase(),
  role: process.env.AF_PERSONA_ROLE ?? '',
  login: process.env.AF_PERSONA_LOGIN ?? '',
  password: process.env.AF_PERSONA_PASSWORD ?? '',
}
if (!persona.name && process.env.AF_ALLOW_PERSONA_SEED !== '1') {
  console.error(
    'This seeds accounts that a known address can sign in as, so it runs as the seed command ' +
      'of an environment the engine created, with the persona in AF_PERSONA_NAME and friends. ' +
      'Set AF_ALLOW_PERSONA_SEED=1 to run it by hand against a database you are sure about.',
  )
  process.exit(2)
}
if (!persona.name || !persona.email || !persona.role) {
  console.error('AF_PERSONA_NAME, AF_PERSONA_EMAIL and AF_PERSONA_ROLE are all required.')
  process.exit(2)
}

// The same parameters the API verifies with, in web/apps/api/src/admin/session.ts:
// N = 2^15, r and p at Node's defaults, a 64 byte key, a 32 byte salt stored
// beside it, and maxmem doubled past the exact budget because OpenSSL counts
// its own overhead against it. A hash written with any other parameters is a
// row the sign-in compares against and never matches, which reads as a wrong
// password rather than as a seed bug.
const SCRYPT_N = 32768
const SCRYPT_KEYLEN = 64
const SCRYPT_MAXMEM = 128 * SCRYPT_N * 8 * 2

function hashPassword(password) {
  const salt = randomBytes(32)
  return new Promise((resolve, reject) => {
    scryptCb(password, salt, SCRYPT_KEYLEN, { N: SCRYPT_N, maxmem: SCRYPT_MAXMEM }, (err, hash) =>
      err ? reject(err) : resolve({ hash, salt }),
    )
  })
}

// A github_id well outside anything GitHub issues, so a real account can never
// collide with one of these. Derived from the name so that a second run
// reconciles the row it made rather than adding another: `users.email` is
// indexed and not unique, and `github_id` is the only key the table has.
function syntheticGitHubId(name) {
  let h = 2166136261
  for (const c of Buffer.from(name, 'utf8')) {
    h ^= c
    h = Math.imul(h, 16777619) >>> 0
  }
  return 9_000_000_000 + (h % 1_000_000_000)
}

const postgres = loadPostgres()
const sql = postgres(url, { max: 2, onnotice: () => {} })

try {
  let id
  if (persona.login === 'password') {
    if (!persona.password) {
      console.error(`Persona ${persona.name} signs in with a password and none was handed in.`)
      process.exit(2)
    }
    // An operator. The role has to be one migration 0029 lists, and the
    // manifest says `owner`, which is the portal's own name for it. Never
    // root: the root operator is set once, at provisioning, by a person.
    const { hash, salt } = await hashPassword(persona.password)
    const now = new Date().toISOString()
    const [row] = await sql`
      INSERT INTO admin_users (email, name, role, password_hash, password_salt, password_set_at,
                               is_root, created_at, updated_at)
      VALUES (${persona.email}, ${'Preview ' + titled(persona.role)}, ${persona.role},
              ${hash}, ${salt}, ${now}, false, ${now}, ${now})
      ON CONFLICT (email) DO UPDATE SET
        name = EXCLUDED.name,
        role = EXCLUDED.role,
        password_hash = EXCLUDED.password_hash,
        password_salt = EXCLUDED.password_salt,
        password_set_at = EXCLUDED.password_set_at,
        suspended_at = NULL,
        suspended_reason = NULL,
        updated_at = EXCLUDED.updated_at
      RETURNING id`
    id = row.id
    console.log(`${persona.email} is an operator with the ${persona.role} role`)
  } else {
    // A customer of the oldest organization, which is the one the rest of the
    // fixture data hangs off. Named by age rather than by slug because the
    // slug is masked: reading it here would mean reading a value this run
    // cannot predict.
    const [org] = await sql`SELECT id, slug FROM organizations ORDER BY created_at ASC LIMIT 1`
    if (!org) {
      console.error(
        'There are no organizations in this database, so there is nothing to be a member of. ' +
          'The golden this branched from is empty.',
      )
      process.exit(1)
    }
    const [user] = await sql`
      INSERT INTO users (github_id, github_login, email, name)
      VALUES (${syntheticGitHubId(persona.name)}, ${'af-' + persona.name}, ${persona.email},
              ${'Preview ' + titled(persona.role)})
      ON CONFLICT (github_id) DO UPDATE SET
        email = EXCLUDED.email,
        github_login = EXCLUDED.github_login,
        name = EXCLUDED.name,
        updated_at = now()
      RETURNING id`
    await sql`
      INSERT INTO members (org_id, user_id, role, source)
      VALUES (${org.id}, ${user.id}, ${persona.role}, 'manual')
      ON CONFLICT (org_id, user_id) DO UPDATE SET role = EXCLUDED.role, updated_at = now()`
    id = user.id
    console.log(`${persona.email} is ${persona.role} of ${org.slug}`)
  }
  // The identifier, last, which the seed contract records.
  console.log(id)
} catch (err) {
  console.error(err)
  process.exit(1)
} finally {
  await sql.end({ timeout: 5 })
}

function titled(role) {
  return role.charAt(0).toUpperCase() + role.slice(1)
}
