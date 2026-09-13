// Building the control plane, so that one process is not the only way to run it.
//
// This file used to be main.ts and it was the whole program: six hundred lines
// of environment reading and construction, at the top level of a module,
// reachable only by running it. That is a fine shape for a program with one
// edition and it is the wrong shape for a product with two, and the cost was
// not theoretical. The enterprise tree holds four finished, tested packages,
// and single sign-on and provisioning were mounted by NOTHING: their own
// comments say "registered by the enterprise entry point" and there was no
// enterprise entry point, because adding one meant either editing this file
// from a tree the community build must not see, or copying every line above
// into a second file that would drift within a week.
//
// The paragraph above deliberately does not name that tree. The boundary job
// in ci.yml greps this directory for its name and fails on a hit, because the
// community build must not be able to reach enterprise code even through a
// string, and it caught this comment on the first push. That is the gate
// working, not an obstacle to word around: the sentence is just as clear
// without the path, and the rule holds for prose as well as for imports.
//
// So the body is a function now, and it takes one seam. Both editions call it,
// so there is exactly one place that reads AF_ variables and exactly one place
// that builds the server, and a configuration difference between the community
// and the enterprise control plane is not expressible rather than merely
// unlikely. That property is the reason for the refactor: two copies of this
// file would disagree about a default and the symptom would be a bug only a
// paying customer can reproduce.
//
// WHERE THE SEAM IS, AND WHY IT IS THERE AND NOT SOMEWHERE ELSE.
// createServer reads extensionRoutes() once, while it is building the router,
// so an extension registered afterwards is registered into nothing: the object
// exists, the routes list contains it, and no request ever reaches it. That is
// this repository's own signature failure, and putting the seam anywhere later
// would rebuild it. beforeServer runs after the pool exists, because every
// extension needs one, and before the router is built, because after is too
// late.
import { serve } from '@hono/node-server'
import { createPool, createAdminPool, migrate, type AdminPool, type Pool } from '@antifailure/db'
import postgres from 'postgres'
import { createServer } from './server.ts'
import { failureRetentionFrom } from './failures.ts'
import { registeredExtensions } from './extensions.ts'
import { describeTrustedProxyHops, trustedProxyHopsFrom } from './clientaddress.ts'
import { RealGitHubClient } from './auth/github.ts'
import { systemClock, type Clock } from './clock.ts'
import { sweepSessions } from './auth/session.ts'
import { sweepDeviceAuthorizations } from './auth/device.ts'
import { parseAllowlist, describeAllowlist, signupUrlFrom, sweepOAuthStates } from './auth/signin.ts'
import { selfServeSignupFrom, describeSelfServeSignup } from './auth/provision.ts'
import { leadNotifierFrom } from './enterprise/leads.ts'
import { siteOriginsFrom, siteOriginsSummary } from './siteorigin.ts'
import { keyringFrom } from './providers/seal.ts'
import { findConsoleBuild } from './console/static.ts'
import { appConfigFrom, InstallationTokens } from './github/app.ts'
import { RealRepositoryApi } from './github/api.ts'
import { sweepGenerations, sweepTeardowns, type LifecycleDeps } from './github/lifecycle.ts'
import { sweepSetups } from './github/setup.ts'
import { pricesFrom } from './providers/pricing.ts'
import { retentionFromEnv, startMaintenance } from './maintenance.ts'
import { freshness, recordingStopped } from './analytics/read.ts'
import { surrogateSecretFrom } from './analytics/record.ts'
import { analyticsRetentionFromEnv } from './analytics/rollup.ts'
import { ResendMailer } from './auth/mail.ts'
import { sweepEmailSignInTokens } from './auth/email.ts'
import { sweepExpiredWorkflowTokens } from './tokens.ts'
import { resumeDeletions } from './enterprise/deletion.ts'
import type { EmailSignInConfig } from './auth/email.ts'
import { PRICE_ENV, RealStripeClient, stripeConfigFrom } from './billing/index.ts'
import {
  githubAppInstallUrlFrom,
  hostedRequiredPlanFrom,
  operatorSetsPlanFrom,
} from './hosted.ts'
import { POSTHOG_REGIONS, postHogRegionFrom, postHogSummary } from './analytics/posthog.ts'
import { createPostHogSink, postHogSinkSummary } from './analytics/posthog-sink.ts'

function required(name: string, ...fallbacks: string[]): string {
  for (const n of [name, ...fallbacks]) {
    const value = process.env[n]
    if (value) return value
  }
  const names = [name, ...fallbacks].join(' or ')
  console.error(`${names} is not set. The control plane needs one of them to start.`)
  process.exit(2)
}

// ---------------------------------------------------------------------------
// The seam
// ---------------------------------------------------------------------------

/**
 * What an edition is handed to build itself with.
 *
 * Everything here is a value this file has already read, validated and said out
 * loud, rather than a variable name to read again. An edition that re-read the
 * environment would be a second reader of the same setting and therefore a
 * second place for it to be wrong, which is the exact failure the seam exists
 * to avoid.
 */
export interface BootContext {
  pool: Pool
  adminPool: AdminPool | null
  clock: Clock
  /** Where a browser lands, and on this deployment the address the API itself
   *  answers on: the console is served from this origin by the same process. */
  appBaseUrl: string | undefined
  /** False only for local HTTP development, and it is read from the same
   *  variable the session cookie is read from, so the two cannot disagree. */
  secureCookies: boolean
  /** The environment, passed rather than reached for, so a test can supply one
   *  and so every reader of a setting is visible from here. */
  env: NodeJS.ProcessEnv
  /** Where a startup line goes. The same stream every other line above uses,
   *  because an edition's registrations belong in the same log as the
   *  configuration that produced them. */
  log: (line: string) => void
}

export interface BootHooks {
  /**
   * Called once, after the pool is open and before the router is built.
   *
   * A throw here stops the process. That is deliberate and it is the same rule
   * the rest of this file applies to every secret: an enterprise control plane
   * that cannot mount single sign-on has not started successfully, it has
   * started as something else, and discovering that on the first login attempt
   * is discovering it in production.
   */
  beforeServer?: (ctx: BootContext) => void | Promise<void>
  /** Stops edition-owned scheduling before the shared database pool closes. */
  beforeClose?: () => void | Promise<void>
}

/** The running process, for a caller that wants to stop it. */
export interface ControlPlane {
  pool: Pool
  port: number
  close(): Promise<void>
}

/**
 * How often the grouped failure store writes what it accumulated.
 *
 * Ten seconds, matching the Logs page's poll, so a failure reaches the screen
 * within about two ticks. Shorter would put a write on the error path in all
 * but name; longer would leave an operator watching a page that says nothing is
 * wrong while it is.
 */
const FAILURE_FLUSH_MS = 10_000

export async function startControlPlane(hooks: BootHooks = {}): Promise<ControlPlane> {

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

  // Where somebody asking to buy is announced, and where the page that asks them
  // is allowed to post from.
  //
  // Both are said out loud at start-up, because both are absences that look
  // exactly like working software. A deployment with a lead route and no notifier
  // records leads nobody is told about; a deployment with no site origin serves a
  // form that a browser refuses to submit and reports as a network error.
  const siteOrigins = siteOriginsFrom(process.env.AF_SITE_ORIGIN)
  console.log(siteOriginsSummary(siteOrigins))
  const leads = leadNotifierFrom(process.env, emailSignIn?.mailer)
  console.log(leads.summary)

  // The marketing site's analytics, forwarded from this process so that a
  // reader's browser never opens a connection to posthog.com. Read at start-up so
  // an unknown region stops the process here rather than on the first page view,
  // and said out loud either way: a proxy that is not mounted looks exactly like
  // one that is, right up until every event 404s.
  const postHogRegion = postHogRegionFrom(process.env.AF_POSTHOG_REGION)
  console.log(postHogSummary(postHogRegion))

  // What this process reports about ITSELF, which is a different thing from the
  // proxy above and is off by default. Somebody self-hosting this measures their
  // own usage or nobody does; only the hosted deployment tells us about ours.
  const postHogSink = createPostHogSink({
    projectKey: process.env.AF_POSTHOG_PROJECT_KEY ?? null,
    region: postHogRegion,
  })
  console.log(postHogSinkSummary(postHogSink))

  // Said out loud at startup, every time. Whether an instance is open to the
  // world is not something anybody should have to infer from a deployment
  // template, and a closed instance that quietly opened is the failure that has
  // no symptom until it has a very large one.
  const signInAllowlist = parseAllowlist(process.env.AF_SIGNIN_ALLOWLIST)
  console.log(describeAllowlist(signInAllowlist))

  // The other half of the same sentence, said in the same place and for the same
  // reason. The allowlist decides who gets through the door; this decides whether
  // there is a room on the other side of it. An installation where sign-in is
  // open and self serve signup is off is one where every new person reaches an
  // empty state, which looks like a broken product rather than a configured one,
  // and the only way to find that out used to be to watch somebody do it.
  const selfServeSignup = selfServeSignupFrom(process.env.AF_SELF_SERVE_SIGNUP)
  console.log(describeSelfServeSignup(selfServeSignup))

  // Read at start-up so a hop count that is not a number stops the process here,
  // rather than putting every caller in one shared bucket and answering 429 to
  // the whole product. The default is one proxy, which is the ingress alone.
  const trustedProxyHops = trustedProxyHopsFrom(process.env.AF_TRUSTED_PROXY_HOPS)
  console.log(describeTrustedProxyHops(trustedProxyHops))

  // Read at start-up rather than on first use, so a secret of the wrong length
  // stops the process here instead of on the one request the feature exists for.
  //
  // The line it prints names the VERSIONS held, not a boolean, and that is the
  // one check a rotation otherwise has no way to make: "the new revision picked
  // up the new key" is unprovable from the outside without decrypting somebody's
  // credential, and this says it in the log the deploy already tails. Versions
  // are operators' own labels; no key material is printed here or anywhere.
  const keyring = keyringFrom({
    AF_PROVIDER_KEY_SECRET: process.env.AF_PROVIDER_KEY_SECRET,
    AF_PROVIDER_KEY_SECRETS: process.env.AF_PROVIDER_KEY_SECRETS,
    AF_PROVIDER_KEY_VERSION: process.env.AF_PROVIDER_KEY_VERSION,
  })
  console.log(
    keyring
      ? keyring.summary()
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

  // Analytics. Off unless a surrogate secret is configured, and said out loud
  // either way, because "recording" and "not recording" are the two states an
  // operator most needs to be sure about and a dashboard of zeros looks the same
  // in both. Read at start-up so a secret of the wrong length stops the process
  // here rather than on the first event.
  const analyticsSecret = surrogateSecretFrom(process.env.AF_ANALYTICS_SURROGATE_SECRET)
  const analyticsOperatorOrgSlug = process.env.AF_ANALYTICS_OPERATOR_ORG ?? null
  console.log(
    analyticsSecret
      ? 'analytics is recording: AF_ANALYTICS_SURROGATE_SECRET is set'
      : 'analytics is NOT recording: AF_ANALYTICS_SURROGATE_SECRET is not set',
  )
  console.log(
    analyticsOperatorOrgSlug
      ? `the analytics dashboard is readable by owners and admins of ${analyticsOperatorOrgSlug}`
      : 'the analytics dashboard is readable by nobody: AF_ANALYTICS_OPERATOR_ORG is not set',
  )

  // Did this installation record once and stop?
  //
  // "Analytics is not recording" is the correct and unremarkable state for a
  // staging environment and for any self-hosted control plane whose operator
  // never wanted it, so the line above cannot be an alarm. On an installation
  // that HAS recorded it is a regression, and the usual way to reach it is a
  // rollback to a revision that predates the analytics variables, which takes the
  // environment back with it and leaves everything else working.
  //
  // The database is asked because it is the only party to this that a rollback
  // does not move. See recordingStopped in analytics/read.ts for why an absent
  // variable cannot detect its own absence.
  //
  // A failed read is reported as a failed read. An installation whose rollup
  // state cannot be reached has not been shown to be healthy, and printing
  // nothing here would be indistinguishable from printing nothing because there
  // was nothing to say.
  try {
    const state = await pool.withoutTenant((db) => freshness(db))
    if (recordingStopped(analyticsSecret !== null, state.lastRunAt)) {
      console.error(
        'ANALYTICS HAS STOPPED RECORDING. This installation was recording, and the ' +
          `last rollup ran at ${state.lastRunAt}, but AF_ANALYTICS_SURROGATE_SECRET is ` +
          'not set on this revision, so nothing new is being written and the dashboard ' +
          'will keep serving the numbers up to that point. A rollback to a revision ' +
          'from before analytics was configured does exactly this. Restore the ' +
          'variable, or if analytics was switched off deliberately, expect a hole.',
      )
    }
  } catch (err) {
    console.error(
      'could not check whether analytics has stopped recording: ' +
        (err instanceof Error ? err.message : String(err)),
    )
  }

  let hostedRequiredPlan
  let githubAppInstallUrl
  let signupUrl
  let operatorSetsPlan = false
  try {
    hostedRequiredPlan = hostedRequiredPlanFrom(process.env.AF_HOSTED_REQUIRED_PLAN)
    githubAppInstallUrl = githubAppInstallUrlFrom(process.env.AF_GITHUB_APP_INSTALL_URL)
    signupUrl = signupUrlFrom(process.env.AF_SIGNUP_URL)
    operatorSetsPlan = operatorSetsPlanFrom(process.env.AF_OPERATOR_SETS_PLAN)
  } catch (err) {
    console.error(err instanceof Error ? err.message : String(err))
    process.exit(2)
  }
  if (hostedRequiredPlan && !stripe.config) {
    console.error(
      'AF_HOSTED_REQUIRED_PLAN is set but billing is off. Set AF_STRIPE_SECRET_KEY, ' +
        'AF_STRIPE_WEBHOOK_SECRET and AF_STRIPE_PRICE_TEAM so an organization can satisfy the gate.',
    )
    process.exit(2)
  }
  // The same contradiction reached through a second door, which opened when
  // AF_STRIPE_PRICE_ENTERPRISE stopped being required.
  //
  // The check above asks whether billing is on. Billing can now be on while the
  // gated plan itself has no price: set AF_HOSTED_REQUIRED_PLAN=enterprise with a
  // Team price and no Enterprise price and every organization is refused
  // everything until it holds a plan that this process has no way to sell it.
  // That is exactly the state the check above exists to prevent, so it is refused
  // in the same place and for the same reason rather than discovered by the first
  // customer who cannot get in.
  if (hostedRequiredPlan && stripe.config && !stripe.config.prices[hostedRequiredPlan]) {
    // The variable is NAMED from PRICE_ENV rather than built out of the plan.
    //
    // This line originally wrote a prefix and appended the uppercased plan, which
    // put a truncated fragment in the source and nothing else. config-docs.test.ts
    // scans for AF_ names and reported that fragment as a variable read and not
    // documented, which is true and unfixable from the reference: it is not a
    // name anybody can set. A closed map is the only shape that keeps the set of
    // settings this process reads enumerable.
    console.error(
      `AF_HOSTED_REQUIRED_PLAN is ${hostedRequiredPlan} and there is no Stripe price for that ` +
        `plan, so no organization could ever satisfy the gate. Set ${PRICE_ENV[hostedRequiredPlan]}, ` +
        'or unset AF_HOSTED_REQUIRED_PLAN.',
    )
    process.exit(2)
  }
  // The other contradiction, refused for the same reason and in the same place.
  //
  // A route that grants any plan by hand, on a process that also sells those
  // plans, is a product nobody has to pay for. Refusing it HERE rather than only
  // inside `billing.set` is the point: a start-up refusal covers whatever writes
  // the plan next, and a check inside one procedure covers one procedure. The
  // pair of them is what makes `operatorSetsPlan` mean "this process takes no
  // money at all" wherever it is read.
  if (operatorSetsPlan && (stripe.config || hostedRequiredPlan)) {
    console.error(
      'AF_OPERATOR_SETS_PLAN is set on an installation that takes payment. A plan that can be ' +
        'granted by hand is not a plan anybody has to buy: unset it, or unset the Stripe ' +
        'variables and AF_HOSTED_REQUIRED_PLAN.',
    )
    process.exit(2)
  }
  console.log(
    hostedRequiredPlan
      ? `hosted access requires the ${hostedRequiredPlan} plan`
      : 'no hosted plan gate: this installation serves every plan',
  )
  console.log(
    operatorSetsPlan
      ? 'plans are set by hand: billing.set writes the plan on this installation'
      : 'plans are not set by hand: billing.set is refused (AF_OPERATOR_SETS_PLAN is not set)',
  )

  // Said out loud for the same reason as its neighbours, and because this one was
  // unset on both control planes for weeks without anybody noticing. Nothing
  // fails when it is missing: sign-in works, the console renders, and the only
  // symptom is on a screen the operator does not see, shown to somebody who has
  // just arrived and has no organization yet. A configuration whose absence is
  // invisible from the inside is one the startup line has to name.
  console.log(
    githubAppInstallUrl
      ? `people with no organization are offered the GitHub App at ${githubAppInstallUrl}`
      : 'people with no organization are NOT offered the GitHub App: AF_GITHUB_APP_INSTALL_URL is ' +
          'not set. They can still ask GitHub to recheck their membership.',
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

  // ---------------------------------------------------------------------------
  // The edition's own registrations, and the line that says whether they
  // happened
  // ---------------------------------------------------------------------------
  //
  // Everything above is common to both editions. This is the one point where
  // they differ, and it has to be here: createServer walks extensionRoutes()
  // while it builds the router, so anything registered after this call is
  // registered into a list nobody reads again.
  //
  // A throw is not caught. An enterprise deployment whose single sign-on failed
  // to register would otherwise come up as a community control plane wearing an
  // enterprise tag, serving 404 on every provider callback, and the first person
  // to notice would be a customer's identity administrator.
  const appBaseUrl = process.env.AF_APP_BASE_URL ?? process.env.AF_ENV_URL
  const secureCookies = process.env.AF_INSECURE_COOKIES !== '1'
  if (hooks.beforeServer) {
    await hooks.beforeServer({
      pool,
      adminPool,
      clock: systemClock,
      appBaseUrl,
      secureCookies,
      env: process.env,
      log: (line) => console.log(line),
    })
  }

  // Said out loud, always, in both editions.
  //
  // This one line is the instrument the night this file was split was missing.
  // Four finished enterprise packages were mounted by nothing at all, and no
  // running process said so, because nothing ever printed what was mounted:
  // the community control plane's log and an enterprise control plane's log
  // with a broken registration are identical documents. They are not any more.
  const mounted = registeredExtensions()
  if (mounted.length === 0) {
    console.log('no extensions are registered: this is the community control plane')
  } else {
    for (const extension of mounted) {
      console.log(
        `extension ${extension.name} is mounted: ` +
          extension.routes.map((r) => `${r.method} ${r.path}`).join(', '),
      )
    }
  }

  const { app, ingestLimiter, authLimiter, failures } = createServer({
    pool,
    adminPool,
    github,
    clock: systemClock,
    // The same value the seam handed the edition, not a second read of the
    // same variable. Two readers is how a community and an enterprise server
    // end up disagreeing about whether a cookie is Secure.
    secureCookies,
    trustedProxyHops,
    appBaseUrl,
    signInAllowlist,
    selfServeSignup,
    postHog: postHogRegion ? { bases: POSTHOG_REGIONS[postHogRegion] } : null,
    postHogSink,
    leadNotifier: leads.notifier,
    keyring,
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
    operatorSetsPlan,
    githubAppInstallUrl,
    signupUrl,
    modelPrices,
    consoleBuild,
    analyticsSecret,
    analyticsOperatorOrgSlug,
    // Every origin the marketing site may call from, read and validated above
    // rather than taken raw from the environment: siteOriginsFrom refuses a value
    // carrying a path, which could never match an Origin header and would allow
    // nobody while looking configured. Unset refuses every beacon, every lead and
    // every application rather than reflecting whatever Origin arrives.
    siteOrigins,
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
        // The analytics rollup rides the same pass, for the same reason and with
        // the same credential: it reads a table the application role cannot read
        // and writes one it can only select from. A second scheduler would be a
        // second thing to notice had stopped.
        analyticsRetentionDays: analyticsRetentionFromEnv(process.env),
        // The grouped failure store's retention rides the same pass, on the
        // same credential, for the same reason: 0042 gives the application role
        // no DELETE, and a second scheduler is a second thing to notice had
        // stopped.
        failureRetentionDays: failureRetentionFrom(process.env),
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
  // The failure store's flush.
  //
  // ON ITS OWN TIMER, and much faster than the five minute housekeeping pass,
  // because this is the one thing on that list a person is watching. Ten
  // seconds is the interval the Logs page polls at, so a failure is on screen
  // within about two ticks of happening.
  //
  // Separate from housekeeping for a second reason: every sweep in that pass is
  // about table size and being late costs rows. This one is about a number an
  // operator is reading during an incident, and being five minutes late means
  // the page says nothing is wrong for five minutes after it started going
  // wrong.
  //
  // `void` with a catch, and the catch is not decoration. `flush` already puts
  // failed groups back in the buffer and increments a counter, so an
  // unhandled rejection here would be a second report of something already
  // reported, on the path least able to afford one.
  const failureFlush = setInterval(() => {
    void failures.flush().catch((err) => console.error('failure store flush', err))
  }, FAILURE_FLUSH_MS)
  failureFlush.unref()

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

      // Beside the other two because it is the same kind of debt, and it is the
      // one that had no sweeper at all rather than one that could not delete.
      // A state row is dead ten minutes after it is written and stayed in the
      // table for good: every abandoned "Continue with GitHub" left one, and so
      // did every site publish, because the deploy gate probes GET /auth/github.
      void sweepOAuthStates(pool, systemClock).catch((err) =>
        console.error('oauth state sweep', err),
      )

      // The fourth of the same kind, on the table that holds credentials.
      //
      // A GitHub Actions run trades its workflow identity for an engine token
      // once per engine session, so a single continuous integration run mints
      // about eight of them, each good for fifteen minutes. Nothing had ever
      // removed one. Three days of this repository's own pull requests had put
      // seven hundred dead rows on /cli, which is the one screen a person can
      // read to find out what can act as their organization, and it had buried
      // their own signed in terminal near the bottom of it.
      //
      // It cannot reach a live credential, a revoked one, a person's terminal or
      // a pasted engine secret: the policy in 0041 admits expired workflow
      // identities and nothing else, and it decides on the database's clock
      // rather than on the cutoff passed from here.
      void sweepExpiredWorkflowTokens(pool, systemClock).catch((err) =>
        console.error('workflow token sweep', err),
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
        // The pull request that adds the workflow file to a newly installed
        // repository. Same deps, same interval, same reason it is started here:
        // a queue nothing sweeps is a table of rows that say "queued" forever.
        void sweepSetups(lifecycle).catch((err) => console.error('setup sweep', err))
      },
      60 * 1000,
    )
    lifecycleSweep.unref()
    console.log(
      'the pull request lifecycle is running: checks, one comment per pull request, teardown, ' +
        'and the setup pull request for a newly installed repository',
    )
  } else {
    console.log(
      'no GitHub App: pull request checks and comments are not published, and no teardown ' +
        'request can reach a workflow run',
    )
  }

  let server: ReturnType<typeof serve>
  // Awaited, so the caller is handed a port rather than a promise of one. With
  // AF_PORT=0 the kernel chooses, and a test that has to guess which port that
  // was is a test that races the server it is testing.
  const listening = await new Promise<number>((resolve) => {
    const started = serve({ fetch: app.fetch, port }, (info) => {
      console.log(`control plane listening on :${info.port}`)
      resolve(info.port)
    })
    server = started
  })

  // Draining rather than dropping. A rolling deploy that kills connections
  // mid-request turns every deploy into a handful of failed pull request checks.
  for (const signal of ['SIGTERM', 'SIGINT'] as const) {
    process.on(signal, () => {
      console.log(`${signal}: draining`)
      server.close(() => {
        // The sink is flushed BEFORE the pool closes and the process exits, or
        // whatever it batched in the last ten seconds is lost on every deploy.
        // It swallows its own failures, so a vendor being unreachable delays this
        // by its own timeout and never turns a clean shutdown into a bad exit
        // status.
        // Flushed before the pool closes, for the same reason the sink is:
        // whatever the last ten seconds grouped is otherwise lost on every
        // deploy, and a deploy is exactly when an operator is looking.
        void Promise.resolve()
          .then(() => hooks.beforeClose?.())
          .then(() => failures.flush())
          .catch((err) => console.error('failure store flush on shutdown', err))
          .then(() => postHogSink.shutdown())
          .then(() => pool.close())
          .then(() => process.exit(0))
      })
    })
  }

  return {
    pool,
    port: listening,
    // For a caller that started this in-process, which is the test that proves
    // an enterprise route is reachable over a real socket. The signal handlers
    // above are for the deployment; this is for the suite, and it stops the
    // same things in the same order.
    close: () =>
      new Promise<void>((resolve, reject) => {
        server.close((err) => {
          if (err) {
            reject(err)
            return
          }
          void Promise.resolve()
            .then(() => hooks.beforeClose?.())
            .then(() => failures.flush())
            .catch((err) => console.error('failure store flush on close', err))
            .then(() => postHogSink.shutdown())
            .then(() => pool.close())
            .then(() => resolve())
            .catch(reject)
        })
      }),
  }
}
