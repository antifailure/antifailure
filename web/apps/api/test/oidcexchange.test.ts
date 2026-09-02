// Exchanging a GitHub Actions workflow identity for an engine token.
//
// The suite is written around one attack and everything else is scaffolding
// for it. A GitHub Actions identity token is signed by GitHub and its
// `repository` claim is true, so verifying the signature perfectly and then
// reading that claim as an authorization authenticates a stranger flawlessly
// and authorizes them anyway: anybody can create a repository, put
// `id-token: write` in a workflow, and mint a genuine token naming it.
//
// So the case that matters is "a token GitHub really did sign, for a repository
// nobody has claimed, is refused", and it is written twice: once as a refusal,
// and once as the stronger statement that no credential exists afterwards. A
// suite that only checked "an invalid token is refused" would pass with the
// binding check deleted, because an invalid token is refused by the signature.
//
// Every token below is signed with a real key pair rather than handed to a stub
// verifier, for the reason prlifecycle.test.ts gives: a stub that returns the
// claims it was given cannot show that a token nobody could have minted is
// refused. Every credential below is obtained through the real routes, for the
// reason tokenapi.test.ts gives: a test that inserted a row into engine_tokens
// would prove these endpoints work for tokens they will never see.

import { test, describe, before, after, beforeEach } from 'node:test'
import assert from 'node:assert/strict'
import {
  createHash,
  createSign,
  generateKeyPairSync,
  randomUUID,
  type KeyObject,
} from 'node:crypto'
import { ACTIONS_ISSUER, ActionsKeys, CALLBACK_AUDIENCE, TokenRefused } from '../src/github/oidc.ts'
import { OIDC_TOKEN_TTL_MS, repositoryLimiter } from '../src/github/exchange.ts'
import { FakeClock } from '../src/clock.ts'
import { sql as rawSql } from 'drizzle-orm'
import { CSRF_HEADER } from '../src/auth/session.ts'
import { DEVICE_POLL_INTERVAL_SECONDS } from '../src/auth/device.ts'
import type { Role } from '../src/permissions.ts'
import {
  available,
  dropOrg,
  seedOrg,
  signInAs,
  startApi,
  type ApiHarness,
  type Org,
} from './harness.ts'

const hasDatabase = await available()

// ---------------------------------------------------------------------------
// A GitHub Actions identity, signed the way GitHub signs one.
// ---------------------------------------------------------------------------

const identityKey = generateKeyPairSync('rsa', { modulusLength: 2048 })
// A second, equally real key pair. This is what "a valid signature from a
// different key" means: a token that is correctly signed RS256 by somebody who
// is not GitHub. Verifying only that the signature parses would accept it.
const impostorKey = generateKeyPairSync('rsa', { modulusLength: 2048 })
const IDENTITY_KID = 'oidc-exchange-test-key'

function jwks(): string {
  const jwk = identityKey.publicKey.export({ format: 'jwk' }) as Record<string, unknown>
  return JSON.stringify({ keys: [{ ...jwk, kid: IDENTITY_KID, use: 'sig', alg: 'RS256' }] })
}

function base64url(value: string): string {
  return Buffer.from(value, 'utf8').toString('base64url')
}

interface IdentityClaims {
  repository: string
  runId?: number
  audience?: string
  issuer?: string
  expiresInSeconds?: number
  issuedAtOffsetSeconds?: number
  notBeforeOffsetSeconds?: number
  key?: KeyObject
  algorithm?: string
}

/** A workflow identity token, signed for real. `key` and `algorithm` exist so a
 *  test can produce the two tokens a lenient verifier accepts. */
function identityToken(claims: IdentityClaims, now: Date): string {
  const header = { alg: claims.algorithm ?? 'RS256', typ: 'JWT', kid: IDENTITY_KID }
  const seconds = Math.floor(now.getTime() / 1000)
  const payload: Record<string, unknown> = {
    iss: claims.issuer ?? ACTIONS_ISSUER,
    aud: claims.audience ?? CALLBACK_AUDIENCE,
    iat: seconds + (claims.issuedAtOffsetSeconds ?? -10),
    exp: seconds + (claims.expiresInSeconds ?? 600),
    repository: claims.repository,
    repository_owner: claims.repository.split('/')[0],
    run_id: String(claims.runId ?? 42),
    run_attempt: '1',
    ref: 'refs/heads/main',
    event_name: 'push',
    job_workflow_ref: `${claims.repository}/.github/workflows/antifailure.yml@refs/heads/main`,
    sha: 'e'.repeat(40),
  }
  if (claims.notBeforeOffsetSeconds !== undefined) {
    payload.nbf = seconds + claims.notBeforeOffsetSeconds
  }
  const signingInput = `${base64url(JSON.stringify(header))}.${base64url(JSON.stringify(payload))}`
  const signature = createSign('RSA-SHA256')
    .update(signingInput)
    .sign(claims.key ?? identityKey.privateKey)
  return `${signingInput}.${signature.toString('base64url')}`
}

/** The `alg: none` token: a header claiming there is nothing to check, and no
 *  signature at all. A verifier that reads the algorithm out of the header as
 *  an instruction returns success on this and every claim in it is attacker
 *  chosen. */
function unsignedToken(repository: string, now: Date): string {
  const header = { alg: 'none', typ: 'JWT' }
  const seconds = Math.floor(now.getTime() / 1000)
  const payload = {
    iss: ACTIONS_ISSUER,
    aud: CALLBACK_AUDIENCE,
    iat: seconds - 10,
    exp: seconds + 600,
    repository,
    repository_owner: repository.split('/')[0],
    run_id: '1',
    run_attempt: '1',
    job_workflow_ref: `${repository}/.github/workflows/x.yml@refs/heads/main`,
  }
  return `${base64url(JSON.stringify(header))}.${base64url(JSON.stringify(payload))}.`
}

// ---------------------------------------------------------------------------
// The key set, which needs no database
//
// The requirement here is a negative one and it is the kind that is easy to
// satisfy by accident in the wrong direction: when GitHub's key set cannot be
// fetched, the verifier has to REFUSE. A verifier that keeps working on a
// stale or empty key set when it cannot check who signed anything has stopped
// verifying, and it would do so silently, on the day GitHub had an outage.
// ---------------------------------------------------------------------------

describe("GitHub's signing keys", () => {
  const clock = new FakeClock()

  test('a key set that cannot be fetched refuses rather than failing open', async () => {
    const keys = new ActionsKeys(clock, {
      fetchImpl: async () => new Response('nope', { status: 503 }),
    })
    await assert.rejects(
      () => keys.current(IDENTITY_KID),
      (err: unknown) => err instanceof TokenRefused && err.reason === 'keys_unavailable',
    )
  })

  test('a network failure refuses too, and says so rather than crashing', async () => {
    // A fetch that cannot reach github.com rejects with a TypeError, which is
    // the shape that used to escape this class and reach a caller as a 500 with
    // no reason in it.
    const keys = new ActionsKeys(clock, {
      fetchImpl: async () => {
        throw new TypeError('fetch failed')
      },
    })
    await assert.rejects(
      () => keys.current(IDENTITY_KID),
      (err: unknown) => err instanceof TokenRefused && err.reason === 'keys_unavailable',
    )
  })

  test('a key set with no usable key is not a key set', async () => {
    const keys = new ActionsKeys(clock, {
      fetchImpl: async () =>
        new Response(JSON.stringify({ keys: [{ kty: 'RSA', kid: 'no-modulus' }] }), {
          headers: { 'content-type': 'application/json' },
        }),
    })
    await assert.rejects(
      () => keys.current(null),
      (err: unknown) => err instanceof TokenRefused && err.reason === 'keys_unavailable',
    )
  })

  test('the key set is cached, so a verification is not a request to GitHub', async () => {
    let fetches = 0
    const keys = new ActionsKeys(clock, {
      fetchImpl: async () => {
        fetches += 1
        return new Response(jwks(), { headers: { 'content-type': 'application/json' } })
      },
    })
    for (let i = 0; i < 20; i += 1) await keys.current(IDENTITY_KID)
    assert.equal(fetches, 1, 'the key set was fetched per verification')
  })

  test('a kid nobody has seen refetches once a minute and no faster', async () => {
    // What a rotation looks like from here, and also what somebody sending
    // tokens with random kids looks like. Both have to be handled by the same
    // rule, or the handling of the first is a load generator for the second.
    let fetches = 0
    const keys = new ActionsKeys(clock, {
      fetchImpl: async () => {
        fetches += 1
        return new Response(jwks(), { headers: { 'content-type': 'application/json' } })
      },
    })
    await keys.current(IDENTITY_KID)
    assert.equal(fetches, 1)
    for (let i = 0; i < 50; i += 1) await keys.current(`invented-${i}`)
    assert.equal(fetches, 1, 'an unknown kid turned this endpoint into a load generator')

    clock.advance(ActionsKeys.MIN_REFETCH_MS + 1000)
    await keys.current('a-kid-from-after-a-rotation')
    assert.equal(fetches, 2, 'a rotation was never picked up')
  })
})

describe(
  'exchanging a workflow identity for an engine token',
  { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' },
  () => {
    let api: ApiHarness
    let org: Org
    let other: Org
    // The owner half of every repository in this file. Unique per run, because
    // github_installations.installation_id is UNIQUE and the binding table
    // carries a partial unique index over live rows across every tenant, so a
    // run killed before its after hook would otherwise fail every later run in
    // a way that reads as a broken control plane.
    let owner: string
    let otherOwner: string
    let repository: string

    before(async () => {
      api = await startApi({ actionsJwks: jwks })
      org = await seedOrg(api.admin, 'oidc-main')
      other = await seedOrg(api.admin, 'oidc-other')
      owner = org.slug
      otherOwner = other.slug
      repository = `${owner}/app`

      // The App installed on each account. Written directly because what is
      // under test here is the exchange rather than the delivery path that
      // records an installation, and githubapp.test.ts covers that.
      await api.admin`
        INSERT INTO github_installations (org_id, installation_id, account_login, account_type)
        VALUES (${org.orgId}, ${installationId()}, ${owner}, 'Organization')`
      await api.admin`
        INSERT INTO github_installations (org_id, installation_id, account_login, account_type)
        VALUES (${other.orgId}, ${installationId()}, ${otherOwner}, 'Organization')`
    })

    after(async () => {
      await dropOrg(api.admin, org.orgId)
      await dropOrg(api.admin, other.orgId)
      await api.close()
    })

    beforeEach(async () => {
      // Every test starts from no claims and no exchanged credentials, so one
      // test's binding cannot make the next one pass.
      await api.admin`
        DELETE FROM engine_tokens WHERE kind = 'oidc'
          AND org_id IN (${org.orgId}, ${other.orgId})`
      await api.admin`
        DELETE FROM oidc_repository_bindings WHERE org_id IN (${org.orgId}, ${other.orgId})`
      // The audit log too, so the suite's own history is not what the audit
      // test reads. Append-only for the application role, which is the property
      // that matters; this runs as the owner, the same way dropOrg does.
      await api.admin`
        DELETE FROM audit_entries WHERE org_id IN (${org.orgId}, ${other.orgId})`
      // The per-repository limiter is a token bucket on the harness clock, and
      // the harness clock only moves when somebody moves it. Twenty exchanges
      // in one file would otherwise empty it and the twenty first test would
      // fail on a 429 that has nothing to do with what it asserts.
      api.clock.advance(60_000)
    })

    function installationId(): number {
      return 970_000_000 + Number(BigInt('0x' + randomUUID().slice(0, 8)) % 100_000_000n)
    }

    // -----------------------------------------------------------------------
    // Calling
    // -----------------------------------------------------------------------

    async function exchange(
      token: string,
    ): Promise<{ status: number; json: Record<string, unknown>; retryAfter: string | null }> {
      const res = await api.fetch('/v1/auth/github-oidc', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ token }),
      })
      return {
        status: res.status,
        json: (await res.json()) as Record<string, unknown>,
        retryAfter: res.headers.get('retry-after'),
      }
    }

    /** A real CLI token with the scopes and role asked for, obtained the way
     *  `af login` obtains one. */
    async function cliToken(role: Role, scopes: string[], within: Org = org): Promise<string> {
      const started = await api.fetch('/auth/device/code', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ clientLabel: 'a test terminal', scopes }),
      })
      assert.equal(started.status, 200)
      const codes = (await started.json()) as { device_code: string; user_code: string }

      const person = await signInAs(api, within, role, `oidc-${role}`)
      const approved = await api.fetch('/auth/device/approve', {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          cookie: person.cookie,
          [CSRF_HEADER]: person.csrfToken,
        },
        body: JSON.stringify({ user_code: codes.user_code }),
      })
      assert.equal(approved.status, 200)

      api.clock.advance(DEVICE_POLL_INTERVAL_SECONDS * 1000 + 1000)
      const granted = await api.fetch('/auth/device/token', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ device_code: codes.device_code }),
      })
      assert.equal(granted.status, 200)
      return ((await granted.json()) as { access_token: string }).access_token
    }

    async function call(
      token: string | null,
      method: string,
      path: string,
      body?: unknown,
    ): Promise<{ status: number; json: Record<string, unknown> }> {
      const headers: Record<string, string> = {}
      if (token) headers.authorization = `Bearer ${token}`
      if (body !== undefined) headers['content-type'] = 'application/json'
      const res = await api.fetch(path, {
        method,
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
      })
      const text = await res.text()
      let parsed: Record<string, unknown> = {}
      try {
        parsed = JSON.parse(text) as Record<string, unknown>
      } catch {
        // Left empty. A route answering with something other than JSON is a
        // finding, and the status is asserted on instead.
      }
      return { status: res.status, json: parsed }
    }

    /** Claims a repository for an organization, through the real route. */
    async function claim(repo: string, within: Org = org): Promise<{ status: number; json: Record<string, unknown> }> {
      const admin = await cliToken('owner', ['tokens.manage'], within)
      return call(admin, 'POST', '/v1/oidc/bindings', { repository: repo })
    }

    /** The exact statement claimRepository runs, so the transaction level test
     *  exercises the mechanism the route relies on rather than a paraphrase. */
    function sqlFor(repo: string) {
      return rawSql`
        INSERT INTO oidc_repository_bindings (org_id, repository)
        VALUES (${org.orgId}::uuid, ${repo})
        ON CONFLICT (repository) WHERE revoked_at IS NULL DO NOTHING
        RETURNING id`
    }

    /** Whether a bearer token is accepted by the engine surface. An empty batch
     *  is refused on its contents, which is a different and correct answer, so
     *  anything but 401 means the credential was accepted. */
    async function acceptedByIngest(token: string): Promise<boolean> {
      const res = await api.fetch('/v1/events', {
        method: 'POST',
        headers: { 'content-type': 'application/json', authorization: `Bearer ${token}` },
        body: JSON.stringify({ events: [] }),
      })
      return res.status !== 401
    }

    /** Every live oidc credential in the two organizations, so a test can
     *  assert that a refusal minted nothing rather than only that it answered
     *  with an error. */
    async function liveExchangedTokens(): Promise<number> {
      const rows = await api.admin<{ n: string }[]>`
        SELECT count(*) AS n FROM engine_tokens
        WHERE kind = 'oidc' AND revoked_at IS NULL`
      return Number(rows[0]!.n)
    }

    // -----------------------------------------------------------------------
    // The path the whole feature exists for
    // -----------------------------------------------------------------------

    test('a claimed repository exchanges its identity for a token that ingests', async () => {
      const claimed = await claim(repository)
      assert.equal(claimed.status, 201, JSON.stringify(claimed.json))

      const issued = await exchange(identityToken({ repository }, api.clock.now()))
      assert.equal(issued.status, 200, JSON.stringify(issued.json))
      const token = String(issued.json.token)
      assert.match(token, /^aft_/, 'the engine expects the same shape a static token has')
      assert.equal(issued.json.org_id, org.orgId, 'the token landed in the wrong organization')
      assert.equal(issued.json.repository, repository)

      // The assertion the feature exists for. An exchange that returns a string
      // /v1/events rejects is a dead end with a friendlier error message.
      assert.ok(await acceptedByIngest(token), 'the exchanged token was refused by /v1/events')

      // RFC3339, in the future, and no further away than the declared lifetime.
      const expiresAt = Date.parse(String(issued.json.expires_at))
      assert.ok(Number.isFinite(expiresAt), `expires_at was ${String(issued.json.expires_at)}`)
      assert.ok(expiresAt > api.clock.now().getTime())
      assert.ok(expiresAt <= api.clock.now().getTime() + OIDC_TOKEN_TTL_MS)
    })

    test('the exchanged token is stored as a hash and never returned again', async () => {
      await claim(repository)
      const issued = await exchange(identityToken({ repository }, api.clock.now()))
      const token = String(issued.json.token)

      const rows = await api.admin<{ token_hash: Buffer; prefix: string; kind: string }[]>`
        SELECT token_hash, prefix, kind FROM engine_tokens
        WHERE org_id = ${org.orgId} AND kind = 'oidc'`
      assert.equal(rows.length, 1)
      assert.equal(rows[0]!.prefix, token.slice(0, 12))
      assert.deepEqual(
        Buffer.from(rows[0]!.token_hash),
        createHash('sha256').update(token, 'utf8').digest(),
        'the stored value is not the SHA-256 of the token',
      )

      // And it does not appear in `af token list`, which is the operator's
      // list of long lived credentials rather than a log of every CI job.
      const admin = await cliToken('owner', ['tokens.manage'])
      const listed = await call(admin, 'GET', '/v1/tokens')
      assert.deepEqual(listed.json.tokens, [])
    })

    test('the token stops working when it expires', async () => {
      await claim(repository)
      const issued = await exchange(identityToken({ repository }, api.clock.now()))
      const token = String(issued.json.token)
      assert.ok(await acceptedByIngest(token))

      // One second past the declared lifetime. Short lived has to be a property
      // somebody can watch happen, not a number in a response body.
      api.clock.advance(OIDC_TOKEN_TTL_MS + 1000)
      assert.equal(
        await acceptedByIngest(token),
        false,
        'an expired exchanged token was still accepted by /v1/events',
      )
    })

    test('an exchanged token cannot become a permanent one, or a person', async () => {
      // The property that stops fifteen minutes from turning into forever. If
      // an exchanged credential could reach POST /v1/tokens it would mint an
      // engine token that never expires, and the expiry would be decoration:
      // one workflow run in a claimed repository would be a permanent
      // credential. If it could claim a repository it would widen its own
      // reach without anybody approving it.
      await claim(repository)
      const issued = await exchange(identityToken({ repository }, api.clock.now()))
      const token = String(issued.json.token)
      assert.ok(await acceptedByIngest(token), 'the fixture token does not work at all')

      assert.equal((await call(token, 'POST', '/v1/tokens', { name: 'forever' })).status, 401)
      assert.equal((await call(token, 'GET', '/v1/tokens')).status, 401)
      assert.equal(
        (await call(token, 'POST', '/v1/oidc/bindings', { repository: `${owner}/another` })).status,
        401,
      )
      assert.equal((await call(token, 'DELETE', `/v1/oidc/bindings/${repository}`)).status, 401)
      // And it is not a person: answering whoami with one would put a machine's
      // actions in a human's name.
      assert.equal((await call(token, 'GET', '/v1/whoami')).status, 401)

      // Nothing was created by any of the above.
      const minted = await api.admin<{ n: string }[]>`
        SELECT count(*) AS n FROM engine_tokens
        WHERE org_id = ${org.orgId} AND kind = 'engine'`
      assert.equal(Number(minted[0]!.n), 0)
    })

    // -----------------------------------------------------------------------
    // THE ONE THAT MATTERS
    // -----------------------------------------------------------------------

    test('an unclaimed repository under an installed owner is claimed for that org', async () => {
      // The synthesis. Nobody typed a claim for this repository, and it gets
      // one, because GitHub already said who controls the account: the
      // installation row is written only by an HMAC-verified delivery, which is
      // the same evidence a manual claim is checked against and one step fewer.
      const fresh = `${owner}/nobody-claimed-this`
      const issued = await exchange(identityToken({ repository: fresh }, api.clock.now()))
      assert.equal(issued.status, 200, JSON.stringify(issued.json))
      assert.equal(issued.json.org_id, org.orgId, 'the synthesized claim landed in the wrong org')

      // A real row, in the right tenant, audited as installation-derived rather
      // than as somebody's decision, because a year from now the question is on
      // what evidence this repository was allowed to report.
      const rows = await api.admin<{ org_id: string }[]>`
        SELECT org_id FROM oidc_repository_bindings
        WHERE repository = ${fresh} AND revoked_at IS NULL`
      assert.equal(rows.length, 1)
      assert.equal(rows[0]!.org_id, org.orgId)
      const audited = await api.admin<{ detail: Record<string, unknown> }[]>`
        SELECT detail FROM audit_entries
        WHERE org_id = ${org.orgId} AND action = 'oidc_binding.created'`
      assert.equal(audited.length, 1)
      assert.equal(audited[0]!.detail.source, 'installation')
    })

    test('an owner two organizations have installed on is refused, not guessed at', async () => {
      // `github_installations.account_login` carries no unique constraint, so
      // two organizations CAN hold installations on one account. Picking one
      // would be a tenancy decision made by row order, which is precisely the
      // defect this table replaced. Ambiguity refuses and sends the customer to
      // the manual claim, where a person decides.
      await api.admin`
        INSERT INTO github_installations (org_id, installation_id, account_login, account_type)
        VALUES (${other.orgId}, ${installationId()}, ${owner}, 'Organization')`
      try {
        const contested = `${owner}/two-installations-claim-me`
        const refused = await exchange(identityToken({ repository: contested }, api.clock.now()))
        assert.equal(refused.status, 403, JSON.stringify(refused.json))
        assert.equal(refused.json.reason, 'no_binding')
        assert.equal(await liveExchangedTokens(), 0)
        const rows = await api.admin<{ n: string }[]>`
          SELECT count(*) AS n FROM oidc_repository_bindings WHERE repository = ${contested}`
        assert.equal(Number(rows[0]!.n), 0, 'an ambiguous owner still minted a claim')
      } finally {
        await api.admin`
          DELETE FROM github_installations
          WHERE org_id = ${other.orgId} AND lower(account_login) = ${owner}`
      }
    })

    test('synthesis never takes a repository another organization already claims', async () => {
      // The claim is held by `other`, and `org` holds the installation on the
      // owner. The installation must not be able to overrule a live claim, or
      // an organization could take a repository simply by installing the App.
      const contested = `${owner}/already-spoken-for`
      await api.admin`
        INSERT INTO oidc_repository_bindings (org_id, repository)
        VALUES (${other.orgId}, ${contested})`
      const refused = await exchange(identityToken({ repository: contested }, api.clock.now()))
      assert.equal(refused.status, 403, JSON.stringify(refused.json))
      assert.equal(refused.json.reason, 'no_binding')
      const rows = await api.admin<{ org_id: string }[]>`
        SELECT org_id FROM oidc_repository_bindings
        WHERE repository = ${contested} AND revoked_at IS NULL`
      assert.equal(rows.length, 1)
      assert.equal(rows[0]!.org_id, other.orgId, 'the claim was taken from the org that held it')
    })

    test("a repository owned by a stranger cannot reach anybody's organization", async () => {
      // The full attack, spelled out. The account has no installation here, no
      // organization, and no claim, and its token is signed by GitHub.
      const stranger = 'some-drive-by/their-own-repo'
      const refused = await exchange(identityToken({ repository: stranger }, api.clock.now()))
      assert.equal(refused.status, 403)
      assert.equal(refused.json.reason, 'no_binding')
      assert.equal(await liveExchangedTokens(), 0)
    })

    test('a claim covers one repository and not its neighbours under the same owner', async () => {
      // The boundary a lookup keyed on `repository_owner` rather than on
      // `repository` quietly erases, and the one that is easy to erase by
      // accident because the owner is right there in the claim. Everything
      // about this token is genuine and the organization does hold a claim; it
      // holds it on a different repository.
      //
      // Written after resolving the binding by owner instead of by repository
      // was tried on purpose and every other case in this file still passed:
      // the suite proved the attack from outside and said nothing about the
      // repository next door.
      assert.equal((await claim(repository)).status, 201)
      const sibling = `${owner}/a-repository-nobody-claimed`
      const issued = await exchange(identityToken({ repository: sibling }, api.clock.now()))
      assert.equal(issued.status, 200, JSON.stringify(issued.json))

      // The sibling gets in, because the same installation covers it, and that
      // is the approved behaviour. What must NOT happen is one claim covering
      // both: each repository gets its own row, so revoking one leaves the
      // other alone and the audit says which was allowed when.
      const rows = await api.admin<{ repository: string }[]>`
        SELECT repository FROM oidc_repository_bindings
        WHERE org_id = ${org.orgId} AND revoked_at IS NULL ORDER BY repository`
      assert.deepEqual(
        rows.map((r) => r.repository).sort(),
        [sibling, repository].sort(),
        'a claim on one repository covered its neighbour instead of each having its own',
      )
    })

    test("one organization's claim does not let another organization's repository in", async () => {
      // A claims its own repository. B's repository is still unclaimed, and a
      // valid identity for it must not resolve to A merely because A holds a
      // claim on something.
      assert.equal((await claim(repository)).status, 201)
      const issued = await exchange(
        identityToken({ repository: `${otherOwner}/app` }, api.clock.now()),
      )
      // It resolves, because the OTHER organization has the installation on
      // that owner. The property under test is where it lands, not whether it
      // is refused: holding a claim on something must never widen an
      // organization's reach to a repository it does not control.
      assert.equal(issued.status, 200, JSON.stringify(issued.json))
      assert.equal(issued.json.org_id, other.orgId)
      assert.notEqual(issued.json.org_id, org.orgId, 'a repository resolved into the wrong tenant')
    })

    test('a claim resolves to the organization that made it and to no other', async () => {
      assert.equal((await claim(`${otherOwner}/app`, other)).status, 201)
      const issued = await exchange(
        identityToken({ repository: `${otherOwner}/app` }, api.clock.now()),
      )
      assert.equal(issued.status, 200, JSON.stringify(issued.json))
      assert.equal(issued.json.org_id, other.orgId)
      assert.notEqual(issued.json.org_id, org.orgId)
    })

    // -----------------------------------------------------------------------
    // The token itself
    // -----------------------------------------------------------------------

    test('an identity minted for a different audience is refused', async () => {
      await claim(repository)
      // GitHub's default audience is the repository owner's URL, which every
      // workflow in the organization gets by asking for nothing. Accepting it
      // would mean a token minted for an unrelated purpose works here.
      const refused = await exchange(
        identityToken({ repository, audience: `https://github.com/${owner}` }, api.clock.now()),
      )
      assert.equal(refused.status, 401, JSON.stringify(refused.json))
      assert.equal(refused.json.reason, 'wrong_audience')
      assert.equal(await liveExchangedTokens(), 0)
    })

    test('an identity from a different issuer is refused', async () => {
      await claim(repository)
      const refused = await exchange(
        identityToken(
          { repository, issuer: 'https://token.actions.githubusercontent.com.evil.test' },
          api.clock.now(),
        ),
      )
      assert.equal(refused.status, 401)
      assert.equal(refused.json.reason, 'wrong_issuer')
      assert.equal(await liveExchangedTokens(), 0)
    })

    test('an expired identity is refused', async () => {
      await claim(repository)
      const refused = await exchange(
        identityToken({ repository, expiresInSeconds: -600 }, api.clock.now()),
      )
      assert.equal(refused.status, 401)
      assert.equal(refused.json.reason, 'expired')
      assert.equal(await liveExchangedTokens(), 0)
    })

    test('an identity issued in the future is refused', async () => {
      await claim(repository)
      const refused = await exchange(
        identityToken({ repository, issuedAtOffsetSeconds: 3600 }, api.clock.now()),
      )
      assert.equal(refused.status, 401)
      assert.equal(refused.json.reason, 'not_yet_valid')
    })

    test('an identity that is not valid yet is refused', async () => {
      await claim(repository)
      // nbf rather than iat. GitHub does not send one today, which is exactly
      // why an unread nbf would go unnoticed: the token would be accepted
      // inside a window it says it is outside of.
      const refused = await exchange(
        identityToken({ repository, notBeforeOffsetSeconds: 3600 }, api.clock.now()),
      )
      assert.equal(refused.status, 401, JSON.stringify(refused.json))
      assert.equal(refused.json.reason, 'not_yet_valid')
      assert.equal(await liveExchangedTokens(), 0)
    })

    test('a valid signature from a key that is not GitHub is refused', async () => {
      await claim(repository)
      // Correctly signed RS256, correct claims, correct kid. Signed by somebody
      // else. This is the token a verifier that checks the signature parses,
      // rather than that it checks against the published key set, accepts.
      const refused = await exchange(
        identityToken({ repository, key: impostorKey.privateKey }, api.clock.now()),
      )
      assert.equal(refused.status, 401, JSON.stringify(refused.json))
      assert.equal(refused.json.reason, 'invalid_signature')
      assert.equal(await liveExchangedTokens(), 0)
    })

    test('an unsigned identity is refused before the key is even looked at', async () => {
      await claim(repository)
      const refused = await exchange(unsignedToken(repository, api.clock.now()))
      assert.equal(refused.status, 401)
      assert.equal(refused.json.reason, 'bad_algorithm')
      assert.equal(await liveExchangedTokens(), 0)
    })

    test('an identity claiming an HMAC is refused, so the published key is never a secret', async () => {
      await claim(repository)
      // Algorithm confusion: the header says HS256, and a verifier that reaches
      // for "the key" finds GitHub's RSA public key, which anybody can fetch,
      // and uses it as an HMAC secret. The refusal has to come from the
      // algorithm rather than from the signature failing to verify.
      const refused = await exchange(
        identityToken({ repository, algorithm: 'HS256' }, api.clock.now()),
      )
      assert.equal(refused.status, 401)
      assert.equal(refused.json.reason, 'bad_algorithm')
    })

    test('a body with no token is refused with a sentence naming the audience', async () => {
      const res = await api.fetch('/v1/auth/github-oidc', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({}),
      })
      assert.equal(res.status, 400)
      const body = (await res.json()) as { error: string }
      assert.match(body.error, new RegExp(CALLBACK_AUDIENCE))
    })

    // -----------------------------------------------------------------------
    // Revocation
    // -----------------------------------------------------------------------

    test('revoking a claim refuses new exchanges and kills the tokens already out', async () => {
      const created = await claim(repository)
      assert.equal(created.status, 201)
      const issued = await exchange(identityToken({ repository }, api.clock.now()))
      const token = String(issued.json.token)
      assert.ok(await acceptedByIngest(token))

      const admin = await cliToken('owner', ['tokens.manage'])
      const revoked = await call(admin, 'DELETE', `/v1/oidc/bindings/${repository}`)
      assert.equal(revoked.status, 200, JSON.stringify(revoked.json))
      assert.equal(revoked.json.tokensRevoked, 1, 'the credential the claim issued was left live')

      // Both halves. A revocation that stops new exchanges and leaves fifteen
      // minutes of live credentials behind has not revoked anything, and
      // fifteen minutes is exactly the window somebody revoking in a hurry
      // cares about.
      assert.equal(
        await acceptedByIngest(token),
        false,
        'a credential issued by a revoked claim still ingested',
      )
      const again = await exchange(identityToken({ repository }, api.clock.now()))
      assert.equal(again.status, 403)
      assert.equal(again.json.reason, 'binding_revoked')
    })

    test('a repository can be claimed again after its claim was withdrawn', async () => {
      // The message on a revoked claim tells somebody they can claim it again,
      // and until this ran that was a sentence rather than a fact. It works
      // because the unique index covers only live rows, so the withdrawn one
      // stays as the record of who was allowed and when that ended without
      // standing in the way of the next claim.
      assert.equal((await claim(repository)).status, 201)
      const admin = await cliToken('owner', ['tokens.manage'])
      assert.equal((await call(admin, 'DELETE', `/v1/oidc/bindings/${repository}`)).status, 200)
      assert.equal(
        (await exchange(identityToken({ repository }, api.clock.now()))).json.reason,
        'binding_revoked',
      )

      assert.equal((await claim(repository)).status, 201)
      const issued = await exchange(identityToken({ repository }, api.clock.now()))
      assert.equal(issued.status, 200, JSON.stringify(issued.json))
      assert.equal(issued.json.org_id, org.orgId)

      // Both rows are still there, and the list shows the withdrawn one rather
      // than hiding it: after an incident, when a claim was made is the
      // question, and a deleted row cannot answer it.
      const listed = await call(admin, 'GET', '/v1/oidc/bindings')
      const rows = listed.json.bindings as { repository: string; revokedAt: string | null }[]
      assert.equal(rows.filter((r) => r.repository === repository).length, 2)
      assert.equal(rows.filter((r) => r.repository === repository && !r.revokedAt).length, 1)
    })

    test('a suspended installation stops the repository reporting', async () => {
      await claim(repository)
      await api.admin`
        UPDATE github_installations SET suspended_at = now()
        WHERE org_id = ${org.orgId} AND lower(account_login) = ${owner}`
      try {
        const refused = await exchange(identityToken({ repository }, api.clock.now()))
        assert.equal(refused.status, 403, JSON.stringify(refused.json))
        assert.equal(refused.json.reason, 'installation_suspended')
        assert.equal(await liveExchangedTokens(), 0)
      } finally {
        await api.admin`
          UPDATE github_installations SET suspended_at = NULL WHERE org_id = ${org.orgId}`
      }
    })

    test('a suspended organization is refused the credential rather than the ingest', async () => {
      // A DIFFERENT QUESTION FROM THE ONE ABOVE. The installation check asks
      // whether GitHub still says this account is ours. This asks whether we
      // have stopped the customer, and until this ran only /v1/events asked it.
      // A stopped organization was handed a working credential and refused
      // fifteen minutes later on another route, which sends somebody to the
      // reporting path when the answer is their billing state.
      await claim(repository)
      await api.admin`
        UPDATE organizations SET suspended_at = now(), suspended_reason = 'unpaid invoice'
        WHERE id = ${org.orgId}`
      try {
        const refused = await exchange(identityToken({ repository }, api.clock.now()))
        assert.equal(refused.status, 403, JSON.stringify(refused.json))
        assert.equal(refused.json.reason, 'organization_suspended')
        // The reason travels. "403 organization_suspended" tells somebody the
        // shape of their problem; the recorded reason tells them which one.
        assert.match(String(refused.json.error), /unpaid invoice/)
        // Refused BEFORE the write, not beside it. A row carrying a hash is a
        // credential that exists, whatever the response body said.
        assert.equal(await liveExchangedTokens(), 0)
      } finally {
        await api.admin`
          UPDATE organizations SET suspended_at = NULL, suspended_reason = NULL
          WHERE id = ${org.orgId}`
      }

      // And the same exchange succeeds once the suspension lifts, which is what
      // separates a check that reads the column from one that refuses everybody.
      const issued = await exchange(identityToken({ repository }, api.clock.now()))
      assert.equal(issued.status, 200, JSON.stringify(issued.json))
    })

    // -----------------------------------------------------------------------
    // Who may claim a repository
    // -----------------------------------------------------------------------

    test('claiming needs the same scope and role as minting an engine token', async () => {
      const noScope = await cliToken('owner', ['environments.view'])
      const scoped = await call(noScope, 'POST', '/v1/oidc/bindings', { repository })
      assert.equal(scoped.status, 403, 'a token without tokens.manage claimed a repository')

      const member = await cliToken('member', ['tokens.manage'])
      const roled = await call(member, 'POST', '/v1/oidc/bindings', { repository })
      assert.equal(roled.status, 403, 'a member claimed a repository')

      const anonymous = await call(null, 'POST', '/v1/oidc/bindings', { repository })
      assert.equal(anonymous.status, 401)

      // And none of the three left a claim behind, which is the assertion that
      // would still hold if the route returned 403 after writing the row.
      const rows = await api.admin<{ n: string }[]>`
        SELECT count(*) AS n FROM oidc_repository_bindings WHERE org_id = ${org.orgId}`
      assert.equal(Number(rows[0]!.n), 0)
    })

    test('the exchange can read and stamp a claim but never create one', async () => {
      // The policy under test is the one the exchange runs beneath, keyed on
      // the account out of a verified token. Read and stamp are what a
      // successful exchange does; INSERT would let a connection holding only a
      // repository owner's name mint the very permission this table exists to
      // require, so the grant is split rather than left as FOR ALL.
      await claim(repository)
      const stamped = await api.pool.withGitHubAccount(owner, async (db) => {
        const seen = await db.execute<{ id: string }>(
          rawSql`SELECT id FROM oidc_repository_bindings WHERE repository = ${repository}`)
        await db.execute(
          rawSql`UPDATE oidc_repository_bindings SET last_used_at = now() WHERE repository = ${repository}`)
        return seen.length
      })
      assert.equal(stamped, 1, 'the exchange cannot see the claim that authorizes it')

      const refused = await api.pool
        .withGitHubAccount(owner, async (db) => {
          await db.execute(rawSql`
            INSERT INTO oidc_repository_bindings (org_id, repository)
            VALUES (${org.orgId}::uuid, ${`${owner}/minted-by-the-exchange`})`)
        })
        .then(
          () => null,
          (e: unknown) => e,
        )
      assert.ok(refused, 'a connection scoped to a repository owner created a claim')
      const rows = await api.admin<{ n: string }[]>`
        SELECT count(*) AS n FROM oidc_repository_bindings
        WHERE repository = ${`${owner}/minted-by-the-exchange`}`
      assert.equal(Number(rows[0]!.n), 0)
    })

    test('a repository whose owner this organization has no installation on cannot be claimed', async () => {
      // The check that stops a land grab. Without it anybody could claim
      // `some-company/their-app`, and that company's genuine CI would then
      // either be locked out or resolve to the squatter's tenant.
      const admin = await cliToken('owner', ['tokens.manage'])
      const refused = await call(admin, 'POST', '/v1/oidc/bindings', {
        repository: 'some-company/their-app',
      })
      assert.equal(refused.status, 400, JSON.stringify(refused.json))
      assert.equal(refused.json.reason, 'not_installed')
      const rows = await api.admin<{ n: string }[]>`
        SELECT count(*) AS n FROM oidc_repository_bindings WHERE repository = 'some-company/their-app'`
      assert.equal(Number(rows[0]!.n), 0)
    })

    test('a repository already claimed elsewhere cannot be claimed twice', async () => {
      assert.equal((await claim(repository)).status, 201)
      // The second organization holds an installation on its own account, so
      // this is refused by the one-live-claim-per-repository rule rather than
      // by the installation check. Both refusals matter and this is the one
      // that keeps the resolution unambiguous.
      await api.admin`
        INSERT INTO github_installations (org_id, installation_id, account_login, account_type)
        VALUES (${other.orgId}, ${installationId()}, ${owner}, 'Organization')`
      try {
        const admin = await cliToken('owner', ['tokens.manage'], other)
        const refused = await call(admin, 'POST', '/v1/oidc/bindings', { repository })
        assert.equal(refused.status, 409, JSON.stringify(refused.json))
        assert.equal(refused.json.reason, 'already_claimed')

        // And the original claim still resolves where it did.
        const issued = await exchange(identityToken({ repository }, api.clock.now()))
        assert.equal(issued.status, 200)
        assert.equal(issued.json.org_id, org.orgId)
      } finally {
        await api.admin`
          DELETE FROM github_installations
          WHERE org_id = ${other.orgId} AND lower(account_login) = ${owner}`
      }
    })

    test('claiming the same repository twice is the same claim, not a failure', async () => {
      const first = await claim(repository)
      assert.equal(first.status, 201)
      const second = await claim(repository)
      assert.equal(second.status, 201)
      assert.equal(
        (second.json.binding as { id: string }).id,
        (first.json.binding as { id: string }).id,
        'the second claim created a second row',
      )
    })

    test('two administrators claiming at once make one claim, not an error', async () => {
      // Two requests in flight together, which is one person double clicking or
      // two owners on a call with one link pasted into it.
      //
      // Said plainly, because a test that claims more than it shows is worse
      // than none: this does NOT reproduce the interleaving that breaks a
      // read-then-write. It was run against that shape on purpose and passed,
      // because the second request's read lands after the first has committed.
      // The guarantee comes from the partial unique index and the ON CONFLICT
      // in claimRepository, and the test below drives two overlapping
      // transactions directly to show the index doing it. What this one
      // covers is the ordinary observable outcome, which is worth holding on
      // its own: one row, one audit entry, and nobody told their own
      // repository belongs to somebody else.
      const admin = await cliToken('owner', ['tokens.manage'])
      const [first, second] = await Promise.all([
        call(admin, 'POST', '/v1/oidc/bindings', { repository }),
        call(admin, 'POST', '/v1/oidc/bindings', { repository }),
      ])
      assert.equal(first.status, 201, JSON.stringify(first.json))
      assert.equal(second.status, 201, JSON.stringify(second.json))
      assert.equal(
        (first.json.binding as { id: string }).id,
        (second.json.binding as { id: string }).id,
        'the two claims are different rows, so one repository has two live claims',
      )

      const rows = await api.admin<{ n: string }[]>`
        SELECT count(*) AS n FROM oidc_repository_bindings
        WHERE repository = ${repository} AND revoked_at IS NULL`
      assert.equal(Number(rows[0]!.n), 1)

      // And exactly one audit entry, because the second call changed nothing.
      const audited = await api.admin<{ n: string }[]>`
        SELECT count(*) AS n FROM audit_entries
        WHERE org_id = ${org.orgId} AND action = 'oidc_binding.created'`
      assert.equal(Number(audited[0]!.n), 1)
    })

    test('the index refuses a second live claim from inside an overlapping transaction', async () => {
      // The mechanism the fix rests on, driven where the interleaving can be
      // forced rather than hoped for. Both transactions are open and past their
      // reads before either inserts, which is exactly the window a
      // read-then-write cannot survive.
      //
      // Under READ COMMITTED the second insert blocks on the first's index
      // entry and, once that commits, finds the conflict and writes nothing. So
      // the losing transaction gets an empty result rather than an error, which
      // is what lets claimRepository answer with the existing claim instead of
      // refusing a repository the caller already owns.
      let releaseFirst: () => void = () => {}
      const firstMayCommit = new Promise<void>((r) => {
        releaseFirst = r
      })
      let secondHasRead: () => void = () => {}
      const secondRead = new Promise<void>((r) => {
        secondHasRead = r
      })

      const insert = sqlFor(repository)
      const first = api.pool.withTenant({ orgId: org.orgId }, async (db) => {
        const rows = await db.execute<{ id: string }>(insert)
        // Hold the transaction open until the other one has read and tried.
        await Promise.race([secondRead, new Promise((r) => setTimeout(r, 5000))])
        return rows.length
      })
      const second = api.pool.withTenant({ orgId: org.orgId }, async (db) => {
        await db.execute(rawSql`SELECT 1 FROM oidc_repository_bindings WHERE repository = ${repository}`)
        secondHasRead()
        const rows = await db.execute<{ id: string }>(insert)
        return rows.length
      })
      releaseFirst()
      await firstMayCommit
      const [a, b] = await Promise.all([first, second])

      assert.equal(a + b, 1, `the two transactions inserted ${a + b} rows between them`)
      const rows = await api.admin<{ n: string }[]>`
        SELECT count(*) AS n FROM oidc_repository_bindings
        WHERE repository = ${repository} AND revoked_at IS NULL`
      assert.equal(Number(rows[0]!.n), 1)
    })

    test('claims are listed with the audience a workflow has to ask for', async () => {
      await claim(repository)
      const admin = await cliToken('owner', ['tokens.manage'])
      const listed = await call(admin, 'GET', '/v1/oidc/bindings')
      assert.equal(listed.status, 200)
      assert.equal(listed.json.audience, CALLBACK_AUDIENCE)
      const rows = listed.json.bindings as { repository: string; revokedAt: string | null }[]
      assert.deepEqual(
        rows.map((r) => r.repository),
        [repository],
      )
      assert.equal(rows[0]!.revokedAt, null)

      // And another organization's claim is not in it.
      const outside = await cliToken('owner', ['tokens.manage'], other)
      const theirs = await call(outside, 'GET', '/v1/oidc/bindings')
      assert.deepEqual(theirs.json.bindings, [])
    })

    // -----------------------------------------------------------------------
    // Bounds
    // -----------------------------------------------------------------------

    test('one repository cannot mint credentials in a loop', async () => {
      await claim(repository)
      let refused: Record<string, unknown> | null = null
      let refusedHeader: string | null = null
      for (let i = 0; i < 60 && !refused; i += 1) {
        const res = await exchange(identityToken({ repository, runId: i + 1 }, api.clock.now()))
        if (res.status === 429) {
          refused = res.json
          refusedHeader = res.retryAfter
        }
      }
      assert.ok(refused, 'the per-repository bound never refused anything')
      assert.equal(refused.reason, 'rate_limited')
      // The wait the limiter actually computed, not a number chosen at the
      // route. A Retry-After that disagrees with the limiter sends an obedient
      // client back early, where it is refused again for being early.
      const wait = Number(refused.retryAfterSeconds)
      assert.ok(wait >= 1, `retryAfterSeconds was ${wait}, which means retry at once`)
      assert.equal(
        refusedHeader,
        String(wait),
        'the Retry-After header and the body disagree about when to come back',
      )
      // And against the limiter's own arithmetic rather than only against
      // itself. Asserting the header equals the body passes just as well when
      // both are a constant somebody typed at the route, and a Retry-After that
      // disagrees with the bucket sends an obedient client back early, where it
      // is refused again for being early. A second limiter of the same shape,
      // drained the same way, says what the number has to be.
      const reference = repositoryLimiter(new FakeClock())
      let expected = 0
      for (let i = 0; i < 60 && expected === 0; i += 1) {
        const v = reference.take('same-repository-every-time')
        if (!v.allowed) expected = v.retryAfterSeconds
      }
      assert.ok(expected >= 1, 'the reference limiter never refused, so this proves nothing')
      assert.equal(wait, expected, 'the wait is not the one this limiter would compute')
    })

    // -----------------------------------------------------------------------
    // The audit trail
    // -----------------------------------------------------------------------

    test('the claim, the exchange and the revocation are all in the audit log', async () => {
      await claim(repository)
      await exchange(identityToken({ repository }, api.clock.now()))
      const admin = await cliToken('owner', ['tokens.manage'])
      await call(admin, 'DELETE', `/v1/oidc/bindings/${repository}`)

      const rows = await api.admin<{ action: string; detail: Record<string, unknown> }[]>`
        SELECT action, detail FROM audit_entries
        WHERE org_id = ${org.orgId} AND action LIKE 'oidc%' ORDER BY seq`
      assert.deepEqual(
        rows.map((r) => r.action),
        ['oidc_binding.created', 'oidc.token_issued', 'oidc_binding.revoked'],
      )
      // Which workflow file earned the credential, which is what somebody reads
      // when a token they did not expect turns up in the log.
      assert.match(
        String(rows[1]!.detail.jobWorkflowRef),
        /\.github\/workflows\/antifailure\.yml@/,
      )
      // And no entry carries anything usable as a credential.
      for (const row of rows) {
        assert.doesNotMatch(JSON.stringify(row.detail), /aft_/, 'an audit entry carries a token')
      }
    })
  },
)
