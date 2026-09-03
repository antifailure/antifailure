// The free plan numbers the site publishes, against the code that enforces them.
//
// THE CLASS THIS EXISTS FOR. `PLAN_QUOTAS` and `PLAN_COST_CAPS` decide whether
// an organization's next environment is created. Both were enforced and
// published nowhere a customer could read, so the only way to learn the free
// plan's shape was to hit it. Publishing them fixes that once; nothing stops
// them drifting afterwards, and a pricing page that is wrong about a number is
// worse than one that never named it, because somebody plans around it.
//
// legal-facts.test.ts is the same gate over the legal pages and it was written
// after seven published claims were found false in one night. Every one of them
// was true when it was written. This is that lesson applied to the one page
// where a wrong number costs money.
//
// WHAT IT CANNOT SEE. It holds numbers. It cannot hold the sentence beside a
// number: "three environments at once" and "three environments a month" carry
// the same integer and mean different things, and the words around them stay a
// judgement. It also cannot see a limit nobody thought to publish. What it does
// do is fail at the moment the code moves, which is the moment the page becomes
// wrong.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { PLAN_QUOTAS, DEFAULT_PLAN } from '../src/limits.ts'
import { PLAN_COST_CAPS } from '../src/costs.ts'

/**
 * The published file is READ AS TEXT rather than imported, for the reason
 * legal-facts.test.ts gives: www is a separate npm project with its own module
 * resolution, and importing across the boundary compiles here and fails there.
 *
 * The cost of parsing is that a file which stops matching reads as an empty set
 * and every assertion over it passes vacuously. The first test is the negative
 * control on exactly that.
 */
const here = path.dirname(fileURLToPath(import.meta.url))
const repoRoot = path.join(here, '..', '..', '..', '..')
const facts = await readFile(path.join(repoRoot, 'www/lib/plan-facts.ts'), 'utf8')

/** The `value:` of one entry in FREE_PLAN, by its key. */
function published(key: string): number | null {
  const block = facts.match(new RegExp(`\\n  ${key}: \\{[\\s\\S]*?\\n  \\},`, 'm'))
  if (!block) return null
  const found = block[0].match(/value:\s*(\d+(?:\.\d+)?)\s*,/)
  return found ? Number(found[1]) : null
}

describe('the free plan numbers the site publishes', () => {
  it('parses at all, so the assertions below are about the file', () => {
    // Without this every test that follows would pass against a file that had
    // been renamed, reformatted or deleted, which is the failure mode of every
    // gate that reads prose.
    for (const key of ['environments', 'perRunHours', 'perDayHours']) {
      assert.notEqual(
        published(key),
        null,
        `www/lib/plan-facts.ts no longer parses for ${key}. Either the shape changed ` +
          'or the file moved. Fix the parser here rather than deleting the assertion.',
      )
    }
    assert.equal(published('noSuchKey'), null, 'the parser matches anything, so it proves nothing')
  })

  it('names the plan an organization has when it is paying nothing', () => {
    // Every number below is about `free` specifically, and `free` matters only
    // because it is what an organization with no live subscription gets.
    assert.equal(DEFAULT_PLAN, 'free')
  })

  it('publishes the environment quota the control plane enforces', () => {
    // dispatch.ts refuses a creation over this, counting environments whose
    // state is not torn_down.
    assert.equal(published('environments'), PLAN_QUOTAS[DEFAULT_PLAN]!.environments)
  })

  it('publishes both cost caps the control plane enforces', () => {
    const caps = PLAN_COST_CAPS[DEFAULT_PLAN]!
    assert.equal(published('perRunHours'), caps.perRunHours)
    assert.equal(published('perDayHours'), caps.perDayHours)
  })

  it('does not publish a quota nothing enforces', () => {
    // `goldens` and `artifactGigabytes` are declared in PLAN_QUOTAS and are
    // counted for display only: no path refuses a creation over either. If
    // somebody wires enforcement, this test is the note that the page may now
    // say so. Until then, publishing them would be the same defect the rest of
    // this file exists to prevent, pointing the other way.
    for (const unenforced of ['goldens', 'artifactGigabytes']) {
      assert.equal(
        published(unenforced),
        null,
        `www/lib/plan-facts.ts publishes ${unenforced}, which nothing enforces. Either ` +
          'wire the quota into a path that can refuse, or take the number off the page.',
      )
    }
  })
})
