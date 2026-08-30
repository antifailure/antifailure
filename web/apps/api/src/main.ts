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
import { retentionFromEnv, startMaintenance } from './maintenance.ts'
import { ResendMailer } from './auth/mail.ts'
import { sweepEmailSignInTokens } from './auth/email.ts'
import type { EmailSignInConfig } from './auth/email.ts'

function required(name: string, ...fallbacks: string[]): string {
  for (const n of [name, ...fallbacks]) {
    const value = process.env[n]
    if (value) return value
  }
  const names = [name, ...fallbacks].join(' or ')
  console.error(`${names} is not set. The control plane needs one of them to start.`)
  process.exit(2)
}

// AF_DATABASE_URL is what a deployment sets. DATABASE_URL is what Antifailure
// injects into a service it runs, and inside a preview environment they are
// the same database, so either is accepted.
//
// This is not a convenience. The whole argument for a preview being made of
// the real artifact is that the artifact is unchanged, and an image that can
// only start under one orchestrator's variable names is an image a preview
// cannot run. Found by pointing af up at this control plane and watching the
// server refuse to start.
const databaseUrl = required('AF_DATABASE_URL', 'DATABASE_URL')
const port = Number(process.env.AF_PORT ?? 8080)

if (process.env.AF_MIGRATE === '1') {
  // Migrations run as a privileged role, deliberately not the one the
  // application connects as: a role that can ALTER TABLE can disable the
  // policies that isolate tenants.
  const adminUrl = required('AF_MIGRATION_DATABASE_URL', 'DATABASE_URL')
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

// Signing in with a link, off unless it is configured.
//
// Three variables and all three are needed, because two of them without the
// third is a link that goes nowhere or mail that cannot be sent. Naming which
// one is missing is the difference between a five-second fix and an afternoon.
function emailSignInFromEnv(): EmailSignInConfig | undefined {
  const apiKey = process.env.AF_RESEND_API_KEY
  const from = process.env.AF_MAIL_FROM
  // AF_PUBLIC_URL is what a deployment sets. AF_ENV_URL is what Antifailure
  // injects: the address of the environment's first web service, which is the
  // application a person opens.
  //
  // The distinction matters because the link is sent by this process and
  // landed on by another one. Inside a preview, this service's own address is
  // the API's, and a sign in link pointing there is a link that goes nowhere.
  // A run got as far as reading the mail and then failed on
  // ERR_CONNECTION_REFUSED four minutes in, because the only address the
  // application knew was its own container port.
  const publicUrl = process.env.AF_PUBLIC_URL ?? process.env.AF_ENV_URL
  if (!apiKey && !from && !publicUrl) return undefined

  const missing = [
    apiKey ? null : 'AF_RESEND_API_KEY',
    from ? null : 'AF_MAIL_FROM',
    publicUrl ? null : 'AF_PUBLIC_URL or AF_ENV_URL',
  ].filter((name): name is string => name !== null)
  if (missing.length > 0) {
    console.error(
      `signing in with a link is half configured: ${missing.join(', ')} not set. ` +
        'Set all three, or none of them to turn the path off.',
    )
    process.exit(2)
  }

  return {
    mailer: new ResendMailer({
      apiKey: apiKey!,
      from: from!,
      ...(process.env.AF_RESEND_BASE_URL ? { baseUrl: process.env.AF_RESEND_BASE_URL } : {}),
    }),
    baseUrl: publicUrl!,
    ...(process.env.AF_PRODUCT_NAME ? { productName: process.env.AF_PRODUCT_NAME } : {}),
  }
}

const emailSignIn = emailSignInFromEnv()

const { app, ingestLimiter, authLimiter } = createServer({
  pool,
  github,
  clock: systemClock,
  secureCookies: process.env.AF_INSECURE_COOKIES !== '1',
  appBaseUrl: process.env.AF_APP_BASE_URL ?? process.env.AF_ENV_URL,
  ...(emailSignIn ? { emailSignIn } : {}),
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
    if (emailSignIn) {
      void sweepEmailSignInTokens(pool, systemClock).catch((err) => console.error('link sweep', err))
    }
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
