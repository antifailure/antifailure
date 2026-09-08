#!/usr/bin/env node
// The enterprise control plane.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// THIS FILE IS THE ONE THREE OTHER FILES ALREADY DESCRIBED.
//
//   web/apps/api/src/extensions.ts  "the enterprise entry point imports this
//                                    and calls it"
//   ee/web/sso/src/routes.ts:172    "Registered by the enterprise entry point"
//   ee/web/sso/test/harness.ts:13   "the way the enterprise entry point
//                                    registers it"
//
// It did not exist. Four finished, tested, documented enterprise packages sat
// in ee/web with no production caller of any kind: `git grep "install("` over
// ee/web and web found nothing outside tests, and nothing under
// web/apps/api/src mentioned @antifailure-ee at all. Single sign-on and SCIM
// were driven end to end over real HTTP against a real Postgres by their own
// suites, and reachable by no customer, because the process that would have
// mounted them was a sentence in three comments.
//
// WHY IT IS HERE AND NOT IN web/apps/api. ci.yml runs a required check called
// "Nothing in the community web depends on the enterprise one", which greps
// web/apps and web/packages for `antifailure-ee` and for `ee/web` and fails on
// a hit. That is the correct boundary and it is not an obstacle to route
// around: the community control plane must not be able to name enterprise code
// even in a string. So the composition lives on this side of the line, imports
// downwards only, and the community tree stays unable to see it.
//
// WHAT IT ADDS TO boot.ts, WHICH IS EXACTLY ONE THING. Every environment
// variable, every refusal, every startup line, the pool, the sweeps, the
// lifecycle and the server are boot.ts's, unchanged and unduplicated. This adds
// registrations. There is no second copy of the configuration, which was the
// alternative and which would have drifted from the community server inside a
// week and produced a bug only a paying customer could reproduce.

import { startControlPlane } from '@antifailure/api/boot'
import { registerEnterprise } from './register.ts'

await startControlPlane({
  beforeServer: (ctx) => {
    // Where a provider posts an assertion, and where a SCIM resource says it
    // lives. Not the same question as "where does a browser land", even though
    // this deployment answers both with one address: the control plane serves
    // the console from its own origin, which is why AF_APP_BASE_URL is the
    // right default and why it is a default rather than an assumption.
    //
    // Wrong here is not a subtle failure. An identity provider is configured
    // with the assertion consumer URL this value produces, so a control plane
    // that names an address it does not answer on hands every customer a
    // provider configuration that cannot work, and the symptom appears in
    // somebody else's admin console.
    const baseUrl = (ctx.env.AF_ENTERPRISE_BASE_URL ?? ctx.appBaseUrl ?? '').replace(/\/+$/, '')
    if (!baseUrl) {
      ctx.log(
        'the enterprise control plane needs to know its own public address, and neither ' +
          'AF_ENTERPRISE_BASE_URL nor AF_APP_BASE_URL is set. Single sign-on would publish ' +
          'assertion consumer URLs pointing nowhere.',
      )
      process.exit(2)
    }
    ctx.log(
      ctx.env.AF_ENTERPRISE_BASE_URL
        ? `enterprise routes publish themselves at ${baseUrl} (AF_ENTERPRISE_BASE_URL)`
        : `enterprise routes publish themselves at ${baseUrl} (from AF_APP_BASE_URL)`,
    )

    registerEnterprise({
      pool: ctx.pool,
      clock: ctx.clock,
      baseUrl,
      appBaseUrl: ctx.appBaseUrl ?? `${baseUrl}/`,
      secureCookies: ctx.secureCookies,
      env: ctx.env,
      log: ctx.log,
    })
  },
})
