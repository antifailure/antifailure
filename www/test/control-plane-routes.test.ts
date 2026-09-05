// The URLs the site calls, pinned to the exact strings it called before they
// became an inventory.
//
// The call sites used to build these by hand, as
// `` fetch(`${CONTROL_PLANE_URL}/v1/applications`) ``, and were changed to read
// them out of lib/control-plane-routes.ts so that tools/routecheck can prove
// the deployed control plane serves every one of them. That refactor is only
// safe if it changed nothing about the request that leaves the browser, and
// "it type checks" does not prove that: a wrong path, a doubled slash or a
// missing segment all compile. So the strings are asserted literally.
//
// It also asserts the properties tools/routecheck depends on and cannot check
// from the outside: that no two entries claim the same route, and that every
// entry carries the probe cost and the reason behind it. A gate reading an
// inventory with a silently duplicated or half-filled entry is a gate checking
// fewer routes than it reports.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { CONTROL_PLANE_ROUTES, controlPlaneUrl } from '../lib/control-plane-routes'
import { CONTROL_PLANE_URL } from '../lib/site'

describe('the control plane URLs the site builds', () => {
  it('are the same absolute URLs the call sites built by hand', () => {
    assert.equal(controlPlaneUrl('applications.create'), `${CONTROL_PLANE_URL}/v1/applications`)
    assert.equal(controlPlaneUrl('leads.create'), `${CONTROL_PLANE_URL}/v1/leads`)
    assert.equal(controlPlaneUrl('site.events'), `${CONTROL_PLANE_URL}/v1/site/events`)
    assert.equal(controlPlaneUrl('auth.github'), `${CONTROL_PLANE_URL}/auth/github`)
  })

  it('point at the production control plane when nothing is configured', () => {
    // The default matters more than it looks. AuthScreen.tsx once held a bare
    // constant reading the staging origin, and every invited person who clicked
    // sign in on the marketing site landed on a deployment with a different
    // OAuth application and a different database.
    if (!process.env.NEXT_PUBLIC_CONTROL_PLANE_URL) {
      assert.equal(controlPlaneUrl('auth.github'), 'https://app.antifailure.dev/auth/github')
    }
  })

  it('never doubles a slash, whatever the origin is spelled like', () => {
    for (const name of Object.keys(CONTROL_PLANE_ROUTES) as (keyof typeof CONTROL_PLANE_ROUTES)[]) {
      const url = controlPlaneUrl(name)
      assert.ok(!url.slice('https://'.length).includes('//'), `${name} produced ${url}`)
      assert.ok(url.startsWith('http'), `${name} produced ${url}`)
    }
  })
})

describe('the inventory tools/routecheck reads', () => {
  it('names each route once, so the gate cannot check fewer than it reports', () => {
    const seen = new Set<string>()
    for (const [name, route] of Object.entries(CONTROL_PLANE_ROUTES)) {
      const key = `${route.method} ${route.path}`
      assert.ok(!seen.has(key), `${key} is declared twice, the second time as ${name}`)
      seen.add(key)
    }
    assert.equal(seen.size, Object.keys(CONTROL_PLANE_ROUTES).length)
  })

  it('says what every probe costs and why', () => {
    for (const [name, route] of Object.entries(CONTROL_PLANE_ROUTES)) {
      assert.ok(
        route.probeEffect === 'inert' || route.probeEffect === 'writes',
        `${name} declares probeEffect ${route.probeEffect}`,
      )
      // The reason is what makes the claim checkable against the API's source.
      // An entry asserting "inert" with nothing behind it is the shape of thing
      // that gets a probe sent at a handler nobody meant to reach.
      assert.ok(route.probeReason.length > 40, `${name} has no real probeReason`)
      assert.ok(route.whenMissing.length > 20, `${name} does not say what breaks`)
      assert.ok(route.calledFrom.endsWith('.tsx') || route.calledFrom.endsWith('.ts'), `${name} calledFrom is ${route.calledFrom}`)
    }
  })

  it('declares only methods a probe can tell apart', () => {
    // The control plane answers 404 both for a path it does not have and for a
    // path it has under a different method. routecheck therefore probes with
    // the method the site itself uses, and only understands these two.
    for (const [name, route] of Object.entries(CONTROL_PLANE_ROUTES)) {
      assert.ok(['GET', 'POST'].includes(route.method), `${name} uses ${route.method}`)
      assert.ok(route.path.startsWith('/'), `${name} has path ${route.path}`)
    }
  })
})
