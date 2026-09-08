// The shared licence corpus, read here and emitted by the Go reader.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// WHAT THIS PROVES THAT license.test.ts CANNOT. That file drives this
// implementation against licences it mints itself, so it proves this reader is
// internally consistent. It would have stayed green through every divergence
// between this reader and the engine's, because it never asks the engine
// anything. There are two implementations of one decision here, which this
// repository's own list names as its most expensive recurring defect, and the
// only thing that can catch a drift between two implementations is a corpus
// neither of them owns.
//
// ee/engine/license/vectors_test.go emits ee/license-vectors.json from the Go
// implementation, which license.ts's own header calls the definition. This
// reads the same file and has to reproduce every verdict in it. A change to
// either side that alters a decision now fails a test in one language or the
// other, on the next run, rather than in a customer's installation where one
// binary says a feature is permitted and the other says it is not.
//
// It found exactly that on its first run: this side permitted every feature a
// licence named, and the engine has always filtered the ones no build enforces.
//
// WHAT IT DOES NOT COMPARE, said here rather than left to be assumed checked.
// The verdicts: the refusal, the state, the days left, whether the licence is
// honoured, which features are permitted, and the seat limit as a number. NOT
// the warning sentences, which differ today in how they format a date, this
// side using toUTCString and the Go side "2 January 2006". Both are shown to an
// operator, so it is worth fixing, and it is a difference in prose for two
// surfaces rather than in what either binary permits.

import { describe, it, before } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  ALL_FEATURES,
  NOT_SHIPPED,
  LicenseRefused,
  evaluate,
  parseLicense,
  trustedKeys,
  type Status,
} from '../src/license.ts'

interface SeatProbe {
  current: number
  exceeded: boolean
}

interface Vector {
  name: string
  why: string
  token: string
  org: string
  now: string
  last_seen?: string
  revoked?: string[]
  refusal: string
  state?: string
  days_left: number
  honoured: boolean
  enabled: string[]
  seats?: SeatProbe[]
}

interface Corpus {
  note: string
  keys: string
  all_features: string[]
  not_shipped: string[]
  cases: Vector[]
}

const here = path.dirname(fileURLToPath(import.meta.url))
const corpusPath = path.join(here, '..', '..', '..', 'license-vectors.json')

let corpus: Corpus

before(async () => {
  // Read once, and REFUSED rather than skipped when it is not there. A suite
  // that quietly passes because it could not find the thing it compares against
  // is the failure this repository keeps finding in its own instruments: the
  // absence of an answer read as a satisfied condition.
  let body: string
  try {
    body = await readFile(corpusPath, 'utf8')
  } catch (err) {
    throw new Error(
      `could not read the licence corpus at ${corpusPath}. It is emitted by ` +
        `ee/engine/license/vectors_test.go; regenerate it with ` +
        `'GOWORK=off go test ./license -run TestVectors -update-vectors' from ee/engine. ` +
        `Refusing to skip: a run that proves nothing about a corpus is worse than a red one. ` +
        `Underlying error: ${err instanceof Error ? err.message : String(err)}`,
    )
  }
  corpus = JSON.parse(body) as Corpus
  assert.ok(
    Array.isArray(corpus.cases) && corpus.cases.length > 0,
    `found no cases in ${corpusPath}, so this check is looking in the wrong place or the ` +
      `corpus was emptied. That is a failure, not a pass.`,
  )
  assert.ok(corpus.keys, 'the corpus publishes no keys, so nothing in it can verify')
})

describe('the two licence readers agree', () => {
  it('carries the same list of features as the engine', () => {
    // The list was duplicated across two languages with a comment saying it
    // matched, which is what nothing was checking.
    assert.deepEqual([...ALL_FEATURES].sort(), [...corpus.all_features].sort())
  })

  it('refuses the same features as unshipped', () => {
    assert.deepEqual([...NOT_SHIPPED].sort(), [...corpus.not_shipped].sort())
  })

  it('reproduces every verdict in the corpus', () => {
    const keys = trustedKeys(corpus.keys)
    let checked = 0

    for (const c of corpus.cases) {
      const label = `${c.name}\n  why this case exists: ${c.why}`

      let status: Status | null = null
      let refusal = ''
      try {
        const claims = parseLicense(c.token, keys)
        status = evaluate(claims, {
          org: c.org,
          now: new Date(c.now),
          lastSeen: c.last_seen ? new Date(c.last_seen) : null,
          revoked: new Set(c.revoked ?? []),
        })
      } catch (err) {
        if (!(err instanceof LicenseRefused)) throw err
        refusal = err.refusal
      }

      assert.equal(refusal, c.refusal, `refusal for ${label}`)
      if (c.refusal !== '') {
        checked += 1
        continue
      }
      assert.ok(status, `no status and no refusal for ${label}`)
      assert.equal(status.state, c.state, `state for ${label}`)
      assert.equal(status.daysLeft, c.days_left, `days left for ${label}`)
      assert.equal(status.honoured(), c.honoured, `honoured for ${label}`)
      assert.deepEqual(
        ALL_FEATURES.filter((f) => status!.enabled(f)).sort(),
        [...c.enabled].sort(),
        `permitted features for ${label}`,
      )
      for (const probe of c.seats ?? []) {
        assert.equal(
          status.seatsExceeded(probe.current),
          probe.exceeded,
          `the seat limit at ${probe.current} members for ${label}`,
        )
      }
      checked += 1
    }

    // The count, asserted. A loop over an array that turned out to be empty
    // passes in silence, and this file's whole value is the number of cases it
    // actually compared.
    assert.equal(
      checked,
      corpus.cases.length,
      `compared ${checked} of ${corpus.cases.length} cases, so some were skipped`,
    )
  })

  it('covers every state and every refusal the corpus can carry', () => {
    // The check on the corpus rather than on the implementation, and the mirror
    // of the one the Go side makes. A corpus that quietly stopped carrying a
    // state would pass here for ever while the two sides drifted on exactly the
    // case it dropped.
    const states = new Set(corpus.cases.filter((c) => c.refusal === '').map((c) => c.state))
    const refusals = new Set(corpus.cases.filter((c) => c.refusal !== '').map((c) => c.refusal))

    // 'none' is deliberately absent: it is the answer to having no token at all,
    // and a corpus of tokens cannot carry the case of there being none. Both
    // sides test it directly.
    for (const want of ['active', 'grace', 'expired', 'revoked', 'wrong_org', 'clock_rollback']) {
      assert.ok(states.has(want), `no case in the corpus produces the state ${want}`)
    }
    for (const want of ['malformed', 'tampered', 'unknown_key']) {
      assert.ok(refusals.has(want), `no case in the corpus produces the refusal ${want}`)
    }
  })
})
