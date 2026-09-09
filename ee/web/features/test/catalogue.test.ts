// One answer to what a customer is entitled to, held in three places at once.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// THE FEATURE CATALOGUE EXISTS FOUR TIMES IN THIS REPOSITORY and no import
// joins any two of them:
//
//   ee/engine/license/license.go       the Feature constants, which are Go
//   ee/web/features/src/features.ts    this package, which is TypeScript
//   tools/licensegen/main.go           an MIT tool that must not import ee
//   docs/.../licensing.md              what a buyer reads
//
// Three of those four boundaries are structural and permanent. Go cannot import
// TypeScript. The MIT tools module cannot import the enterprise one, which its
// own comment says and which the edition boundary job enforces. Prose cannot
// import anything. So every one of them is a COPY, and a copy with no gate is a
// copy that drifts.
//
// licensegen already held its copy to the Go original by parsing license.go,
// which is the pattern this file extends to the other two ends. Drift here is
// silent in the direction that costs money: a feature added to the licence and
// not to the control plane is a capability nobody can be refused, and one
// removed from the licence and left in the documentation is a page selling
// something that no longer exists.
//
// THE PRECEDENT THIS IS BUILT ON is web/apps/api/test/admin-platform.test.ts,
// which compares the TypeScript MCP tool list to serve.go in BOTH directions
// and caught three registered and unlisted tools the day it was written. One
// direction is not enough and never has been: a check that only fails on a
// missing entry is passed by an extra one, which is the shape every catalogue
// drifts in first.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { readdir, readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { FEATURES, declared, sites, type Feature } from '../src/index.ts'

// Imported for their side effects, which is the entire point. Declaring a site
// happens at module scope in the package that enforces the feature, so a
// package nothing imports declares nothing, and this file importing them is
// what makes the registry a statement about code that exists rather than a
// list somebody maintains.
//
// THE FAILURE THIS AVOIDS is in the engine's own version of this test, which
// asserts that Sites(FeatureCompliance) is EMPTY from a test binary that links
// none of the enforcing packages. It cannot fail. A check that cannot say no is
// worse than no check, and it was living inside the file it was checking.
import '@antifailure-ee/sso'
import '@antifailure-ee/scim'

const here = path.dirname(fileURLToPath(import.meta.url))
const repo = path.resolve(here, '..', '..', '..', '..')

const licenseGo = await readFile(path.join(repo, 'ee/engine/license/license.go'), 'utf8')
const licensegenGo = await readFile(path.join(repo, 'tools/licensegen/main.go'), 'utf8')
const licensingDoc = await readFile(
  path.join(repo, 'docs/src/content/docs/enterprise/licensing.md'), 'utf8')
const issuingDoc = await readFile(
  path.join(repo, 'docs/src/content/docs/enterprise/issuing-licenses.md'), 'utf8')
const entitlementsTs = await readFile(
  path.join(repo, 'web/apps/api/src/entitlements.ts'), 'utf8')

/**
 * The string values of the Feature constants in license.go.
 *
 * A regular expression over a declaration rather than a search for the names,
 * because the names appear in prose in that file more often than they appear as
 * constants, and a check that matched the paragraph explaining the catalogue
 * would pass whatever the catalogue said.
 */
function goFeatures(source: string): string[] {
  const out: string[] = []
  for (const line of source.split('\n')) {
    const m = /^\s*Feature[A-Za-z]+\s+Feature\s*=\s*"([a-z_]+)"\s*$/.exec(line)
    if (m?.[1]) out.push(m[1])
  }
  return out.sort()
}

/**
 * The keys of a named Feature-keyed map in license.go, resolved through the
 * constants.
 *
 * Two maps are read by this file, notShipped and unenforced, and one reader for
 * both is what stops the second from being held by a weaker check than the
 * first. A map name that is not in the file asserts rather than returning
 * empty: "the map is gone" and "the map is empty" are different facts and a
 * reader that answered the same for both could not say no about either.
 */
function goFeatureMap(source: string, mapName: string): string[] {
  const constants = new Map<string, string>()
  for (const line of source.split('\n')) {
    const m = /^\s*(Feature[A-Za-z]+)\s+Feature\s*=\s*"([a-z_]+)"\s*$/.exec(line)
    if (m?.[1] && m[2]) constants.set(m[1], m[2])
  }
  const block = new RegExp(`var ${mapName} = map\\[Feature\\]string\\{([\\s\\S]*?)\\n\\}`).exec(source)
  assert.ok(block?.[1], `no ${mapName} map was found in license.go`)
  const out: string[] = []
  for (const m of block[1].matchAll(/^\t(Feature[A-Za-z]+):/gm)) {
    const name = constants.get(m[1]!)
    assert.ok(name, `${mapName} names ${m[1]}, which is not a Feature constant`)
    out.push(name)
  }
  return out.sort()
}

/** The names inside a fenced-free run of backticked identifiers in a document
 *  section, so a page that lists the catalogue can be compared to it. */
function backticked(section: string): string[] {
  return [...section.matchAll(/`([a-z_]+)`/g)]
    .map((m) => m[1]!)
    .filter((n) => (FEATURES as readonly string[]).includes(n))
}

describe('the feature catalogue is one answer in four places', () => {
  it('this package and the Go licence name exactly the same features', () => {
    const go = goFeatures(licenseGo)
    assert.ok(
      go.length > 0,
      'no Feature constants were read out of license.go, so this test is reading the wrong ' +
        'file or the constants moved, and it would pass whatever the catalogue said',
    )
    // BOTH DIRECTIONS. deepEqual on sorted arrays fails on a name in either one
    // that is not in the other, which is what one-sided containment would miss.
    assert.deepEqual([...FEATURES].sort(), go)
  })

  it('licensegen names exactly the same features', () => {
    // Read out of the tool's own list rather than assumed from the Go
    // constants, because that list is the third copy and it is the one that
    // decides what can actually be signed.
    const block = /var knownFeatures = \[\]string\{([\s\S]*?)\n\}/.exec(licensegenGo)
    assert.ok(block?.[1], 'no knownFeatures list was found in licensegen')
    const names = [...block[1].matchAll(/"([a-z_]+)"/g)].map((m) => m[1]!).sort()
    assert.deepEqual(names, [...FEATURES].sort())
  })

  it('the documentation lists exactly the same features', () => {
    for (const [name, doc, heading] of [
      ['licensing.md', licensingDoc, '## What is in `ee/`'],
      ['issuing-licenses.md', issuingDoc, 'The features are'],
    ] as const) {
      const at = doc.indexOf(heading)
      assert.ok(at >= 0, `${name} no longer contains ${heading}, so nothing was compared`)
      // Bounded to the paragraph that lists them. Reading the whole page would
      // pick up every feature mentioned in prose anywhere on it, which passes
      // for a page that has stopped listing the catalogue at all.
      const listed = new Set(backticked(doc.slice(at, at + 700)))
      const missing = FEATURES.filter((f) => !listed.has(f))
      const extra = [...listed].filter((f) => !(FEATURES as readonly string[]).includes(f))
      assert.deepEqual(missing, [], `${name} does not list ${missing.join(', ')}`)
      assert.deepEqual(extra, [], `${name} lists ${extra.join(', ')}, which is not a feature`)
    }
  })

  it('a feature the licence refuses to permit is named as such in the documentation', () => {
    // The one asymmetry that is allowed and has to be stated. Two names in the
    // catalogue are refused at issue and at evaluation, so a page that lists
    // them without saying so is selling them.
    const refused = goFeatureMap(licenseGo, 'notShipped')
    assert.ok(refused.length > 0, 'nothing is marked unshipped, so this test proves nothing')
    for (const feature of refused) {
      assert.match(
        licensingDoc, new RegExp(`cannot be sold[\\s\\S]*\`${feature}\``),
        `licensing.md lists ${feature} and never says it cannot be sold`,
      )
      assert.match(
        issuingDoc, new RegExp(`cannot be issued[\\s\\S]*\`${feature}\``),
        `issuing-licenses.md does not say ${feature} cannot be issued`,
      )
    }
  })
})

describe('every declared enforcement site names something that can refuse', () => {
  it('the control plane features this edition enforces have declared a site', () => {
    // Not a count of twelve. Most of these are enforced in the engine, where
    // the licence key lives and where this registry cannot see them; asserting
    // twelve here would be asserting something false about a different process.
    // What is asserted is that the features enforced HERE say so.
    assert.deepEqual(
      declared(), ['scim', 'sso'] as Feature[],
      'the set of features enforced in the control plane changed. If one was added, import ' +
        'its package at the top of this file so the registry can see it and add it here. If ' +
        'one disappeared, a declare() call was removed and a feature is silently free again.',
    )
  })

  it('the named symbol is in the named file', async () => {
    // THE FAILURE THIS EXISTS FOR, and it has already shipped once. The engine
    // declared compliance's site as Pack.Evaluate, a method that takes no
    // context and therefore cannot ask the licence anything; the real refusal
    // was seventy lines away in another file. The declaration was true about
    // the feature and false about the symbol, which is the same lie one level
    // down and is invisible to any check that only counts declarations.
    let checked = 0
    for (const feature of declared()) {
      const declaredSites = sites(feature)
      assert.ok(declaredSites.length > 0, `${feature} is declared with no site`)
      for (const site of declaredSites) {
        const [file, symbol] = site.split(':')
        assert.ok(file && symbol, `${feature} has an unreadable site: ${site}`)
        const source = await readFile(path.join(repo, file), 'utf8')
        assert.ok(
          source.includes(symbol),
          `${feature} claims to be enforced at ${site}, and ${file} never defines ${symbol}`,
        )
        // And the symbol has to be somewhere that can actually answer. A site
        // that names a file which never asks the entitlement question is a
        // declaration about the wrong function.
        assert.match(
          source, /licensed\(|licensedIn\(|requireFeature\(/,
          `${file} declares an enforcement site and never asks whether the feature is licensed`,
        )
        checked += 1
      }
    }
    assert.ok(checked >= 3, `only ${checked} sites were checked, and there are at least three`)
  })
})


// ---------------------------------------------------------------------------
// Is every feature accounted for, anywhere in the product
// ---------------------------------------------------------------------------
//
// THE GAP THIS CLOSES, and it is a gap between two checks rather than inside
// either of them. The block above asserts that the features enforced in the
// CONTROL PLANE have declared a site, and it is deliberately not a count of
// twelve, because most of them are enforced in the engine where this registry
// cannot see them. The engine has its own registry and its own test, and that
// test asserts Sites(FeatureCompliance) is EMPTY from a test binary linking
// none of the enforcing packages, so it cannot fail. Between one check that
// correctly declines to look at the engine and one that cannot look at
// anything, nothing in this repository ever asked the whole question: does
// every feature a licence can grant do something, somewhere, in either half.
//
// It was measured on 2026-09-08 and the answer was no. Seven of the twelve are
// enforced, two are refused at issue and at evaluation, and THREE were neither.
// Two of those three had the fact written down in prose. air_gapped had it
// written down nowhere: every occurrence of the name in the repository is a
// copy of the catalogue, the licence vectors, two lines of documentation, or a
// test, and a licence naming it verifies, reports active, prints in af license
// status, and grants nothing.
//
// So the question this asks is a partition. Every feature must be in exactly
// one of three states and each state must be RECORDED, because the whole
// failure is that "enforced somewhere" and "nobody has looked" are the same
// silence:
//
//   refused    in notShipped, so licensegen will not sign it and Evaluate will
//              not permit it
//   unenforced in unenforced, which ships and gates nothing, with the reason
//              stored beside the name
//   enforced   at a site this test can find, in either half of the product
//
// WHY THE SCANNERS CARRY POSITIVE CONTROLS. A scanner that stops matching
// reports no sites, which turns every enforced feature into an unrecorded one
// and fails loudly, so that direction is safe. The dangerous direction is a
// pattern loose enough to match prose, which would quietly reclassify a
// genuinely ungated feature as enforced. Each scanner is therefore asserted to
// find one specific feature it must find, so "I could not look" is a distinct
// answer from "there is nothing there".

/** Every path under a directory, recursively. */
async function filesUnder(dir: string): Promise<string[]> {
  const entries = await readdir(dir, { withFileTypes: true, recursive: true })
  return entries
    .filter((e) => e.isFile())
    .map((e) => path.join(e.parentPath, e.name))
}

/**
 * Features declared at an enforcement site in the enterprise ENGINE.
 *
 * Read out of the source rather than out of the engine's own registry, because
 * that registry is Go and lives in a process this test cannot start. Only
 * `feature.Declare(license.FeatureX` counts, which is the call that records a
 * site, and test files are excluded: a declaration made by a test is a
 * statement about the test binary and not about the product.
 */
async function engineSites(constants: Map<string, string>): Promise<Set<string>> {
  const out = new Set<string>()
  for (const file of await filesUnder(path.join(repo, 'ee/engine'))) {
    if (!file.endsWith('.go') || file.endsWith('_test.go')) continue
    const source = await readFile(file, 'utf8')
    for (const m of source.matchAll(/feature\.Declare\(\s*license\.(Feature[A-Za-z]+)/g)) {
      const name = constants.get(m[1]!)
      assert.ok(name, `${file} declares license.${m[1]}, which is not a Feature constant`)
      out.add(name)
    }
  }
  return out
}

/**
 * Features with a non-null `enforcedAt` in the community control plane's
 * entitlement catalogue.
 *
 * The third authority, and the one neither registry can see. support_access is
 * enforced in MIT code at admin/customers.ts and is therefore absent from the
 * enterprise registry above, which would read as unenforced without this.
 */
function communitySites(source: string): Set<string> {
  const out = new Set<string>()
  for (const m of source.matchAll(/^ {2}([a-z_]+): \{\n([\s\S]*?)\n {2}\},/gm)) {
    const key = m[1]!
    if (!(FEATURES as readonly string[]).includes(key)) continue
    if (/enforcedAt: '[^']+'/.test(m[2]!)) out.add(key)
  }
  return out
}

describe('every feature a licence can grant is accounted for somewhere', () => {
  it('partitions the catalogue into refused, unenforced, and enforced', async () => {
    const constants = new Map<string, string>()
    for (const line of licenseGo.split('\n')) {
      const m = /^\s*(Feature[A-Za-z]+)\s+Feature\s*=\s*"([a-z_]+)"\s*$/.exec(line)
      if (m?.[1] && m[2]) constants.set(m[1], m[2])
    }
    assert.ok(constants.size > 0, 'no Feature constants were read, so nothing below can resolve')

    const refused = new Set(goFeatureMap(licenseGo, 'notShipped'))
    const ungated = new Set(goFeatureMap(licenseGo, 'unenforced'))
    const engine = await engineSites(constants)
    const controlPlane = new Set<string>(declared())
    const community = communitySites(entitlementsTs)

    // The positive controls, one per scanner. Each names a feature that is
    // enforced beyond doubt at a site of that scanner's kind, so a scanner that
    // has stopped seeing its input fails here saying so, rather than passing
    // this test by reporting a feature as ungated.
    assert.ok(
      engine.has('multi_runtime'),
      'the engine scanner found no site for multi_runtime, which is declared in ' +
        'ee/engine/cmd/af/placement.go. The scanner is not reading the engine, so every ' +
        'engine enforced feature below would read as unenforced.',
    )
    assert.ok(
      controlPlane.has('sso'),
      'the control plane registry holds no site for sso. The packages imported at the top ' +
        'of this file are what populate it, so this means an import was dropped.',
    )
    assert.ok(
      community.has('support_access'),
      'the entitlement catalogue scanner found no enforcedAt for support_access, which ' +
        'names admin/customers.ts:supportAccessVerdict. The block pattern no longer ' +
        'matches entitlements.ts, so every community enforced feature would read as ungated.',
    )

    const unaccounted: string[] = []
    const both: string[] = []
    for (const feature of FEATURES) {
      const enforced = engine.has(feature) || controlPlane.has(feature) || community.has(feature)
      const recorded = refused.has(feature) || ungated.has(feature)
      if (!enforced && !recorded) unaccounted.push(feature)
      // Recorded as granting nothing AND enforced somewhere is the other way
      // this can be wrong, and it is the one that goes stale silently: somebody
      // builds the gate and leaves the entry saying there is none.
      if (enforced && recorded) both.push(feature)
    }

    assert.deepEqual(
      unaccounted, [],
      `${unaccounted.join(', ')} can be named in a licence and is enforced nowhere in either ` +
        `half of the product, and nothing records that. A licence naming one verifies, ` +
        `reports active, prints in af license status, and grants nothing, which is ` +
        `indistinguishable from a working feature from the outside. Either gate it at a real ` +
        `site, or add it to notShipped in ee/engine/license/license.go so it cannot be sold, ` +
        `or add it to unenforced there with the reason it is not gated.`,
    )
    assert.deepEqual(
      both, [],
      `${both.join(', ')} is recorded in license.go as granting nothing and is also enforced ` +
        `at a real site. One of the two is now false. If the gate was built, delete the ` +
        `entry; the entry says what building it would mean.`,
    )
  })

  it('the two recorded sets are disjoint and neither is empty', () => {
    const refused = goFeatureMap(licenseGo, 'notShipped')
    const ungated = goFeatureMap(licenseGo, 'unenforced')
    // Empty would make the partition above pass by having nothing to compare,
    // which is the shape of check this repository keeps finding in its own
    // instruments.
    assert.ok(refused.length > 0, 'notShipped is empty, so the partition proves less than it says')
    assert.ok(ungated.length > 0, 'unenforced is empty, so the partition proves less than it says')

    const overlap = refused.filter((f) => ungated.includes(f))
    assert.deepEqual(
      overlap, [],
      `${overlap.join(', ')} is in both notShipped and unenforced. Those are different ` +
        `answers: the first says we did not build it and refuses to sell it, the second ` +
        `says we built it and do not gate it. They cannot both be true.`,
    )
  })

  it('a feature recorded as unenforced is named as such in the documentation', () => {
    // The same rule the refused features already carry, and for the same
    // reason: a page that lists a feature and never says the licence does not
    // grant it is a page selling the licence rather than the capability.
    const ungated = goFeatureMap(licenseGo, 'unenforced')
    assert.ok(ungated.length > 0, 'nothing is recorded as unenforced, so this test proves nothing')
    for (const feature of ungated) {
      assert.match(
        licensingDoc, new RegExp(`not enforced[\\s\\S]*\`${feature}\``),
        `licensing.md lists ${feature} and never says the licence does not enforce it`,
      )
    }
  })
})
