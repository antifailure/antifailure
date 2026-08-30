#!/usr/bin/env node
// The identities a preview environment can be signed into as.
//
// Masking does its job: every address in a branched database is a synthetic
// one at example.test, which is reserved and can never receive mail. That is
// correct and it leaves nobody to sign in as, because an agent needs an
// address it knows before the environment exists.
//
// So this adds two, after the branch is made and before the application
// starts. It is the same thing a customer does with a seed step, and it is
// deliberately additive: it creates nothing that was not asked for, removes
// nothing, and changes no existing row. Running it twice is the same as
// running it once.
//
// It refuses to run against anything that is not a preview. The check is not a
// formality: this file creates an account that a known address can sign in as,
// and running it against production would be handing that address the
// organization.

import postgres from 'postgres'

// AF_DATABASE_URL is what a deployment sets. DATABASE_URL is what the engine
// injects into a migration command, and inside a preview those are the same
// database, so either is accepted and neither is required to be spelled twice
// in a manifest.
const url = process.env.AF_DATABASE_URL || process.env.DATABASE_URL
if (!url) {
  console.error('Neither AF_DATABASE_URL nor DATABASE_URL is set; there is nothing to seed.')
  process.exit(2)
}

// Two independent signals, and both have to say preview. AF_ENV_ID is set by
// the engine for an environment it created; AF_ALLOW_PERSONA_SEED is a
// deliberate opt-in for running this by hand against a local database.
if (!process.env.AF_ENV_ID && process.env.AF_ALLOW_PERSONA_SEED !== '1') {
  console.error(
    'This seeds accounts that a known address can sign in as, so it runs only inside an ' +
      'environment the engine created. Set AF_ALLOW_PERSONA_SEED=1 to run it by hand against ' +
      'a database you are sure about.',
  )
  process.exit(2)
}

const PERSONAS = [
  { email: 'owner@antifailure.test', login: 'af-owner', name: 'Preview Owner', role: 'owner' },
  { email: 'viewer@antifailure.test', login: 'af-viewer', name: 'Preview Viewer', role: 'viewer' },
]

const sql = postgres(url, { max: 2, onnotice: () => {} })

try {
  // The oldest organization, which is the one the rest of the fixture data
  // hangs off. Named by age rather than by slug because the slug is masked:
  // reading it here would mean reading a value this run cannot predict.
  const [org] = await sql`SELECT id, slug FROM organizations ORDER BY created_at ASC LIMIT 1`
  if (!org) {
    console.error(
      'There are no organizations in this database, so there is nothing to be a member of. ' +
        'The golden this branched from is empty.',
    )
    process.exit(1)
  }

  for (const persona of PERSONAS) {
    // A github_id well outside anything GitHub issues, so a real account can
    // never collide with one of these.
    const githubId = 9_000_000_000 + PERSONAS.indexOf(persona)

    const [user] = await sql`
      INSERT INTO users (github_id, github_login, email, name)
      VALUES (${githubId}, ${persona.login}, ${persona.email}, ${persona.name})
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

    console.log(`${persona.email} is ${persona.role} of ${org.slug}`)
  }
} catch (err) {
  console.error(err)
  process.exit(1)
} finally {
  await sql.end({ timeout: 5 })
}
