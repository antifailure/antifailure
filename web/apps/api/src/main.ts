#!/usr/bin/env node
// The control plane process.
//
// Reads its configuration from the environment, refuses to start without what
// it needs, and says which variable is missing. A server that starts with a
// missing secret and fails on the first request that needs it is a server that
// fails in production rather than at deploy time.

import { serve } from '@hono/node-server'
import { createPool, migrate } from '@antifailure/db'
import postgres from 'postgres'
import { createServer } from './server.ts'
import { RealGitHubClient } from './auth/github.ts'
import { systemClock } from './clock.ts'
import { sweepSessions } from './auth/session.ts'
import { parseAllowlist, describeAllowlist } from './auth/signin.ts'
import { sealingKeyFrom } from './providers/seal.ts'
import { retentionFromEnv, startMaintenance } from './maintenance.ts'

function required(name: string): string {
  const value = process.env[name]
  if (!value) {
    console.error(`${name} is not set. The control plane needs it to start.`)
    process.exit(2)
  }
  return value
}

const databaseUrl = required('AF_DATABASE_URL')
const port = Number(process.env.AF_PORT ?? 8080)

if (process.env.AF_MIGRATE === '1') {
  // Migrations run as a privileged role, deliberately not the one the
  // application connects as: a role that can ALTER TABLE can disable the
  // policies that isolate tenants.
  const adminUrl = required('AF_MIGRATION_DATABASE_URL')
  const admin = postgres(adminUrl, { max: 1, onnotice: () => {} })
  const result = await migrate(admin, { log: (line) => console.log(line) })
  await admin.end({ timeout: 10 })
  console.log(
    result.applied.length
      ? `applied ${result.applied.length} migrations`
      : 'schema is up to date',
  )
}

const pool = createPool({ url: databaseUrl, max: Number(process.env.AF_POOL_MAX ?? 10) })

const github = new RealGitHubClient({
  clientId: required('AF_GITHUB_CLIENT_ID'),
  clientSecret: required('AF_GITHUB_CLIENT_SECRET'),
  redirectUri: required('AF_GITHUB_REDIRECT_URI'),
})

// Said out loud at startup, every time. Whether an instance is open to the
// world is not something anybody should have to infer from a deployment
// template, and a closed instance that quietly opened is the failure that has
// no symptom until it has a very large one.
const signInAllowlist = parseAllowlist(process.env.AF_SIGNIN_ALLOWLIST)
console.log(describeAllowlist(signInAllowlist))

// Read at start-up rather than on first use, so a secret of the wrong length
// stops the process here instead of on the one request the feature exists for.
const sealingKey = sealingKeyFrom(process.env.AF_PROVIDER_KEY_SECRET)
console.log(
  sealingKey
    ? 'provider keys can be stored: AF_PROVIDER_KEY_SECRET is set'
    : 'provider keys CANNOT be stored: AF_PROVIDER_KEY_SECRET is not set',
)

const { app, ingestLimiter, authLimiter } = createServer({
  pool,
  github,
  clock: systemClock,
  secureCookies: process.env.AF_INSECURE_COOKIES !== '1',
  appBaseUrl: process.env.AF_APP_BASE_URL,
  signInAllowlist,
  sealingKey,
})

// Partitions, kept ahead of the writes. Skipped when this process is not the
// one that owns the schema, because it is DDL and needs the migration role.
// An installation that runs migrations from a separate job sets
// AF_MAINTENANCE_DATABASE_URL here instead.
const maintenanceUrl =
  process.env.AF_MAINTENANCE_DATABASE_URL ?? process.env.AF_MIGRATION_DATABASE_URL
if (maintenanceUrl) {
  startMaintenance(
    {
      adminUrl: maintenanceUrl,
      retentionMonths: retentionFromEnv(process.env),
      archiveDir: process.env.AF_EVENT_ARCHIVE_DIR,
      log: (line) => console.log(line),
    },
    systemClock,
  )
} else {
  console.warn(
    'no AF_MAINTENANCE_DATABASE_URL or AF_MIGRATION_DATABASE_URL: ' +
      'events partitions will not be kept ahead by this process',
  )
}

// Housekeeping, not enforcement: expiry is checked when a session is resolved,
// so a sweeper that is late costs table size and nothing else.
const housekeeping = setInterval(
  () => {
    void sweepSessions(pool, systemClock).catch((err) => console.error('session sweep', err))
    ingestLimiter.sweep()
    authLimiter.sweep()
  },
  5 * 60 * 1000,
)
housekeeping.unref()

const server = serve({ fetch: app.fetch, port }, (info) => {
  console.log(`control plane listening on :${info.port}`)
})

// Draining rather than dropping. A rolling deploy that kills connections
// mid-request turns every deploy into a handful of failed pull request checks.
for (const signal of ['SIGTERM', 'SIGINT'] as const) {
  process.on(signal, () => {
    console.log(`${signal}: draining`)
    server.close(() => {
      void pool.close().then(() => process.exit(0))
    })
  })
}
