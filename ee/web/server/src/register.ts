// What the enterprise edition adds to the control plane.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Separated from main.ts so that the registration can be driven by a test at a
// real port without the test also having to become a process launcher for every
// case. main.ts is the process; this is the decision, and the decision is what
// is worth proving.

import { registerExtension, setSignInPolicy, type Clock } from '@antifailure/api'
import type { Pool } from '@antifailure/db'
import { ssoExtension, signInPolicy, keyFromEnv } from '@antifailure-ee/sso'
import { scimExtension } from '@antifailure-ee/scim'
import { declare, licensed } from '@antifailure-ee/features'
import {
  Forwarder,
  auditStreamExtension,
  fromEnvironment,
  scheduleFromEnvironment,
  sealingKeyFrom,
  sinkFor,
  startForwarder,
  SealError,
  SinkEnv,
  SinkRefused,
  type Fetcher,
  type ForwarderHandle,
} from '@antifailure-ee/audit'
import { gated, statusNow } from './gate.ts'
import { licenseFromEnv, LicenseRefused, ALL_FEATURES, type Claims, type Status } from './license.ts'

// Recorded at module scope, so importing this module is what puts the site in
// the registry and a module nothing imports declares nothing. The site is
// `startAuditStream` because that is the function that builds the `permitted`
// callback the forwarder asks on every pass; the licence question is asked
// there and nowhere else on this path.
declare('audit_stream', 'ee/web/server/src/register.ts:startAuditStream')
// The second site, and it is a second place the question is asked rather than a
// second name for the first. A hosted organization chooses its own destination
// through these routes, and a route that let an unentitled organization store a
// destination would be configuration for a stream the forwarder then declines,
// which reads to the customer exactly like a stream that is broken.
declare('audit_stream', 'ee/web/server/src/register.ts:auditStreamRoutes')

export interface RegisterOptions {
  pool: Pool
  clock: Clock
  /** Where this control plane is reachable, which is the address a provider
   *  posts an assertion to and the address SCIM resources carry. */
  baseUrl: string
  /** Where a browser lands after signing in. */
  appBaseUrl: string
  secureCookies: boolean
  env: NodeJS.ProcessEnv
  log: (line: string) => void
  /** The key stored connection secrets are encrypted under. Read from the
   *  environment when absent, which is what the deployment does; supplied
   *  directly by the suite, which must not have a key in its environment. */
  encryptionKey?: Buffer
  /** The transport every audit sink uses, the installation's and each
   *  organization's. The environment's fetch when absent, which is what the
   *  deployment does. Supplied by the suite so a customer destination that names
   *  a public host can be delivered to a receiver the suite holds, while the
   *  customer destination rule, which refuses every loopback and private
   *  address, stays exactly as strict as it is in production. */
  fetch?: Fetcher
}

export interface Registered {
  /** The claims this process is running under, or null for no licence. The
   *  gate re-evaluates them against the clock on every request, so this is what
   *  was parsed rather than what is permitted at any particular moment. */
  claims: Claims | null
  /** The extensions this entry point mounted, whether or not the licence
   *  permits them. Mounted and refused is the point: see gate.ts. */
  mounted: string[]
  /** The audit stream forwarder, when one is configured, so the process that
   *  started it can stop it and a suite can wait for a pass rather than sleep.
   *  Null when AF_AUDIT_STREAM_SINK is unset, which is the ordinary case. */
  auditStream: ForwarderHandle | null
}

/**
 * The licence, and the sentence that is printed about it whatever it says.
 *
 * Said out loud on every start, in every state, for the same reason every other
 * configuration line in boot.ts is: an enterprise control plane running
 * unlicensed and an enterprise control plane running licensed produced
 * identical logs until this line existed, and the first person to discover the
 * difference was a customer whose provider got a 402.
 */
export function readLicense(env: NodeJS.ProcessEnv, now: Date, log: (line: string) => void): Claims | null {
  let status: Status
  try {
    status = licenseFromEnv(env, now)
  } catch (err) {
    if (err instanceof LicenseRefused) {
      // Refused, not degraded. A licence that does not parse is a deployment
      // mistake and not a commercial state, and starting anyway would mean an
      // enterprise deployment quietly serving 402 to its own identity provider
      // because somebody pasted a truncated key.
      log(`the license key was refused (${err.refusal}): ${err.message}`)
      process.exit(2)
    }
    throw err
  }

  switch (status.state) {
    case 'none':
      log(
        'no license is installed: every enterprise route is mounted and refuses with 402. ' +
          'Set AF_LICENSE_KEY and AF_ORG, or run the community entry point.',
      )
      break
    case 'active':
      log(
        `licensed to ${status.claims!.org} on the ${status.claims!.plan} plan, ` +
          `permitting ${status.claims!.features.join(', ') || 'nothing'}` +
          (status.claims!.seats > 0 ? `, ${status.claims!.seats} seats` : ', unlimited seats'),
      )
      break
    default:
      log(`the license is ${status.state}: ${status.warning}`)
      break
  }
  // Asked rather than copied out of the claims: a licence lists what was bought
  // and `enabled` reports what is permitted right now, and those differ for an
  // expired licence, a revoked one and a rolled back clock.
  log(
    'enterprise features permitted right now: ' +
      (ALL_FEATURES.filter((f) => status.enabled(f)).join(', ') || 'none'),
  )
  return status.claims
}

/**
 * How many members the licence covers, or null for no limit.
 *
 * Exported so it can be tested as a number rather than only through a
 * provisioning flow. provision.ts already proves what happens when the host
 * supplies a limit; what was never proved is that a host supplies one, because
 * there was no host.
 *
 * Zero seats is unlimited, not a limit of nobody, which is the Go side's rule
 * and the only reading that does not lock every customer on an unmetered
 * licence out of their own directory.
 */
export function seatsFrom(claims: Claims | null): number | null {
  if (!claims || claims.seats <= 0) return null
  return claims.seats
}

/**
 * Registers single sign-on and provisioning, gated.
 *
 * BOTH SIGN-ON EXTENSION POINTS, always. ee/web/sso/src/index.ts explains why
 * at length and it is worth repeating here because this is the caller that used
 * not to exist: registering the routes without the sign-in policy leaves an
 * organization that has REQUIRED single sign-on with GitHub sign-in still open,
 * which is a feature that looks complete and enforces nothing.
 */
export function registerEnterprise(options: RegisterOptions): Registered {
  const claims = readLicense(options.env, options.clock.now(), options.log)
  const org = options.env.AF_ORG?.trim() ?? ''
  const gate = {
    claims,
    org,
    now: () => options.clock.now(),
    revoked: new Set(
      (options.env.AF_LICENSE_REVOKED ?? '').split(',').map((s) => s.trim()).filter((s) => s !== ''),
    ),
    log: options.log,
  }

  // Read here, at startup, rather than on the first login that needs it, which
  // is the rule boot.ts already applies to every other secret.
  const encryptionKey = options.encryptionKey ?? keyFromEnv(options.env)

  const sso = gated(
    ssoExtension({
      pool: options.pool,
      clock: options.clock,
      baseUrl: options.baseUrl,
      appBaseUrl: options.appBaseUrl,
      secureCookies: options.secureCookies,
      encryptionKey,
      // The seat limit, supplied at last.
      //
      // ee/web/sso/src/provision.ts has always taken this from "the host",
      // saying the licence is parsed elsewhere and the control plane is handed
      // the number. There was no host, so nothing was ever handed anything, so
      // AF-EE-004's seat limit has never refused a single member. It does now,
      // and it is the licence's own number rather than a second one.
      seats: async () => seatsFrom(gate.claims),
      log: options.log,
    }),
    'sso',
    gate,
  )

  const scim = gated(
    scimExtension({
      pool: options.pool,
      clock: options.clock,
      baseUrl: options.baseUrl,
      defaultRole: 'member',
      log: options.log,
    }),
    'scim',
    gate,
  )

  const sealingKey = auditSealingKey(options)
  const audit = gated(auditStreamRoutes(options, gate, sealingKey), 'audit_stream', gate)

  registerExtension(sso)
  registerExtension(scim)
  registerExtension(audit)
  setSignInPolicy(signInPolicy(options.pool))

  return {
    claims,
    mounted: [sso.name, scim.name, audit.name],
    auditStream: startAuditStream(options, gate, sealingKey),
  }
}

/**
 * The key an organization's collector credential is sealed under, or null.
 *
 * AF_PROVIDER_KEY_SECRET, the variable the community control plane already
 * seals a customer's provider key under, and deliberately not a new one: it
 * already reaches every hosted deployment from Key Vault, so a customer
 * destination needs no configuration an operator does not already have. See
 * ee/web/audit/src/destinations.ts for the whole argument.
 *
 * Unset is a supported state and says so: the routes answer, saving refuses
 * with 503 naming the variable, and an installation sink from the environment
 * still works. Malformed is not, and refuses to start, for the reason
 * readLicense gives about a key that does not parse: it is a deployment mistake,
 * and starting anyway would mean a control plane that fails on the first save
 * somebody attempts rather than at the deploy that broke it.
 */
function auditSealingKey(options: RegisterOptions): Buffer | null {
  try {
    const key = sealingKeyFrom(options.env.AF_PROVIDER_KEY_SECRET)
    if (!key) {
      options.log(
        'AF_PROVIDER_KEY_SECRET is not set, so no organization can choose its own audit stream ' +
          'destination. The installation sink, if one is configured, is unaffected.',
      )
    }
    return key
  } catch (err) {
    if (err instanceof SealError) {
      options.log(`the audit stream sealing key was refused: ${err.message}`)
      process.exit(2)
    }
    throw err
  }
}

/**
 * The routes an organization chooses its own destination through.
 *
 * The ORGANIZATION'S entitlement is asked here, per request, and `gated` asks the
 * INSTALLATION'S licence around every route. Both, for the reason
 * startAuditStream gives for asking both per pass: either one alone would let
 * somebody configure a stream they are not supposed to have.
 */
function auditStreamRoutes(
  options: RegisterOptions,
  gate: { claims: Claims | null; org: string; now: () => Date; revoked: ReadonlySet<string> },
  sealingKey: Buffer | null,
) {
  return auditStreamExtension({
    pool: options.pool,
    clock: options.clock,
    sealingKey,
    log: options.log,
    permitted: async (orgId, now) => {
      if (!statusNow(gate).enabled('audit_stream')) return false
      return licensed(options.pool, orgId, 'audit_stream', now)
    },
  })
}

/**
 * Starts the audit stream forwarder, if one is configured.
 *
 * NOT AN EXTENSION, and that is the fact this was mistaken about twice. An
 * extension is routes; forwarding is a poll loop with no route at all, and the
 * `Extension` interface carrying only `name` and `routes` was cited as the
 * blocker for building this when it is simply the wrong object. This function
 * is registration doing non route work, which is what the line above it,
 * `setSignInPolicy`, already was.
 *
 * WHAT THIS CLOSES. `audit_entries` carries a tamper evident hash chain and is
 * records organization actions including sign on, provisioning and
 * administration. It reached no sink at all, while ee/README.md sold "SIEM
 * streaming with a tamper evident hash chain" and could point at a real half
 * whenever it was questioned: the chain is real, the streaming is real, and
 * they were not joined to each other.
 */
function startAuditStream(
  options: RegisterOptions,
  gate: { claims: Claims | null; org: string; now: () => Date; revoked: ReadonlySet<string> },
  sealingKey: Buffer | null,
): ForwarderHandle | null {
  const fetcher: Fetcher = options.fetch ?? ((url, init) => fetch(url, init))
  let config
  let schedule
  try {
    config = fromEnvironment(options.env, fetcher)
    schedule = config ?? scheduleFromEnvironment(options.env)
  } catch (err) {
    if (err instanceof SinkRefused) {
      // Refused, not degraded, and the same rule the engine's sink follows.
      // Somebody who set this variable has said every privileged action must
      // reach their SIEM; starting with the sink unbuilt forwards nothing and
      // says nothing, which is a compliance control reporting itself as held.
      options.log(`the audit stream sink was refused: ${err.message}`)
      process.exit(2)
    }
    throw err
  }

  if (!config && !sealingKey) {
    // Said out loud, in the state that forwards nothing, for the reason
    // readLicense says its own line out loud: an installation that forwards and
    // one that does not produced identical logs, and the first person to
    // discover the difference was an auditor asking where the entries went.
    options.log(
      `no ${SinkEnv} is set and no organization can choose a destination, so the control ` +
        "plane's audit log is written and not forwarded. The log itself is unaffected: it is " +
        'written whatever a sink does.',
    )
    return null
  }

  // STARTED WITH NO INSTALLATION SINK AT ALL when organizations can choose their
  // own, which is the hosted shape. A forwarder that started only when the
  // environment named a sink would mean a customer who saved a destination saw
  // nothing arrive until somebody restarted the control plane for an unrelated
  // reason. With nothing configured anywhere, a pass reads nothing: the query
  // returns no organization that has neither a destination nor an installation
  // sink covering it.
  const forwarder = new Forwarder({
    pool: options.pool,
    clock: options.clock,
    sink: config?.sink ?? null,
    key: config?.key,
    destinations: sealingKey ? (db, orgId) => sinkFor(db, sealingKey, fetcher, orgId) : null,
    batchSize: schedule.batchSize,
    deliveryBatchSize: schedule.deliveryBatchSize,
    log: options.log,
    // BOTH GATES, ASKED PER PASS, AND NEITHER IS THE OTHER.
    //
    // `statusNow` is the licence key this process was started with: whether
    // this INSTALLATION bought audit streaming, evaluated against the clock so
    // a licence that lapses tonight stops forwarding on the next pass without a
    // restart. `licensed` is the control plane's own entitlement catalogue:
    // whether THIS ORGANIZATION is entitled, which an operator can grant or
    // withdraw with no deploy. SCIM and single sign on ask both for the same
    // reason, and either one alone would forward for somebody who is not
    // supposed to have it.
    //
    // Asked here, per pass, rather than captured at startup. The whole argument
    // in gate.ts applies unchanged: a gate decided at boot cannot expire, and a
    // control plane up for six weeks would keep streaming under a licence that
    // ran out in the third.
    permitted: async (orgId, now) => {
      if (!statusNow(gate).enabled('audit_stream')) return false
      return licensed(options.pool, orgId, 'audit_stream', now)
    },
  })

  options.log(
    'audit stream: forwarding ' +
      (config ? `to ${config.sink.name()} for every organization without its own destination` : '') +
      (config && sealingKey ? ', and ' : '') +
      (sealingKey ? "to each organization's own destination where it has chosen one" : '') +
      `, every ${String(schedule.intervalMs)}ms, ${String(schedule.batchSize)} entries a pass ` +
      `and ${String(schedule.deliveryBatchSize)} a delivery`,
  )

  return startForwarder(forwarder, schedule.intervalMs, (err) =>
    options.log(`audit stream: ${err instanceof Error ? err.message : String(err)}`),
  )
}
