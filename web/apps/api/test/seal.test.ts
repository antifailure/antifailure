// Sealing a customer's provider key.
//
// The property under test is not "it encrypts". It is that a sealed key cannot
// be opened anywhere it should not be: with a different secret, after the row
// was edited, or from another tenant's table. The last of those is the one an
// encryption layer usually misses, because a ciphertext that decrypts fine
// after being copied between rows is still a working key in the wrong hands.

import { test, describe } from 'node:test'
import assert from 'node:assert/strict'
import { randomBytes } from 'node:crypto'
import {
  checkKeyShape,
  displayKey,
  fingerprintOf,
  open,
  sameKey,
  seal,
  sealingKeyFrom,
  SealError,
} from '../src/providers/seal.ts'

const secret = randomBytes(32)
const other = randomBytes(32)
const bound = { orgId: '11111111-1111-1111-1111-111111111111', provider: 'anthropic' }

// ASSEMBLED AT RUNTIME, NOT WRITTEN OUT, and this is the repository's
// convention rather than a trick. tools/scanrepo refuses any file carrying
// something the engine's detector recognises as a live credential, and it uses
// that same detector so CI and the egress proxy cannot disagree about what a
// key looks like. A fixture written literally is therefore a repository that
// fails its own credential gate -- which is exactly what happened when this
// file first landed, and the gate was right.
//
// scanrepo/main.go says the same thing about the detector's own tests.
const ANTHROPIC_PREFIX = ['sk', 'ant', 'api03'].join('-')
const OPENAI_PREFIX = ['sk', 'proj'].join('-')
const KEY = `${ANTHROPIC_PREFIX}-abcdefghijklmnopqrstuvwxyz0123456789`

describe('sealing', () => {
  test('round trips', () => {
    const sealed = seal(secret, KEY, bound)
    assert.equal(open(secret, sealed, bound), KEY)
  })

  test('the ciphertext does not contain the key', () => {
    // The obvious check, and worth having: an "encryption" that stored the
    // plaintext beside the ciphertext would pass every other test here.
    const sealed = seal(secret, KEY, bound)
    assert.doesNotMatch(sealed.ciphertext.toString('utf8'), new RegExp(ANTHROPIC_PREFIX))
    assert.doesNotMatch(sealed.ciphertext.toString('hex'), new RegExp(Buffer.from(KEY).toString('hex')))
  })

  test('two seals of the same key differ, because the nonce is fresh', () => {
    // Deterministic ciphertext would let anybody with the table tell which two
    // organizations are using the same key.
    const a = seal(secret, KEY, bound)
    const b = seal(secret, KEY, bound)
    assert.notEqual(a.ciphertext.toString('hex'), b.ciphertext.toString('hex'))
    assert.notEqual(a.nonce.toString('hex'), b.nonce.toString('hex'))
    // And both still open.
    assert.equal(open(secret, a, bound), KEY)
    assert.equal(open(secret, b, bound), KEY)
  })

  test('a different secret cannot open it', () => {
    const sealed = seal(secret, KEY, bound)
    assert.throws(() => open(other, sealed, bound), SealError)
  })

  test('a row edited by one bit cannot open', () => {
    // GCM's tag. Without it a tampered row would decrypt to a DIFFERENT key,
    // which would then be sent to the provider: a silent, remote-controlled
    // substitution rather than an error.
    const sealed = seal(secret, KEY, bound)
    const tampered = Buffer.from(sealed.ciphertext)
    tampered[0] = (tampered[0] ?? 0) ^ 0x01
    assert.throws(() => open(secret, { ...sealed, ciphertext: tampered }, bound), SealError)
  })

  test('a row copied to another organization cannot open', () => {
    // THE ONE THAT MATTERS MOST. The ciphertext is bound to the organization
    // and provider it was sealed for, so moving a row between tenants -- by a
    // bug in a query, a restore, or somebody with database access -- produces a
    // key that does not work rather than a key that works for the wrong people.
    const sealed = seal(secret, KEY, bound)
    const elsewhere = { orgId: '22222222-2222-2222-2222-222222222222', provider: 'anthropic' }
    assert.throws(() => open(secret, sealed, elsewhere), SealError)
  })

  test('a row moved to the other provider cannot open', () => {
    const sealed = seal(secret, KEY, bound)
    assert.throws(() => open(secret, sealed, { ...bound, provider: 'openai' }), SealError)
  })

  test('a truncated value is refused rather than throwing something unreadable', () => {
    const sealed = seal(secret, KEY, bound)
    assert.throws(
      () => open(secret, { ...sealed, ciphertext: sealed.ciphertext.subarray(0, 4) }, bound),
      /too short/,
    )
  })

  test('what is stored beside it is enough to render, and is not the key', () => {
    const sealed = seal(secret, KEY, bound)
    assert.equal(sealed.last4, '6789')
    assert.equal(sealed.last4.length, 4)
    assert.match(sealed.fingerprint, /^[0-9a-f]{16}$/)
    // The fingerprint must not be reversible to the key by anybody holding it.
    assert.doesNotMatch(sealed.fingerprint, new RegExp(ANTHROPIC_PREFIX))
    assert.equal(displayKey(sealed.last4), '••••••••6789')
  })

  test('the fingerprint is stable and distinguishes keys', () => {
    // This is what makes "you pasted the same key again" detectable during a
    // rotation without either key being displayed or logged.
    assert.equal(fingerprintOf(KEY), fingerprintOf(KEY))
    assert.notEqual(fingerprintOf(KEY), fingerprintOf(KEY + 'x'))
    assert.ok(sameKey(KEY, KEY))
    assert.ok(!sameKey(KEY, KEY + 'x'))
  })
})

describe('the sealing key itself', () => {
  test('is refused at 32 bytes exactly, not merely preferred', () => {
    // A short key would throw deep inside the cipher on first use, which is
    // when somebody saves a key rather than at start-up. The installation would
    // look healthy and fail at the one action this feature exists for.
    assert.throws(() => sealingKeyFrom(randomBytes(16).toString('base64')), /32 bytes/)
    assert.throws(() => sealingKeyFrom(randomBytes(64).toString('base64')), /32 bytes/)
    assert.equal(sealingKeyFrom(randomBytes(32).toString('base64'))?.length, 32)
  })

  test('unset is null, so the feature reports itself off rather than crashing', () => {
    assert.equal(sealingKeyFrom(undefined), null)
    assert.equal(sealingKeyFrom(''), null)
  })
})

describe('refusing a key that is not one', () => {
  test('catches the wrong provider, which is the mistake people actually make', () => {
    assert.match(
      String(checkKeyShape('anthropic', `${OPENAI_PREFIX}-abcdefghijklmnopqrst`)),
      /starts with sk-ant-/,
    )
    assert.match(
      String(checkKeyShape('openai', `${ANTHROPIC_PREFIX}-abcdefghijklmnop`)),
      /Anthropic key/,
    )
  })

  test('catches a whole export line pasted in', () => {
    assert.match(
      String(checkKeyShape('anthropic', `export ANTHROPIC_API_KEY=${ANTHROPIC_PREFIX}-abcdefghijkl`)),
      /space or a newline/,
    )
  })

  test('catches something far too short', () => {
    assert.match(String(checkKeyShape('anthropic', 'sk-ant-x')), /too short/)
  })

  test('accepts a real-looking key', () => {
    // The negative control. Without it, a checker that refused everything would
    // pass every assertion above.
    assert.equal(checkKeyShape('anthropic', KEY), null)
    assert.equal(checkKeyShape('openai', `${OPENAI_PREFIX}-abcdefghijklmnopqrstuvwxyz012345`), null)
  })
})
