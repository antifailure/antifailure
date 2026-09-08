// The licence gate, in front of routes that stay mounted when it refuses.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// THE DECISION THIS FILE IS, AND IT IS THE WHOLE POINT OF THE GATE.
//
// The cheap way to gate a licensed feature is not to register it. One `if`
// around registerExtension, no per-request cost, and an unlicensed installation
// answers 404 on every enterprise path. It is also wrong, and the argument
// against it is the argument this entire repository is built on: 404 says the
// feature does not exist. It is indistinguishable from a build that never had
// it, from a route that was renamed, from a proxy that dropped the path, and
// from the state this lane was dispatched to fix, which is four finished
// products mounted by nothing. An operator staring at a 404 has no way to tell
// "you have not bought this" from "we shipped it broken", and neither does the
// support engineer they eventually reach.
//
// So the routes are ALWAYS mounted and the refusal is an answer. 402, once,
// naming the feature, the state of the licence, and what would change it. That
// makes the negative observable: a test can prove that an unlicensed request
// was REFUSED rather than merely unanswered, which are different claims and
// have been confused here before.
//
// It also means `registeredExtensions()` reports single sign-on as mounted on
// an unlicensed installation, which is correct and is the honest version of the
// distinction Wave 7 draws between absent and ungated. Absent says we did not
// build it. This says we built it, you have not bought it, and here is the
// sentence that says so.
//
// EVALUATED PER REQUEST, NOT ONCE AT STARTUP. A gate decided at boot is a gate
// that cannot expire: a control plane that has been up for six weeks would keep
// honouring a licence that ran out in the third, and nothing would say so until
// somebody restarted it for an unrelated reason. Parsing is done once, because
// parsing is what fails loudly and what costs anything. Evaluating is done on
// every request, against the clock, because that is the part that changes.

import type { Extension, ExtensionRoute } from '@antifailure/api'
import { evaluate, none, type Claims, type Feature, type Status } from './license.ts'

/** The status code for "this installation is not licensed for that".
 *
 *  Deliberately not 403, which the packages behind this gate already use for a
 *  caller who is authenticated and not permitted, and deliberately not 404,
 *  which is the answer this file exists to refuse to give. One code, one
 *  meaning, and a log line that says which of the two gates refused. */
export const NOT_LICENSED = 402

export interface GateOptions {
  /** The claims read at startup, or null when there is no licence at all. */
  claims: Claims | null
  /** The organization this installation runs as. */
  org: string
  now: () => Date
  revoked?: ReadonlySet<string>
  /** Where a refusal is recorded. A refused enterprise request that leaves no
   *  trace is a support call with nothing to read. */
  log?: (line: string) => void
}

/**
 * The licence right now, from claims parsed once.
 *
 * No licence is `none`, not `expired`, and the difference is the sentence an
 * operator is shown. This first said `expired`, because it built an empty
 * claims object with a zero expiry and evaluated it, and the first run of
 * entrypoint.test.ts caught it: a customer who had installed the enterprise
 * image and not yet pasted a key would have been told their licence had run out
 * and its grace period had ended, and sent to renew something they had never
 * bought. Both states refuse the same request, which is exactly why the state
 * has to be right: the code is identical and only the words differ.
 */
export function statusNow(options: GateOptions): Status {
  if (!options.claims) return none()
  return evaluate(options.claims, { org: options.org, now: options.now(), revoked: options.revoked })
}

/** The sentence an unlicensed caller is given. It names the feature, because
 *  "not licensed" with no subject is a message somebody has to open the source
 *  to act on. */
export function refusalBody(feature: Feature, status: Status): Record<string, unknown> {
  return {
    error: 'not_licensed',
    feature,
    licenseState: status.state,
    detail:
      status.state === 'none'
        ? `This control plane is running the enterprise entry point with no license installed, ` +
          `so ${feature} is mounted and refused. Set AF_LICENSE_KEY and AF_ORG, or run the ` +
          `community entry point, which does not mount it at all.`
        : `This installation's license does not currently permit ${feature}: it is ` +
          `${status.state}. ${status.warning}`.trim(),
  }
}

/**
 * Wraps an extension so every one of its routes answers the gate.
 *
 * Every route, without exception and without a list. A gate applied to the
 * routes somebody remembered is the same defect one level down: this repository
 * has already shipped a rate limiter fanned out over a hand written list that
 * a later route was never added to. Mapping over `extension.routes` means a
 * route added to the wrapped package tomorrow is gated the day it appears.
 */
export function gated(extension: Extension, feature: Feature, options: GateOptions): Extension {
  const log = options.log ?? ((line: string) => console.error(line))
  const routes: ExtensionRoute[] = extension.routes.map((route) => ({
    ...route,
    handler: (c) => {
      const status = statusNow(options)
      if (!status.enabled(feature)) {
        log(
          `${extension.name}: refused ${route.method} ${route.path}: the license does not permit ` +
            `${feature} (${status.state})`,
        )
        return c.json(refusalBody(feature, status), NOT_LICENSED)
      }
      return route.handler(c)
    },
  }))
  return { name: extension.name, routes }
}
