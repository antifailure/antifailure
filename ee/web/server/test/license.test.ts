// The licence reader, and the seam that must not be half-installed.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// entrypoint.test.ts proves the gate over a socket, which is the claim that
// matters, and it can only afford a handful of cases because each one is a
// process. This file is the cheap half: the refusals a token can produce, and
// the states a valid one moves through, driven directly.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { generateKeyPairSync, randomUUID, sign } from 'node:crypto'
import { clearExtensions, hasSignInPolicy, registeredExtensions, setSignInPolicy, FakeClock } from '@antifailure/api'
import {
  LicenseRefused,
  evaluate,
  licenseFromEnv,
  none,
  parseLicense,
  trustedKeys,
} from '../src/license.ts'
import { registerEnterprise } from '../src/register.ts'

function key(): { kid: string; publicKeys: string; token: (claims: object) => string; other: string } {
  const kid = `k-${randomUUID().slice(0, 8)}`
  const { publicKey, privateKey } = generateKeyPairSync('ed25519')
  const raw = publicKey.export({ format: 'der', type: 'spki' }).subarray(12)
  const impostor = generateKeyPairSync('ed25519')
  return {
    kid,
    publicKeys: `${kid}=${raw.toString('base64')}`,
    token: (claims) => {
      const payload = Buffer.from(JSON.stringify({ kid, ...claims }), 'utf8')
      return `aflic_${payload.toString('base64url')}.${sign(null, payload, privateKey).toString('base64url')}`
    },
    // A token signed by a key that is real and is not ours, which is the shape
    // of somebody minting their own licences rather than of a corrupted one.
    other: (() => {
      const payload = Buffer.from(JSON.stringify({ kid, org: 'acme', expires_at: '2030-01-01T00:00:00Z' }), 'utf8')
      return `aflic_${payload.toString('base64url')}.${sign(null, payload, impostor.privateKey).toString('base64url')}`
    })(),
  }
}

const future = '2030-01-01T00:00:00Z'

describe('parsing a license key', () => {
  it('accepts one this installation trusts', () => {
    const k = key()
    const claims = parseLicense(k.token({ org: 'acme', features: ['sso'], expires_at: future }), trustedKeys(k.publicKeys))
    assert.equal(claims.org, 'acme')
    assert.deepEqual(claims.features, ['sso'])
  })

  it('accepts one pasted out of an email with newlines in it', () => {
    // Refusing whitespace is a support ticket rather than a security property,
    // and the Go side tolerates it for the same reason.
    const k = key()
    const token = k.token({ org: 'acme', expires_at: future })
    const wrapped = `${token.slice(0, 20)}\n  ${token.slice(20)}\n`
    assert.equal(parseLicense(wrapped, trustedKeys(k.publicKeys)).org, 'acme')
  })

  it('refuses a signature that does not verify', () => {
    const k = key()
    assert.throws(
      () => parseLicense(k.other, trustedKeys(k.publicKeys)),
      (err: unknown) => err instanceof LicenseRefused && err.refusal === 'tampered',
    )
  })

  it('refuses a payload edited after signing', () => {
    // The one that matters commercially: adding a feature to a licence you
    // already hold. The claims are re-serialised, so the signature is over
    // different bytes.
    const k = key()
    const token = k.token({ org: 'acme', features: ['sso'], expires_at: future })
    const [body, signature] = token.slice('aflic_'.length).split('.')
    const edited = JSON.parse(Buffer.from(body!, 'base64url').toString('utf8')) as Record<string, unknown>
    edited.features = ['sso', 'scim', 'air_gapped']
    const forged = `aflic_${Buffer.from(JSON.stringify(edited), 'utf8').toString('base64url')}.${signature}`
    assert.throws(
      () => parseLicense(forged, trustedKeys(k.publicKeys)),
      (err: unknown) => err instanceof LicenseRefused && err.refusal === 'tampered',
    )
  })

  it('refuses a key this installation does not carry, and says that rather than tampered', () => {
    // Two different problems and two different places to send somebody. A build
    // with no keys told to look for tampering is a support ticket that ends in
    // embarrassment.
    const k = key()
    assert.throws(
      () => parseLicense(k.token({ org: 'acme', expires_at: future }), trustedKeys(undefined)),
      (err: unknown) =>
        err instanceof LicenseRefused && err.refusal === 'unknown_key' && /carries no licence signing keys/.test(err.message),
    )
  })

  it('refuses a token that is not a license at all', () => {
    const k = key()
    const keys = trustedKeys(k.publicKeys)
    for (const bad of ['', 'hello', 'aflic_', 'aflic_abc', 'aflic_.abc', 'aflic_abc.', 'aflic_!!!.!!!']) {
      assert.throws(
        () => parseLicense(bad, keys),
        (err: unknown) => err instanceof LicenseRefused && err.refusal === 'malformed',
        `${JSON.stringify(bad)} was not refused as malformed`,
      )
    }
  })

  it('refuses one that names no organization, so a key cannot be passed around', () => {
    const k = key()
    assert.throws(
      () => parseLicense(k.token({ expires_at: future }), trustedKeys(k.publicKeys)),
      (err: unknown) => err instanceof LicenseRefused && /names no organization/.test(err.message),
    )
  })

  it('carries a feature name it has never heard of without refusing the license', () => {
    // The set is closed at issue time, not here. A licence issued for a newer
    // release names features an older build has not heard of, and refusing the
    // whole licence over one would turn every ordering of upgrade and renewal
    // into an outage for features the customer did buy.
    //
    // The unknown name lands in the permitted set, exactly as it does in the Go
    // verifier, whose loop is `for _, f := range claims.Features`. That is
    // parity rather than an oversight, and it is deliberately not "improved"
    // here: two implementations of one decision that differ in a corner nobody
    // observes is how they start differing in one somebody does. Nothing can
    // ask, because Feature is a closed union on this side and a closed const
    // set on that one, so the only reachable answers are the ones below.
    //
    // Worth writing down, because ee/engine/license's own doc comment says the
    // verifier "simply never permits" an unknown name, and both implementations
    // would permit it if anything could ask. Unobservable today, and untrue as
    // written.
    const k = key()
    const claims = parseLicense(
      k.token({ org: 'acme', features: ['sso', 'time_travel'], expires_at: future }),
      trustedKeys(k.publicKeys),
    )
    const status = evaluate(claims, { org: 'acme', now: new Date('2027-01-01T00:00:00Z') })
    assert.equal(status.enabled('sso'), true)
    assert.equal(status.enabled('scim'), false)
    assert.deepEqual(claims.features, ['sso', 'time_travel'])
  })
})

describe('what a license is doing right now', () => {
  const k = key()
  const claims = (over: Record<string, unknown> = {}) =>
    parseLicense(
      k.token({ id: 'lic-1', org: 'acme', features: ['sso', 'scim'], expires_at: future, ...over }),
      trustedKeys(k.publicKeys),
    )

  it('is active before expiry and permits what it names', () => {
    const status = evaluate(claims(), { org: 'acme', now: new Date('2029-01-01T00:00:00Z') })
    assert.equal(status.state, 'active')
    assert.equal(status.enabled('sso'), true)
    assert.equal(status.honoured(), true)
  })

  it('keeps working through the grace period rather than stopping', () => {
    // Nobody's sign-in breaks because a purchase order moved slowly.
    const status = evaluate(claims(), { org: 'acme', now: new Date('2030-01-05T00:00:00Z') })
    assert.equal(status.state, 'grace')
    assert.equal(status.enabled('sso'), true)
    // Fourteen days of grace from 1 January, read on the fifth, so ten remain.
    assert.match(status.warning, /keep working for 10 more days/)
  })

  it('permits nothing once the grace period has ended', () => {
    const status = evaluate(claims(), { org: 'acme', now: new Date('2030-02-01T00:00:00Z') })
    assert.equal(status.state, 'expired')
    assert.equal(status.enabled('sso'), false)
    assert.match(status.warning, /every enterprise setting is preserved/)
  })

  it('refuses a license issued to somebody else', () => {
    const status = evaluate(claims(), { org: 'other', now: new Date('2029-01-01T00:00:00Z') })
    assert.equal(status.state, 'wrong_org')
    assert.equal(status.enabled('sso'), false)
  })

  it('refuses when the clock has moved backwards past a time it was seen at', () => {
    // Offline verification is only as good as the clock, and moving it back is
    // how an expired licence is made to look current.
    const status = evaluate(claims(), {
      org: 'acme',
      now: new Date('2029-01-01T00:00:00Z'),
      lastSeen: new Date('2029-06-01T00:00:00Z'),
    })
    assert.equal(status.state, 'clock_rollback')
    assert.equal(status.enabled('sso'), false)
  })

  it('tolerates an hour of clock movement, because a resumed virtual machine is not an attack', () => {
    const status = evaluate(claims(), {
      org: 'acme',
      now: new Date('2029-01-01T00:00:00Z'),
      lastSeen: new Date('2029-01-01T00:30:00Z'),
    })
    assert.equal(status.state, 'active')
  })

  it('refuses a revoked license without asking anything on the network', () => {
    const status = evaluate(claims(), {
      org: 'acme',
      now: new Date('2029-01-01T00:00:00Z'),
      revoked: new Set(['lic-1']),
    })
    assert.equal(status.state, 'revoked')
  })

  it('reports the seat limit rather than enforcing it by removing anybody', () => {
    const status = evaluate(claims({ seats: 3 }), { org: 'acme', now: new Date('2029-01-01T00:00:00Z') })
    assert.equal(status.seatsExceeded(2), false)
    assert.equal(status.seatsExceeded(3), true)
    // Zero seats is unlimited, not a limit of nobody.
    const unlimited = evaluate(claims({ seats: 0 }), { org: 'acme', now: new Date('2029-01-01T00:00:00Z') })
    assert.equal(unlimited.seatsExceeded(10_000), false)
  })

  it('permits nothing at all with no license, and calls that none rather than expired', () => {
    const status = none()
    assert.equal(status.state, 'none')
    assert.equal(status.enabled('sso'), false)
    assert.equal(status.honoured(), false)
  })

  it('refuses a license key set with no organization to check it against', () => {
    assert.throws(
      () => licenseFromEnv({ AF_LICENSE_KEY: k.token({ org: 'acme', expires_at: future }) }, new Date()),
      (err: unknown) => err instanceof LicenseRefused && /AF_ORG is not/.test(err.message),
    )
  })
})

describe('registering the enterprise edition', () => {
  it('installs both sign-on extension points, never one', () => {
    // ee/web/sso/src/index.ts says why at length: an organization that has
    // REQUIRED single sign-on still has GitHub sign-in open unless the policy
    // is installed too, which is a feature that looks complete and enforces
    // nothing. This caller is the one that did not exist, so nothing checked
    // that it does both.
    clearExtensions()
    setSignInPolicy(null)
    assert.equal(hasSignInPolicy(), false)

    registerEnterprise({
      pool: {} as never,
      clock: new FakeClock(),
      baseUrl: 'https://enterprise.test',
      appBaseUrl: 'https://enterprise.test/',
      secureCookies: true,
      env: {},
      log: () => {},
      encryptionKey: Buffer.alloc(32, 7),
    })

    assert.deepEqual(registeredExtensions().map((e) => e.name).sort(), ['scim', 'sso'])
    assert.equal(
      hasSignInPolicy(),
      true,
      'the routes were registered and the sign-in policy was not, so an organization that ' +
        'requires single sign-on would still be reachable through GitHub',
    )
    clearExtensions()
    setSignInPolicy(null)
  })
})
