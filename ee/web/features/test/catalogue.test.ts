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
import { readFile } from 'node:fs/promises'
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

/** The keys of the notShipped map, resolved through the constants. */
function goNotShipped(source: string): string[] {
  const constants = new Map<string, string>()
  for (const line of source.split('\n')) {
    const m = /^\s*(Feature[A-Za-z]+)\s+Feature\s*=\s*"([a-z_]+)"\s*$/.exec(line)
    if (m?.[1] && m[2]) constants.set(m[1], m[2])
  }
  const block = /var notShipped = map\[Feature\]string\{([\s\S]*?)\n\}/.exec(source)
  assert.ok(block?.[1], 'no notShipped map was found in license.go')
  const out: string[] = []
  for (const m of block[1].matchAll(/^\t(Feature[A-Za-z]+):/gm)) {
    const name = constants.get(m[1]!)
    assert.ok(name, `notShipped names ${m[1]}, which is not a Feature constant`)
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
    const refused = goNotShipped(licenseGo)
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
