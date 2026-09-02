#!/usr/bin/env node
// The control plane process.
//
// Reads its configuration from the environment, refuses to start without what
// it needs, and says which variable is missing. A server that starts with a
// missing secret and fails on the first request that needs it is a server that
// fails in production rather than at deploy time.

import { serve } from '@hono/node-server'
import { createPool, createAdminPool, migrate } from '@antifailure/db'
import postgres from 'postgres'
import { createServer } from './server.ts'
import { RealGitHubClient } from './auth/github.ts'
import { systemClock } from './clock.ts'
import { sweepSessions } from './auth/session.ts'
import { sweepDeviceAuthorizations } from './auth/device.ts'
import { parseAllowlist, describeAllowlist } from './auth/signin.ts'
import { sealingKeyFrom } from './providers/seal.ts'
import { findConsoleBuild } from './console/static.ts'
import { appConfigFrom, InstallationTokens } from './github/app.ts'
import { RealRepositoryApi } from './github/api.ts'
import { sweepGenerations, sweepTeardowns, type LifecycleDeps } from './github/lifecycle.ts'
import { pricesFrom } from './providers/pricing.ts'
import { retentionFromEnv, startMaintenance } from './maintenance.ts'
import { ResendMailer } from './auth/mail.ts'
import { sweepEmailSignInTokens } from './auth/email.ts'
import { resumeDeletions } from './enterprise/deletion.ts'
import type { EmailSignInConfig } from './auth/email.ts'
import { RealStripeClient, stripeConfigFrom } from './billing/index.ts'
import { githubAppInstallUrlFrom, hostedRequiredPlanFrom } from './hosted.ts'

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

// The operator portal's own connection, when this installation has one.
//
// A SEPARATE variable and not a flag on the line above, because the whole
// boundary is that the cross tenant read needs a credential the application
// role cannot acquire. Deriving this URL from databaseUrl by swapping the user
// would mean the application holds everything needed to build it, which is the
// property being denied.
//
// Absent is a supported state and the right default for a single team running
// this themselves: there is no operator portal, and the admin procedures answer
// PRECONDITION_FAILED naming this variable rather than rendering an empty
// portal that looks like a platform with no customers.
const adminDatabaseUrl = process.env.AF_ADMIN_DATABASE_URL
const adminPool = adminDatabaseUrl
  ? createAdminPool({ url: adminDatabaseUrl, max: Number(process.env.AF_ADMIN_POOL_MAX ?? 4) })
  : null
if (adminPool) {
  // Proved at start up rather than on the first support request. A role without
  // BYPASSRLS reads zero rows through the tenant policies, so the failure this
  // catches would otherwise look exactly like a working portal with nothing in
  // it. Refusing to start is correct: an operator portal that silently shows
  // nothing is worse than one that is plainly not configured.
  await adminPool.ensureBypass()
  console.log('the operator portal has its own database credential')
} else {
  console.log('AF_ADMIN_DATABASE_URL is not set: this installation has no operator portal')
}

// The GitHub App, if there is one. Null is a supported state: sign-in works
// without it, and the parts that need an installation say which variables are
// missing rather than failing on the one request they exist for.
const appConfig = appConfigFrom(process.env)
const installationTokens = appConfig
  ? new InstallationTokens(appConfig, systemClock)
  : undefined
console.log(
  appConfig
    ? `GitHub App ${appConfig.appId} is configured: webhook deliveries are verified and membership can be synced`
    : 'no GitHub App: webhook deliveries are refused and membership cannot be synced (AF_GITHUB_APP_ID is not set)',
)

// Acting on a repository as the installation: check runs, the pull request
// comment, cancelling a run. Absent when there is no App, and the lifecycle
// then records what deliveries tell it and publishes nothing, which is what a
// self-hosted control plane with no App should do rather than refuse to start.
const githubApi = installationTokens
  ? new RealRepositoryApi({
      tokens: installationTokens,
      ...(process.env.AF_GITHUB_API_BASE ? { apiBase: process.env.AF_GITHUB_API_BASE } : {}),
    })
  : null

const github = new RealGitHubClient({
  clientId: required('AF_GITHUB_CLIENT_ID'),
  clientSecret: required('AF_GITHUB_CLIENT_SECRET'),
  redirectUri: required('AF_GITHUB_REDIRECT_URI'),
  installationTokens,
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

// Read at start-up so a malformed price stops the process here rather than on
// the first model call, which is the one request where being wrong costs money.
const modelPrices = pricesFrom(process.env.AF_MODEL_PRICES)
console.log(`model prices configured for ${Object.keys(modelPrices).length} models`)

// Billing, if this installation takes money. Read at start-up and said out
// loud, because "billing is off" and "billing is on" are the two states an
// operator most needs to be sure about, and a partially configured one is
// reported as OFF with the missing variables named rather than starting and
// failing on the first customer who pays.
const stripe = stripeConfigFrom(process.env)
console.log(stripe.summary)

let hostedRequiredPlan
let githubAppInstallUrl
try {
  hostedRequiredPlan = hostedRequiredPlanFrom(process.env.AF_HOSTED_REQUIRED_PLAN)
  githubAppInstallUrl = githubAppInstallUrlFrom(process.env.AF_GITHUB_APP_INSTALL_URL)
} catch (err) {
  console.error(err instanceof Error ? err.message : String(err))
  process.exit(2)
}
if (hostedRequiredPlan && !stripe.config) {
  console.error(
    'AF_HOSTED_REQUIRED_PLAN is set but billing is off. Configure all four Stripe variables so an organization can satisfy the gate.',
  )
  process.exit(2)
}
console.log(
  hostedRequiredPlan
    ? `hosted access requires the ${hostedRequiredPlan} plan`
    : 'no hosted plan gate: this installation serves every plan',
)

// Located once, here, and said out loud either way. A control plane running
// without its console is a legitimate way to run this; a control plane that
// silently answers 404 on every page because a COPY was dropped from a
// Dockerfile is not, and the two are indistinguishable without this line.
const consoleBuild = await findConsoleBuild()
console.log(consoleBuild.summary)

// Built once rather than at each call site. The deletion resumer needs the same
// client the routes use: a second one would be a second place for a key to be
// wrong, and a deletion that cancelled a subscription through a different
// client from the one the console used is a difference nobody would find.
const billing = stripe.config
  ? { config: stripe.config, client: new RealStripeClient(stripe.config) }
  : null

const { app, ingestLimiter, authLimiter } = createServer({
  pool,
  adminPool,
  github,
  clock: systemClock,
  secureCookies: process.env.AF_INSECURE_COOKIES !== '1',
  appBaseUrl: process.env.AF_APP_BASE_URL ?? process.env.AF_ENV_URL,
  signInAllowlist,
  sealingKey,
  githubWebhookSecret: appConfig?.webhookSecret ?? null,
  // The webhook's way of invalidating a cached token. Bound to the same
  // InstallationTokens the GitHub client mints from, because dropping a token
  // out of a different cache from the one that holds it is the shape of fix
  // that reads correct in a diff and changes nothing at runtime.
  ...(installationTokens
    ? { forgetInstallationToken: (id: number) => installationTokens.forget(id) }
    : {}),
  stripe: billing,
  hostedRequiredPlan,
  githubAppInstallUrl,
  modelPrices,
  consoleBuild,
  githubApi,
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

    // Beside the session sweep for the same reason and with the same cost:
    // expiry is checked on every read, so being late costs table size. It was
    // written with this comment on it and then never called from anywhere, so
    // device_authorizations grew for the life of the process.
    void sweepDeviceAuthorizations(pool, systemClock).catch((err) =>
      console.error('device authorization sweep', err),
    )

    // Not housekeeping. This one finishes work a customer asked for and is the
    // only thing that gets a deletion past the paid period it is waiting out,
    // which can be a month: nobody is coming back to press a button, and a
    // deletion that stops halfway is exactly the state somebody asked us not to
    // leave them in. It runs unconditionally, on the application pool, rather
    // than beside the partition maintenance, which only runs when an
    // administrative connection string happens to be configured.
    void resumeDeletions({
      pool,
      clock: systemClock,
      github,
      stripe: billing,
      log: (line, err) => console.error(line, err),
    }).catch((err) => console.error('organization deletion sweep', err))

    ingestLimiter.sweep()
    authLimiter.sweep()
  },
  5 * 60 * 1000,
)
housekeeping.unref()

// The pull request lifecycle's own housekeeping, and it is not the same shape
// as the sweeps above.
//
// Those are about table size: expiry is checked on every read, so being late
// costs nothing but rows. These two are about correctness. A check that never
// concludes holds a merge forever with no explanation, and a teardown that is
// never confirmed is somebody's containers still running on a machine they are
// paying for. Both had to be started HERE and not merely written, which is the
// failure this repository has shipped more than once: a sweeper with a comment
// saying what it keeps under control, and no caller.
if (githubApi) {
  const lifecycle: LifecycleDeps = {
    pool,
    clock: systemClock,
    api: githubApi,
    consoleBase: process.env.AF_APP_BASE_URL ?? process.env.AF_ENV_URL ?? null,
    // Names this replica in a lease, so a request stuck under one says which
    // process has it rather than only that somebody does.
    holder: process.env.HOSTNAME ?? 'control-plane',
  }
  // A minute. The teardown lease is a minute, so a slower interval would mean
  // a request whose holder died waits for the sweep rather than for the lease,
  // and the lease would be decorative.
  const lifecycleSweep = setInterval(
    () => {
      void sweepGenerations(lifecycle).catch((err) => console.error('generation sweep', err))
      void sweepTeardowns(lifecycle).catch((err) => console.error('teardown sweep', err))
    },
    60 * 1000,
  )
  lifecycleSweep.unref()
  console.log('the pull request lifecycle is running: checks, one comment per pull request, teardown')
} else {
  console.log(
    'no GitHub App: pull request checks and comments are not published, and no teardown ' +
      'request can reach a workflow run',
  )
}

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
