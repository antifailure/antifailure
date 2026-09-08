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
import { gated } from './gate.ts'
import { licenseFromEnv, LicenseRefused, ALL_FEATURES, type Claims, type Status } from './license.ts'

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
}

export interface Registered {
  /** The claims this process is running under, or null for no licence. The
   *  gate re-evaluates them against the clock on every request, so this is what
   *  was parsed rather than what is permitted at any particular moment. */
  claims: Claims | null
  /** The extensions this entry point mounted, whether or not the licence
   *  permits them. Mounted and refused is the point: see gate.ts. */
  mounted: string[]
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

  registerExtension(sso)
  registerExtension(scim)
  setSignInPolicy(signInPolicy(options.pool))

  return { claims, mounted: [sso.name, scim.name] }
}
