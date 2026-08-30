// The id_token, and the two attacks that have names.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// alg:none and algorithm confusion are the reason this file exists. Both are
// constructed here as real tokens, not as assertions about what the code
// intends, and the confusion vector in particular is built the way the attack
// is actually performed: take the provider's PUBLIC key, which anybody can
// fetch from the published key set, and use its PEM text as an HMAC secret. A
// verifier that reads the algorithm out of the header and then reaches for
// "the key" accepts it, and the attacker can mint any claims they like.
//
// Keys are generated at run time. Nothing here is committed.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { createHmac, createSign, generateKeyPairSync, createHash } from 'node:crypto'
import { TokenRefused, verifyIdToken, type Jwk } from '../src/oidc/jwt.ts'
import {
  DiscoveryRefused,
  authorizationUrl,
  beginLogin,
  codeChallenge,
  completeLogin,
  discover,
  exchangeCode,
} from '../src/oidc/flow.ts'

const ISSUER = 'https://idp.test'
const CLIENT_ID = 'antifailure-test'
const NONCE = 'nonce-from-this-login'
const NOW = new Date('2026-06-01T12:00:00Z')

function rsaKey(kid: string) {
  const { publicKey, privateKey } = generateKeyPairSync('rsa', { modulusLength: 2048 })
  const jwk = publicKey.export({ format: 'jwk' }) as Jwk
  return {
    kid,
    privateKey,
    publicKeyPem: publicKey.export({ type: 'spki', format: 'pem' }).toString(),
    jwk: { ...jwk, kid, use: 'sig', alg: 'RS256' },
  }
}

function ecKey(kid: string) {
  const { publicKey, privateKey } = generateKeyPairSync('ec', { namedCurve: 'P-256' })
  const jwk = publicKey.export({ format: 'jwk' }) as Jwk
  return { kid, privateKey, jwk: { ...jwk, kid, use: 'sig', alg: 'ES256' } }
}

const primary = rsaKey('primary')
const rotated = rsaKey('rotated')
const attacker = rsaKey('attacker')
const ec = ecKey('ec-1')

const b64 = (value: object | string) =>
  Buffer.from(typeof value === 'string' ? value : JSON.stringify(value)).toString('base64url')

function claims(over: Record<string, unknown> = {}) {
  return {
    iss: ISSUER,
    sub: 'user-1',
    aud: CLIENT_ID,
    exp: Math.floor(NOW.getTime() / 1000) + 600,
    iat: Math.floor(NOW.getTime() / 1000) - 10,
    nonce: NONCE,
    email: 'Ada@Example.Test',
    name: 'Ada Lovelace',
    ...over,
  }
}

/** A genuine token from the provider. */
function token(
  key: { kid: string; privateKey: import('node:crypto').KeyObject },
  payload: Record<string, unknown> = claims(),
  alg = 'RS256',
): string {
  const head = b64({ alg, kid: key.kid, typ: 'JWT' })
  const body = b64(payload)
  const signer = createSign(alg.startsWith('ES') ? 'sha256' : 'RSA-SHA256')
  signer.update(`${head}.${body}`)
  const signature = signer.sign(
    alg.startsWith('ES')
      ? { key: key.privateKey, dsaEncoding: 'ieee-p1363' }
      : key.privateKey,
  )
  return `${head}.${body}.${signature.toString('base64url')}`
}

function verify(value: string, over: Record<string, unknown> = {}) {
  return verifyIdToken(value, {
    keys: [primary.jwk, rotated.jwk, ec.jwk],
    issuer: ISSUER,
    clientId: CLIENT_ID,
    nonce: NONCE,
    clockSkewSeconds: 300,
    now: NOW,
    ...over,
  } as never)
}

function reasonFor(fn: () => unknown): string {
  try {
    fn()
  } catch (err) {
    if (err instanceof TokenRefused) return err.reason
    throw err
  }
  assert.fail('the token was accepted')
}

describe('the positive control', () => {
  it('accepts a genuine RS256 token and returns its claims', () => {
    const verified = verify(token(primary))
    assert.equal(verified.sub, 'user-1')
    assert.equal(verified.email, 'Ada@Example.Test')
  })

  it('accepts an ES256 token, with the raw r||s signature JWS uses', () => {
    // Node produces DER by default and JWS wants the raw pair. Getting this
    // wrong makes every ES256 provider look broken.
    assert.equal(verify(token(ec, claims(), 'ES256')).sub, 'user-1')
  })

  it('accepts a token signed by the rotated key, selected by kid', () => {
    assert.equal(verify(token(rotated)).sub, 'user-1')
  })
})

describe('the two attacks with names', () => {
  it('refuses alg: none', () => {
    const head = b64({ alg: 'none', typ: 'JWT' })
    const body = b64(claims({ sub: 'root', email: 'root@example.test' }))
    assert.equal(reasonFor(() => verify(`${head}.${body}.`)), 'bad_algorithm')
  })

  it("refuses HS256 signed with the provider's own public key as the secret", () => {
    // Algorithm confusion, built the way it is actually performed. The public
    // key is published; anybody can fetch it. A verifier that trusts the
    // header's alg and then reaches for "the key" computes exactly this HMAC
    // and agrees.
    const head = b64({ alg: 'HS256', kid: primary.kid, typ: 'JWT' })
    const body = b64(claims({ sub: 'root', email: 'root@example.test' }))
    const mac = createHmac('sha256', primary.publicKeyPem)
      .update(`${head}.${body}`)
      .digest('base64url')

    assert.equal(reasonFor(() => verify(`${head}.${body}.${mac}`)), 'bad_algorithm')
  })

  it('refuses a token signed by a key the provider does not publish', () => {
    // The attacker's key, presented under the primary key's kid so that the
    // right key is selected and simply does not verify.
    const forged = token({ kid: primary.kid, privateKey: attacker.privateKey })
    assert.equal(reasonFor(() => verify(forged)), 'invalid_signature')
  })

  it('refuses a kid the key set does not have', () => {
    assert.equal(
      reasonFor(() => verify(token({ kid: 'never-published', privateKey: primary.privateKey }))),
      'no_key',
    )
  })
})

describe('the claims', () => {
  it('refuses a token from a different issuer', () => {
    assert.equal(reasonFor(() => verify(token(primary, claims({ iss: 'https://other.test' })))), 'wrong_issuer')
  })

  it('refuses a token issued for a different application', () => {
    assert.equal(
      reasonFor(() => verify(token(primary, claims({ aud: 'somebody-elses-client' })))),
      'wrong_audience',
    )
  })

  it('refuses several audiences whose authorized party is not us', () => {
    assert.equal(
      reasonFor(() =>
        verify(token(primary, claims({ aud: [CLIENT_ID, 'other'], azp: 'other' }))),
      ),
      'wrong_authorized_party',
    )
  })

  it('refuses an expired token', () => {
    assert.equal(
      reasonFor(() => verify(token(primary, claims({ exp: Math.floor(NOW.getTime() / 1000) - 600 })))),
      'expired',
    )
  })

  it('tolerates a clock a little out, and stops at the bound', () => {
    const slightlyStale = claims({ exp: Math.floor(NOW.getTime() / 1000) - 120 })
    assert.equal(verify(token(primary, slightlyStale)).sub, 'user-1')
    assert.equal(
      reasonFor(() => verify(token(primary, slightlyStale), { clockSkewSeconds: 30 })),
      'expired',
    )
  })

  it('refuses a token with no expiry', () => {
    const { exp: _dropped, ...rest } = claims()
    assert.equal(reasonFor(() => verify(token(primary, rest))), 'no_expiry')
  })

  it('refuses a token carrying no nonce', () => {
    // Without this, an id_token the attacker legitimately obtained for their
    // own account at the same provider can be injected into somebody else's
    // login and the signature is perfectly valid.
    const { nonce: _dropped, ...rest } = claims()
    assert.equal(reasonFor(() => verify(token(primary, rest))), 'wrong_nonce')
  })

  it("refuses a token carrying somebody else's nonce", () => {
    assert.equal(
      reasonFor(() => verify(token(primary, claims({ nonce: 'a-different-login' })))),
      'wrong_nonce',
    )
  })

  it('refuses an id_token that does not match the access token it came with', () => {
    const accessToken = 'access-token-value'
    const digest = createHash('sha256').update(accessToken, 'ascii').digest()
    const correct = digest.subarray(0, digest.length / 2).toString('base64url')

    assert.equal(verify(token(primary, claims({ at_hash: correct })), { accessToken }).sub, 'user-1')
    assert.equal(
      reasonFor(() => verify(token(primary, claims({ at_hash: 'not-the-hash' })), { accessToken })),
      'wrong_at_hash',
    )
  })

  it('refuses something that is not a JWT at all', () => {
    for (const bad of ['', 'a.b', 'a.b.c.d', 'not-base64!.x.y']) {
      assert.equal(reasonFor(() => verify(bad)), 'malformed', `accepted ${JSON.stringify(bad)}`)
    }
  })
})

describe('the authorization request', () => {
  it('sends a S256 challenge and never the verifier', () => {
    const secrets = beginLogin()
    const url = new URL(
      authorizationUrl({
        endpoints: { authorizationEndpoint: 'https://idp.test/authorize' },
        clientId: CLIENT_ID,
        redirectUri: 'https://antifailure.test/sso/oidc/handle/callback',
        secrets,
      }),
    )

    assert.equal(url.searchParams.get('code_challenge_method'), 'S256')
    assert.equal(url.searchParams.get('code_challenge'), codeChallenge(secrets.codeVerifier))
    assert.equal(url.searchParams.get('state'), secrets.state)
    assert.equal(url.searchParams.get('nonce'), secrets.nonce)
    assert.ok(
      !url.toString().includes(secrets.codeVerifier),
      'the code verifier went to the provider, which defeats the point of PKCE',
    )
    // state and nonce are separate values. Using one for both gives up one of
    // the two properties they provide.
    assert.notEqual(secrets.state, secrets.nonce)
    assert.match(url.searchParams.get('scope') ?? '', /\bopenid\b/)
  })
})

describe('discovery', () => {
  const fetchReturning = (body: unknown, status = 200): typeof fetch =>
    (async () =>
      new Response(JSON.stringify(body), {
        status,
        headers: { 'content-type': 'application/json' },
      })) as never

  const good = {
    issuer: ISSUER,
    authorization_endpoint: 'https://idp.test/authorize',
    token_endpoint: 'https://idp.test/token',
    jwks_uri: 'https://idp.test/keys',
  }

  it('reads a well-formed document', async () => {
    const endpoints = await discover(ISSUER, fetchReturning(good))
    assert.equal(endpoints.tokenEndpoint, 'https://idp.test/token')
  })

  it('tolerates a trailing slash on either side', async () => {
    assert.equal((await discover(`${ISSUER}/`, fetchReturning(good))).issuer, ISSUER)
  })

  it('refuses a document that disagrees about its own issuer', async () => {
    // Every token is checked against the issuer, so taking the issuer from an
    // unverified document would make that check circular.
    await assert.rejects(
      () => discover(ISSUER, fetchReturning({ ...good, issuer: 'https://elsewhere.test' })),
      DiscoveryRefused,
    )
  })

  it('refuses an endpoint served over plain HTTP', async () => {
    await assert.rejects(
      () => discover(ISSUER, fetchReturning({ ...good, token_endpoint: 'http://idp.test/token' })),
      DiscoveryRefused,
    )
  })

  it('refuses a document missing an endpoint it needs', async () => {
    const { jwks_uri: _dropped, ...rest } = good
    await assert.rejects(() => discover(ISSUER, fetchReturning(rest)), DiscoveryRefused)
  })
})

describe('the token exchange', () => {
  it("quotes the provider's own error, which is what a misconfiguration needs", async () => {
    const failing: typeof fetch = (async () =>
      new Response(
        JSON.stringify({ error: 'invalid_client', error_description: 'Client secret is wrong' }),
        { status: 401, headers: { 'content-type': 'application/json' } },
      )) as never

    await assert.rejects(
      () =>
        exchangeCode({
          endpoints: { tokenEndpoint: 'https://idp.test/token' },
          clientId: CLIENT_ID,
          clientSecret: 'wrong',
          redirectUri: 'https://antifailure.test/cb',
          code: 'code',
          codeVerifier: 'verifier',
          fetch: failing,
        }),
      (err: unknown) =>
        err instanceof TokenRefused && /invalid_client.*Client secret is wrong/.test(err.message),
    )
  })

  it('sends the code verifier and never the challenge', async () => {
    let sent = ''
    const capturing: typeof fetch = (async (_url: string, init: RequestInit) => {
      sent = String(init.body)
      return new Response(JSON.stringify({ id_token: 'x.y.z' }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      })
    }) as never

    await exchangeCode({
      endpoints: { tokenEndpoint: 'https://idp.test/token' },
      clientId: CLIENT_ID,
      clientSecret: 'shh',
      redirectUri: 'https://antifailure.test/cb',
      code: 'the-code',
      codeVerifier: 'the-verifier',
      fetch: capturing,
    })

    const body = new URLSearchParams(sent)
    assert.equal(body.get('code_verifier'), 'the-verifier')
    assert.equal(body.get('grant_type'), 'authorization_code')
  })

  it('refuses a response with no id_token rather than continuing', async () => {
    const noToken: typeof fetch = (async () =>
      new Response(JSON.stringify({ access_token: 'a' }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      })) as never

    await assert.rejects(
      () =>
        exchangeCode({
          endpoints: { tokenEndpoint: 'https://idp.test/token' },
          clientId: CLIENT_ID,
          clientSecret: 'shh',
          redirectUri: 'https://antifailure.test/cb',
          code: 'c',
          codeVerifier: 'v',
          fetch: noToken,
        }),
      (err: unknown) => err instanceof TokenRefused && err.reason === 'no_id_token',
    )
  })

  it('completes end to end against a provider that behaves', async () => {
    // The whole flow, so that exchangeCode, fetchJwks and verifyIdToken are
    // proved to fit together rather than each proved alone.
    const idToken = token(primary)
    const behaving: typeof fetch = (async (url: string) =>
      new Response(
        JSON.stringify(
          String(url).includes('keys') ? { keys: [primary.jwk] } : { id_token: idToken },
        ),
        { status: 200, headers: { 'content-type': 'application/json' } },
      )) as never

    const verified = await completeLogin({
      endpoints: { tokenEndpoint: 'https://idp.test/token' },
      jwksUri: 'https://idp.test/keys',
      issuer: ISSUER,
      clientId: CLIENT_ID,
      clientSecret: 'shh',
      redirectUri: 'https://antifailure.test/cb',
      code: 'c',
      codeVerifier: 'v',
      nonce: NONCE,
      clockSkewSeconds: 300,
      now: NOW,
      fetch: behaving,
    })
    assert.equal(verified.sub, 'user-1')
  })
})
