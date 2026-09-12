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
  CannotOpenError,
  checkKeyShape,
  displayKey,
  FIRST_KEY_VERSION,
  fingerprintOf,
  Keyring,
  keyringFrom,
  MissingSealingKeyError,
  open,
  resealValue,
  sameKey,
  seal,
  sealingKeyFrom,
  SealError,
} from '../src/providers/seal.ts'

const secretKey = randomBytes(32)
const otherKey = randomBytes(32)
const secret = Keyring.of(secretKey)
const other = Keyring.of(otherKey)
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
    // Same version, different bytes. This is the shape a rotation that replaced
    // the secret in place produces, and the message has to say the key IS
    // configured for that version so nobody goes looking for a missing one.
    assert.throws(() => open(other, sealed, bound), CannotOpenError)
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

// ---------------------------------------------------------------------------
// The keyring, which is what makes a rotation possible at all
// ---------------------------------------------------------------------------

const K1 = randomBytes(32)
const K2 = randomBytes(32)
const b64 = (b: Buffer) => b.toString('base64')

/** The error a call threw, or a failure saying it threw nothing.
 *
 *  assert.throws returns void in the type definitions, and several assertions
 *  below are about the MESSAGE and the CLASS of one error rather than the mere
 *  fact of it, which is the distinction this whole file turns on. */
function caught(fn: () => unknown): Error {
  try {
    fn()
  } catch (err) {
    return err as Error
  }
  assert.fail('it did not throw')
}

describe('reading the sealing keys from the environment', () => {
  test('one key on its own is version v1, which is what every existing row says', () => {
    // The compatibility property, and the reason this is a merge rather than a
    // replacement: an installation that has never rotated sets nothing new and
    // its rows keep opening.
    const ring = keyringFrom({ AF_PROVIDER_KEY_SECRET: b64(K1) })
    assert.ok(ring)
    assert.deepEqual(ring.versions(), [FIRST_KEY_VERSION])
    assert.equal(ring.current, FIRST_KEY_VERSION)
  })

  test('the plural form takes version=key, the same grammar as AF_LICENSE_PUBLIC_KEYS', () => {
    const ring = keyringFrom({
      AF_PROVIDER_KEY_SECRETS: `v1=${b64(K1)},v2=${b64(K2)}`,
      AF_PROVIDER_KEY_VERSION: 'v2',
    })
    assert.ok(ring)
    assert.deepEqual(ring.versions(), ['v1', 'v2'])
    assert.equal(ring.current, 'v2')
  })

  test('both variables are merged, so the old key stays where Terraform put it', () => {
    // THE PROPERTY THE HOSTED RUNBOOK RESTS ON. The operator adds one new vault
    // secret holding v2 and never reads the old value out of the vault to
    // compose a combined string, which is the step that would have put a live
    // sealing key on somebody's terminal.
    const ring = keyringFrom({
      AF_PROVIDER_KEY_SECRET: b64(K1),
      AF_PROVIDER_KEY_SECRETS: `v2=${b64(K2)}`,
      AF_PROVIDER_KEY_VERSION: 'v2',
    })
    assert.ok(ring)
    assert.deepEqual(ring.versions(), ['v1', 'v2'])
    assert.equal(ring.current, 'v2')
  })

  test('the same key under v1 in both places is not a conflict', () => {
    const ring = keyringFrom({
      AF_PROVIDER_KEY_SECRET: b64(K1),
      AF_PROVIDER_KEY_SECRETS: `v1=${b64(K1)}`,
    })
    assert.ok(ring)
    assert.deepEqual(ring.versions(), ['v1'])
  })

  test('two different keys under one version is refused, because there is no safe choice', () => {
    // Whichever one were chosen, half the rows filed under that version were
    // sealed with the other. This is the misconfiguration a merge introduces
    // and the reason the merge refuses rather than preferring one side.
    assert.throws(
      () => keyringFrom({ AF_PROVIDER_KEY_SECRET: b64(K1), AF_PROVIDER_KEY_SECRETS: `v1=${b64(K2)}` }),
      /two different sealing keys are configured for version "v1"/,
    )
  })

  test('several keys and no stated version is refused rather than guessed', () => {
    const err = caught(() => keyringFrom({ AF_PROVIDER_KEY_SECRETS: `v1=${b64(K1)},v2=${b64(K2)}` }))
    assert.ok(err instanceof SealError)
    assert.match(String(err), /AF_PROVIDER_KEY_VERSION does not say which one/)
    assert.match(String(err), /v1, v2/)
  })

  test('a stated version nobody holds is refused, and says what is held', () => {
    assert.throws(
      () => keyringFrom({ AF_PROVIDER_KEY_SECRET: b64(K1), AF_PROVIDER_KEY_VERSION: 'v2' }),
      /AF_PROVIDER_KEY_VERSION is "v2" and no sealing key is configured for that version/,
    )
  })

  test('a version with no keys at all is a half-applied configuration, not a feature that is off', () => {
    assert.throws(
      () => keyringFrom({ AF_PROVIDER_KEY_VERSION: 'v2' }),
      /no sealing key is configured/,
    )
  })

  test('nothing set is null, so the feature reports itself off rather than crashing', () => {
    assert.equal(keyringFrom({}), null)
    assert.equal(keyringFrom({ AF_PROVIDER_KEY_SECRET: '', AF_PROVIDER_KEY_SECRETS: '' }), null)
  })

  test('an entry that is not version=key is refused', () => {
    assert.throws(() => keyringFrom({ AF_PROVIDER_KEY_SECRETS: b64(K1) }), /not version=key/)
    assert.throws(() => keyringFrom({ AF_PROVIDER_KEY_SECRETS: `=${b64(K1)}` }), /not version=key/)
  })

  test('a version label that is not a version is refused before it reaches the associated data', () => {
    // The label is part of what authenticates a ciphertext, so a surprising byte
    // here is a row that stops opening. It is also quoted in a message an
    // operator reads out of a log.
    assert.throws(
      () => keyringFrom({ AF_PROVIDER_KEY_SECRETS: `V2=${b64(K2)}` }),
      /is not a sealing key version/,
    )
    assert.throws(
      () => keyringFrom({ AF_PROVIDER_KEY_SECRETS: `my key=${b64(K2)}` }),
      /is not a sealing key version/,
    )
  })

  test('a truncated or padded key is refused rather than silently decoding short', () => {
    // Buffer.from(x, 'base64') drops what it does not recognise, so a value with
    // a stray character decodes to fewer bytes instead of failing. Refusing a
    // non-canonical encoding is what makes the length check catch that.
    assert.throws(() => keyringFrom({ AF_PROVIDER_KEY_SECRET: b64(K1).slice(0, 20) }), /32 bytes/)
    assert.throws(() => keyringFrom({ AF_PROVIDER_KEY_SECRET: `"${b64(K1)}"` }), /not valid base64/)
  })

  test('the start-up line names the versions and no key material', () => {
    // The one check a rotation can otherwise not make: whether a new revision
    // actually picked the new key up. Proving it by decrypting somebody's
    // credential is not an option, so the log says which versions are held.
    const ring = keyringFrom({
      AF_PROVIDER_KEY_SECRETS: `v1=${b64(K1)},v2=${b64(K2)}`,
      AF_PROVIDER_KEY_VERSION: 'v2',
    })!
    const line = ring.summary()
    assert.match(line, /2 sealing keys \(v1, v2\), sealing under v2/)
    assert.ok(!line.includes(b64(K1)))
    assert.ok(!line.includes(b64(K2)))
    assert.ok(!line.includes(K1.toString('hex')))
  })
})

describe('more than one sealing key open at once', () => {
  const v1only = Keyring.of(K1, 'v1')
  const v2only = Keyring.of(K2, 'v2')
  const both = Keyring.from([['v1', K1], ['v2', K2]], 'v2')

  test('a row sealed under the old key still opens while the new one is current', () => {
    // THE POINT OF THE WHOLE LANE. Adding a key must not stop the rows that
    // were there from opening.
    const sealed = seal(v1only, KEY, bound)
    assert.equal(sealed.keyVersion, 'v1')
    assert.equal(open(both, sealed, bound), KEY)
  })

  test('new rows are sealed under the stated current version, not the oldest held', () => {
    const sealed = seal(both, KEY, bound)
    assert.equal(sealed.keyVersion, 'v2')
    assert.equal(open(both, sealed, bound), KEY)
  })

  test('the associated data carries the version, so a relabelled row does not open', () => {
    // Two versions holding the SAME bytes, so the only difference between them
    // is the label. The row still refuses, which is the proof that the version
    // is authenticated rather than decorative, and the reason every reader has
    // to build the associated data from the row rather than from the process.
    const twice = Keyring.from([['v1', K1], ['v2', K1]], 'v1')
    const sealed = seal(twice, KEY, bound)
    assert.throws(() => open(twice, { ...sealed, keyVersion: 'v2' }, bound), CannotOpenError)
  })

  test('sealing under a version the keyring does not hold is refused', () => {
    assert.throws(() => v1only.sealingUnder('v2'), /no sealing key is configured/)
  })
})

describe('a missing sealing key is not a tampered row', () => {
  // THE HALF OF THIS THAT MATTERS MOST. Before these two errors were different,
  // an operator who replaced the sealing secret saw "the stored value has been
  // altered" on every customer's key, which sends whoever reads it looking for
  // tampering, a bad restore, or a bug in a query. The fact is a configuration.
  const v1only = Keyring.of(K1, 'v1')
  const v2only = Keyring.of(K2, 'v2')

  test('a row whose version is not held reports the version, by name', () => {
    const sealed = seal(v1only, KEY, bound)
    const err = caught(() => open(v2only, sealed, bound))
    assert.ok(err instanceof MissingSealingKeyError)
    assert.equal(err.keyVersion, 'v1')
    assert.deepEqual(err.held, ['v2'])
    assert.match(err.message, /sealed under sealing key version "v1", which this control plane does not hold/)
    assert.match(err.message, /It holds v2\./)
    assert.match(err.message, /AF_PROVIDER_KEY_SECRETS/)
  })

  test('and it never says the row was altered, which is the sentence that misled', () => {
    const sealed = seal(v1only, KEY, bound)
    const err = caught(() => open(v2only, sealed, bound))
    assert.ok(err instanceof MissingSealingKeyError)
    assert.doesNotMatch(err.message, /altered|tamper/i)
    assert.match(err.message, /This is a configuration rather than a damaged row/)
  })

  test('a genuinely tampered row under a held version still reports tampering', () => {
    // The negative control, and the one that stops this being a rename. If every
    // failure became "missing key" the message would be worse than the one it
    // replaced, because it would send an operator to add a key they already have.
    const sealed = seal(v1only, KEY, bound)
    const tampered = Buffer.from(sealed.ciphertext)
    tampered[0] = (tampered[0] ?? 0) ^ 0x01
    const err = caught(() => open(v1only, { ...sealed, ciphertext: tampered }, bound))
    assert.ok(err instanceof CannotOpenError)
    assert.ok(!(err instanceof MissingSealingKeyError))
    assert.match(err.message, /has been altered/)
    assert.match(err.message, /IS configured here, so this is not a missing key/)
  })

  test('a row copied between tenants reports tampering, not a missing key', () => {
    const sealed = seal(v1only, KEY, bound)
    const elsewhere = { orgId: '22222222-2222-2222-2222-222222222222', provider: 'anthropic' }
    const err = caught(() => open(v1only, sealed, elsewhere))
    assert.ok(err instanceof CannotOpenError)
    assert.ok(!(err instanceof MissingSealingKeyError))
  })

  test('both are still SealError, so nothing that handled one failure breaks', () => {
    const sealed = seal(v1only, KEY, bound)
    assert.throws(() => open(v2only, sealed, bound), SealError)
    assert.throws(() => open(other, { ...sealed, keyVersion: 'v1' }, bound), SealError)
  })

  test('neither message carries key material', () => {
    const sealed = seal(v1only, KEY, bound)
    for (const thrown of [
      caught(() => open(v2only, sealed, bound)),
      caught(() => open(Keyring.of(K2, 'v1'), sealed, bound)),
    ]) {
      assert.ok(!thrown.message.includes(K1.toString('base64')))
      assert.ok(!thrown.message.includes(K2.toString('base64')))
      assert.ok(!thrown.message.includes(KEY))
      assert.ok(!thrown.message.includes(sealed.ciphertext.toString('base64')))
    }
  })
})

describe('re-sealing one value', () => {
  const both = Keyring.from([['v1', K1], ['v2', K2]], 'v2')

  test('opens under its own version and seals under the new one', () => {
    const sealed = seal(Keyring.of(K1, 'v1'), KEY, bound)
    const again = resealValue(both, sealed, bound, 'v2')
    assert.equal(again.keyVersion, 'v2')
    assert.equal(open(both, again, bound), KEY)
    assert.equal(again.fingerprint, sealed.fingerprint)
    assert.equal(again.last4, sealed.last4)
  })

  test('the re-sealed value needs the new key, which is what completing a rotation means', () => {
    const sealed = seal(Keyring.of(K1, 'v1'), KEY, bound)
    const again = resealValue(both, sealed, bound, 'v2')
    assert.throws(() => open(Keyring.of(K1, 'v1'), again, bound), MissingSealingKeyError)
    assert.equal(open(Keyring.of(K2, 'v2'), again, bound), KEY)
  })

  test('a fingerprint that does not match refuses, rather than writing a value it cannot vouch for', () => {
    const sealed = seal(Keyring.of(K1, 'v1'), KEY, bound)
    assert.throws(
      () => resealValue(both, { ...sealed, fingerprint: 'deadbeefdeadbeef' }, bound, 'v2'),
      /does not match the fingerprint stored beside it/,
    )
  })

  test('a row whose key is missing refuses with the missing key error, not a corrupted write', () => {
    const sealed = seal(Keyring.of(K1, 'v3'), KEY, bound)
    assert.throws(() => resealValue(both, sealed, bound, 'v2'), MissingSealingKeyError)
  })
})
