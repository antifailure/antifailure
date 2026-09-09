// The other end of the entitlement catalogue, read from the side that cannot
// import it.
//
// There are two entitlement systems in this product. The engine has
// `license.Feature`, a set of names a signed licence may carry, and the control
// plane has `organizations.plan` and `entitlements.ts`, which is about quotas.
// Nothing reconciled them, so "what does this customer get" had two answers and
// a self hosted licence and a hosted plan could disagree in silence.
//
// The single answer now lives in `ee/engine/feature/catalogue.go`, and every
// entry that says the control plane does something names the file and the
// symbol. The Go suite checks those claims. This file checks them again from
// here, and the duplication is the point rather than an oversight: a control
// plane developer renaming `orgProcedure` runs this suite and does not run a
// Go module that is deliberately outside the workspace, so a check that lived
// only over there would go unread until CI, or until a customer found it.
//
// `admin-platform.test.ts` is the precedent and the shape is copied from it: it
// compares the TypeScript tool list against `serve.go` in BOTH directions and
// caught three registered but unlisted tools the day it was written.
//
// THE PARSES ARE GUARDED. Every regex below is checked for having matched
// something before its result is used. A parse that quietly matches nothing
// passes every assertion made from it and reports success about a check it
// never made, which is the exact defect this repository keeps finding in its
// own instruments.

import { test, describe } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
/** The repository root: test, api, apps, web. */
const root = path.resolve(here, '..', '..', '..', '..')
const apiSrc = path.join(root, 'web', 'apps', 'api', 'src')
const licenseGo = path.join(root, 'ee', 'engine', 'license', 'license.go')
const catalogueGo = path.join(root, 'ee', 'engine', 'feature', 'catalogue.go')

/** `FeatureSSO Feature = "sso"` from the engine's own constant block. */
async function licensedFeatures(): Promise<Map<string, string>> {
  const source = await readFile(licenseGo, 'utf8')
  const found = new Map<string, string>()
  for (const m of source.matchAll(/(Feature\w+)\s+Feature\s+=\s+"([a-z_]+)"/g)) {
    found.set(m[1] ?? '', m[2] ?? '')
  }
  assert.ok(
    found.size >= 10,
    `parsed ${found.size} feature constants out of ${licenseGo}, which means the const block ` +
      `moved and every assertion below is being made about nothing`,
  )
  return found
}

interface Entry {
  constant: string
  state: string
  controlPlaneAt: string | null
}

/**
 * The catalogue, read out of the Go literal.
 *
 * Split on `Feature: license.` because that field opens every entry, so an
 * entry that gained a field or lost one still parses. The count is checked
 * against the constant block, which is what makes a parse that drifts fail
 * loudly rather than silently returning a shorter list.
 */
async function catalogue(): Promise<Entry[]> {
  const source = await readFile(catalogueGo, 'utf8')
  const body = source.slice(source.indexOf('var catalogue = []Entitlement{'))
  assert.ok(body.length > 0, `${catalogueGo} declares no catalogue variable`)

  const entries: Entry[] = []
  const chunks = body.split(/\n\t\tFeature:\s+license\./).slice(1)
  for (const chunk of chunks) {
    // `?? ''` rather than a non-null assertion after assert.ok, because the
    // compiler is the second reader here and narrowing through an assertion
    // function is a rule with edges. An empty string fails the check below and
    // says which entry, which is what a reader needs either way.
    const constant = chunk.match(/^(Feature\w+)/)?.[1] ?? ''
    assert.notEqual(constant, '', 'a catalogue entry does not open with a license.Feature constant')
    const state = chunk.match(/State:\s+State(\w+)/)?.[1] ?? ''
    assert.notEqual(state, '', `${constant} has no State`)
    entries.push({
      constant,
      state: state.toLowerCase(),
      controlPlaneAt: chunk.match(/ControlPlaneAt:\s+"([^"]+)"/)?.[1] ?? null,
    })
  }
  return entries
}

describe('the entitlement catalogue and this control plane still agree', () => {
  test('every feature a licence can carry has an entry, so nothing is unaccounted for', async () => {
    // The direction that catches a lane adding a feature to the engine and
    // nowhere else, which is how `air_gapped` became a word that means nothing:
    // it reached the const block, two documentation pages and licensegen, and
    // no code anywhere asks whether it is on. This suite fails on the next one
    // even though it is a Go change, because the control plane is the other
    // half of the answer and has to be told.
    const features = await licensedFeatures()
    const entries = await catalogue()
    const covered = new Set(entries.map((e) => e.constant))

    for (const [constant, wire] of features) {
      assert.ok(
        covered.has(constant),
        `the licence sells ${wire} and ee/engine/feature/catalogue.go has no entry for it, ` +
          `so nobody has written down what a customer without it gets`,
      )
    }
    assert.equal(
      entries.length,
      features.size,
      'the catalogue and the constant block are different lengths',
    )
  })

  test('every control plane site the catalogue names is still in this source tree', async () => {
    // The file as well as the symbol, for the reason admin/controls.ts gives
    // about enforcedBy: a bare name proves only that SOME file declares one,
    // and enforcement moving into a module nothing calls would still satisfy
    // that.
    const entries = await catalogue()
    const named = entries.filter((e) => e.controlPlaneAt !== null)
    assert.ok(
      named.length > 0,
      'no catalogue entry names a control plane site, so this test is checking nothing',
    )

    for (const entry of named) {
      const [file = '', symbol = ''] = (entry.controlPlaneAt ?? '').split(':')
      assert.ok(file !== '' && symbol !== '',
        `${entry.constant} has ControlPlaneAt ${entry.controlPlaneAt}, which is not file:symbol`)
      const source = await readFile(path.join(root, file), 'utf8')
      assert.match(
        source,
        new RegExp(`\\b${symbol}\\b`),
        `${entry.constant} says ${entry.controlPlaneAt} is where this lives in the control ` +
          `plane, and ${file} declares no ${symbol}`,
      )
    }
  })
})

describe('the claims the catalogue makes about billing, the dashboard and the plan gate', () => {
  // These are prose in the catalogue and behaviour here, and prose that
  // describes behaviour is worth nothing until something checks it. Three
  // people in this repository once agreed a thing was cross site when
  // SameSite=Strict had already made it same site only. A claim needs its
  // mitigation checked, not just described.
  //
  // THE FIRST VERSION OF THIS BLOCK CHECKED THE PREVIOUS ANSWER AND STAYED
  // GREEN, which is worth leaving written down because it is the same defect
  // the whole lane is about. It held two tests named "billing is free because
  // the plan gate exempts it" and "the dashboard is plan wide", against rows
  // that now read absent, through a state called plan_wide that has since been
  // deleted. Both passed, because both asserted true things about hosted.ts and
  // trpc.ts. What had gone false was the REASON, and a green test whose failure
  // message would teach the next person a deleted classification is worse than
  // no test at all.

  test('neither names a control plane site, because the code that looked like theirs serves us', async () => {
    // The fact both rows assert in their own words, and the exact regression
    // that produced the original error. billing read free because billingRouter
    // is real, mounted and ungated, and that code bills the customer FOR
    // Antifailure rather than metering on the customer's own behalf.
    // enterprise_dashboard read as covered by the plan because orgProcedure
    // really does refuse the console below the enterprise tier, which is our
    // own funnel enforcing our own pricing. Putting either path back into
    // ControlPlaneAt is how the mistake gets made again, so it fails here.
    const entries = await catalogue()
    for (const constant of ['FeatureBilling', 'FeatureDashboard']) {
      const entry = entries.find((e) => e.constant === constant)
      assert.ok(entry, `the catalogue has no entry for ${constant}, so this test proves nothing`)
      assert.equal(
        entry.state, 'absent',
        `the catalogue calls ${constant} ${entry.state}. It is named in license.go's ` +
          'notShipped map, which is the authority on what cannot be sold at all, and a ' +
          'catalogue that calls it anything else is describing a capability that does not exist.',
      )
      assert.equal(
        entry.controlPlaneAt, null,
        `the catalogue says ${constant} is implemented at ${entry.controlPlaneAt} in the ` +
          'control plane. That field means where the control plane implements or refuses ' +
          'THIS feature, and naming code that serves us rather than the customer is the ' +
          'precise mistake both of these rows were corrected for.',
      )
    }
  })

  test('the console refusal that misled the catalogue is still real and still keyed on the plan', async () => {
    // The other half of the enterprise_dashboard row, and it is a claim about
    // this tree rather than about the catalogue. The row says the refusal that
    // exists here is our own funnel, keyed on organizations.plan and knowing
    // nothing about the twelve names a licence carries. If trpc.ts stopped
    // asking the plan, that sentence would be describing something that is gone
    // and the row would need rewriting for the opposite reason it was rewritten
    // last time.
    const source = await readFile(path.join(apiSrc, 'trpc.ts'), 'utf8')
    assert.match(
      source,
      /hasHostedAccess\(/,
      'the catalogue says the console refusal is keyed on the plan and trpc.ts no longer ' +
        'calls hasHostedAccess, so what the console is gated on has changed',
    )
    assert.match(
      source,
      /HOSTED_GATE_EXEMPT\.has\(/,
      'trpc.ts no longer consults HOSTED_GATE_EXEMPT, so the plan gate the catalogue ' +
        'describes is not the gate applied on the request path',
    )
  })

  test('no licence feature name has become a string this control plane branches on', async () => {
    // The drift the lane is named for, in the direction nothing else watches: a
    // second entitlement catalogue growing over here.
    //
    // Deliberately narrow. `'sso'` is a sign in method label in server.ts and
    // `'billing'` is an admin role in admin/permissions.ts, and both are
    // ordinary English words that happen to collide with a feature name. So the
    // assertion is not that the strings are absent, it is that no single file
    // holds THREE or more of them, which is what a copy of the licence's list
    // looks like and what an incidental collision never does.
    const features = await licensedFeatures()
    const wire = [...features.values()]
    const { readdir } = await import('node:fs/promises')

    async function* walk(dir: string): AsyncGenerator<string> {
      for (const item of await readdir(dir, { withFileTypes: true })) {
        const full = path.join(dir, item.name)
        if (item.isDirectory()) yield* walk(full)
        else if (item.name.endsWith('.ts')) yield full
      }
    }

    let scanned = 0
    for await (const file of walk(apiSrc)) {
      scanned += 1
      const source = await readFile(file, 'utf8')
      const present = wire.filter((n) => new RegExp(`(['"])${n}\\1`).test(source))
      assert.ok(
        present.length < 3,
        `${path.relative(root, file)} names ${present.join(', ')} as string literals, which ` +
          `is a copy of the licence's feature list. There is one catalogue and it is ` +
          `ee/engine/feature/catalogue.go; a second one here is what this test exists to stop.`,
      )
    }
    assert.ok(scanned > 50, `walked ${scanned} TypeScript files under src, which is too few`)
  })
})
