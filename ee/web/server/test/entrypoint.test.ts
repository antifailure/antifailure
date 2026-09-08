// Does a licensed request reach an enterprise route, and is an unlicensed one
// refused rather than merely unanswered.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// THREE PROCESSES, AND THE THIRD IS THE ONE THAT MAKES THE OTHER TWO MEAN
// SOMETHING.
//
//   licensed enterprise    200, answered by the ee/web package
//   unlicensed enterprise  402, refused by the gate, naming the feature
//   community              404, which is what absence actually looks like
//
// Without the third, 402 and 404 are just two numbers and "refused by the gate
// rather than by absence" is an assertion about a code path nobody watched. The
// community process is the control: same database, same request, same seeded
// connection row, and it answers 404 because it mounts nothing. That is also
// the before-state of this whole lane, reproduced on demand, which is the only
// honest way to show what was fixed.

import { describe, it, before, after } from 'node:test'
import assert from 'node:assert/strict'
import postgres from 'postgres'
import { migrate } from '@antifailure/db'
import {
  COMMUNITY_ENTRY,
  ENTERPRISE_ENTRY,
  adminUrl,
  available,
  licenseFor,
  seed,
  signingKey,
  startEntryPoint,
  type Running,
  type Seeded,
} from './harness.ts'

const hasDatabase = await available()

describe('the enterprise entry point', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let admin: postgres.Sql
  let org: Seeded
  let key: ReturnType<typeof signingKey>
  let licensed: Running
  let unlicensed: Running
  let community: Running

  before(async () => {
    admin = postgres(adminUrl, { max: 4, connect_timeout: 30, onnotice: () => {} })
    await migrate(admin)
    await admin.unsafe(`ALTER ROLE antifailure_app LOGIN PASSWORD 'app-test-password'`)
    org = await seed(admin)
    key = signingKey()

    licensed = await startEntryPoint(ENTERPRISE_ENTRY, {
      AF_ORG: org.slug,
      AF_LICENSE_PUBLIC_KEYS: key.publicKeys,
      AF_LICENSE_KEY: licenseFor(key, org.slug, ['sso', 'scim'], { seats: 25 }),
    })
    // Deliberately not "no keys and no licence". The keys ARE trusted here and
    // there is simply no licence, which is the state a customer who has
    // installed the enterprise image and not yet pasted a key is in, and it is
    // the state that must refuse rather than 404.
    unlicensed = await startEntryPoint(ENTERPRISE_ENTRY, {
      AF_ORG: org.slug,
      AF_LICENSE_PUBLIC_KEYS: key.publicKeys,
    })
    community = await startEntryPoint(COMMUNITY_ENTRY, {})
  })

  after(async () => {
    await licensed?.stop()
    await unlicensed?.stop()
    await community?.stop()
    if (admin) {
      await admin`DELETE FROM organizations WHERE id = ${org.orgId}`
      await admin.end({ timeout: 5 })
    }
  })

  // -------------------------------------------------------------------------
  // The positive: a licensed request is answered by the enterprise package
  // -------------------------------------------------------------------------

  it('answers a SAML metadata request from ee/web/sso', async () => {
    const response = await licensed.get(`/sso/saml/${org.handle}/metadata`)
    assert.equal(response.status, 200)
    // The content type and the document, not just the status. A 200 could come
    // from anything; only ee/web/sso/src/saml/request.ts produces this, and the
    // entity id in it is built from the base URL this process was configured
    // with, so the assertion proves the route ran with this process's own
    // configuration rather than that something somewhere answered.
    assert.match(response.headers.get('content-type') ?? '', /samlmetadata\+xml/)
    const body = await response.text()
    assert.match(body, /EntityDescriptor/)
    assert.ok(
      body.includes(`https://enterprise.test/sso/saml/${org.handle}/acs`),
      `the assertion consumer URL is not this process's own:\n${body}`,
    )
  })

  it('answers a SCIM configuration request from ee/web/scim', async () => {
    const response = await licensed.get('/scim/v2/ServiceProviderConfig')
    assert.equal(response.status, 200)
    const body = (await response.json()) as { schemas?: string[]; patch?: { supported?: boolean } }
    assert.ok(
      body.schemas?.some((s) => s.includes('ServiceProviderConfig')),
      `not a SCIM ServiceProviderConfig: ${JSON.stringify(body)}`,
    )
  })

  it('says what it mounted and what the license permits', async () => {
    // The startup lines are part of the fix, not decoration. A community log
    // and an enterprise log whose registration silently failed were identical
    // documents, which is exactly how four packages went unmounted without a
    // single line of evidence anywhere.
    const said = licensed.output()
    assert.match(said, /extension sso is mounted: .*\/sso\/saml\/:handle\/metadata/)
    assert.match(said, /extension scim is mounted: /)
    assert.match(said, /enterprise features permitted right now: scim, sso/)
  })

  // -------------------------------------------------------------------------
  // The negative: refused, and refused by the gate
  // -------------------------------------------------------------------------

  it('refuses the same request with 402 when there is no license', async () => {
    const response = await unlicensed.get(`/sso/saml/${org.handle}/metadata`)
    assert.equal(
      response.status,
      402,
      `an unlicensed request answered ${response.status}. 404 would mean the route was never ` +
        `mounted, which is refusal by absence and is the thing this gate exists not to do.`,
    )
    const body = (await response.json()) as { error?: string; feature?: string; licenseState?: string; detail?: string }
    assert.equal(body.error, 'not_licensed')
    assert.equal(body.feature, 'sso')
    assert.equal(body.licenseState, 'none')
    // The sentence has to name what to do. A refusal an operator cannot act on
    // sends them to read the source, which is where this lane started.
    assert.match(body.detail ?? '', /AF_LICENSE_KEY/)
  })

  it('refuses SCIM the same way and names scim rather than sso', async () => {
    // Per feature, not per edition. A gate that refused everything under one
    // name would be indistinguishable from a licence check nobody wired per
    // package, and would tell a customer who bought single sign-on and not
    // provisioning the wrong thing.
    const response = await unlicensed.get('/scim/v2/ServiceProviderConfig')
    assert.equal(response.status, 402)
    const body = (await response.json()) as { feature?: string }
    assert.equal(body.feature, 'scim')
  })

  it('mounts the routes it refuses, and says so at startup', async () => {
    // The distinction Wave 7 turns on: absent says we did not build it, ungated
    // says we built it and do not charge for it, and refused says we built it
    // and you have not bought it. Only the third is true here and only this
    // line proves the process knows the difference.
    const said = unlicensed.output()
    assert.match(said, /extension sso is mounted: /)
    assert.match(said, /extension scim is mounted: /)
    assert.match(said, /no license is installed: every enterprise route is mounted and refuses/)
    assert.match(said, /enterprise features permitted right now: none/)
  })

  // -------------------------------------------------------------------------
  // The control: what absence actually looks like
  // -------------------------------------------------------------------------

  it('the community entry point falls through to the catch-all on the same paths', async () => {
    // The before-state, reproduced. Every customer of every release until this
    // commit got exactly this from every enterprise path, against a database
    // that holds a perfectly good SSO connection row.
    //
    // 404 or 503, and which one is not the point. An enterprise path on the
    // community server reaches the console catch-all, which answers 404 when a
    // console build is present and 503 when it is not, and this checkout has
    // not built one. Both are the server saying it has no such route. Neither
    // is 200 and neither is 402, and that pair is the whole claim: the gate's
    // refusal is a decision this process made, and absence is what it looks
    // like when nobody made one.
    for (const pathname of [`/sso/saml/${org.handle}/metadata`, '/scim/v2/ServiceProviderConfig']) {
      const response = await community.get(pathname)
      assert.ok(
        response.status === 404 || response.status === 503,
        `${pathname} answered ${response.status} on the community server, which is neither of the ` +
          `two ways this server says it has no such route`,
      )
      const body = await response.text()
      assert.doesNotMatch(body, /EntityDescriptor/, `${pathname} served SAML metadata on the community server`)
      assert.doesNotMatch(body, /not_licensed/, `${pathname} reached a licence gate on the community server`)
      assert.doesNotMatch(body, /ServiceProviderConfig/, `${pathname} served SCIM config on the community server`)
    }
  })

  it('the community entry point says it mounted nothing', async () => {
    assert.match(community.output(), /no extensions are registered: this is the community control plane/)
    // And the negative control for the assertion above: the community process
    // must not be printing an enterprise mount line, or the check that the
    // enterprise one does would pass on any process at all.
    assert.doesNotMatch(community.output(), /extension sso is mounted/)
  })

  // -------------------------------------------------------------------------
  // The licence is not a boot-time constant
  // -------------------------------------------------------------------------

  it('refuses a license that has run out, on a process that started fine', async () => {
    // Evaluated per request against the clock, not once at startup. A gate
    // decided at boot cannot expire: a control plane up for six weeks would
    // keep honouring a licence that ran out in the third and nothing would say
    // so. The licence here expired long enough ago that its grace period is
    // over before the process even starts, which is the same code path a long
    // running process reaches by waiting.
    const expired = await startEntryPoint(ENTERPRISE_ENTRY, {
      AF_ORG: org.slug,
      AF_LICENSE_PUBLIC_KEYS: key.publicKeys,
      AF_LICENSE_KEY: licenseFor(key, org.slug, ['sso', 'scim'], {
        expiresAt: new Date(Date.now() - 90 * 24 * 3600 * 1000).toISOString(),
      }),
    })
    try {
      const response = await expired.get(`/sso/saml/${org.handle}/metadata`)
      assert.equal(response.status, 402)
      const body = (await response.json()) as { licenseState?: string }
      assert.equal(body.licenseState, 'expired')
      // It started, rather than refusing to. An expired licence is an ordinary
      // commercial event and taking the control plane down over one would be a
      // worse product than one that degrades.
      assert.match(expired.output(), /the license is expired/)
    } finally {
      await expired.stop()
    }
  })

  it('refuses a license issued to somebody else', async () => {
    const other = await startEntryPoint(ENTERPRISE_ENTRY, {
      AF_ORG: org.slug,
      AF_LICENSE_PUBLIC_KEYS: key.publicKeys,
      AF_LICENSE_KEY: licenseFor(key, `not-${org.slug}`, ['sso', 'scim']),
    })
    try {
      const response = await other.get('/scim/v2/ServiceProviderConfig')
      assert.equal(response.status, 402)
      const body = (await response.json()) as { licenseState?: string }
      assert.equal(body.licenseState, 'wrong_org')
    } finally {
      await other.stop()
    }
  })

  it('refuses to start at all on a license that does not parse', async () => {
    // The opposite direction from expiry, deliberately. An expired licence is
    // commercial and the software keeps running; a licence that does not parse
    // is somebody pasting the wrong thing into a deployment, and an enterprise
    // control plane that started as a community one because of a typo is the
    // failure this lane exists to end.
    await assert.rejects(
      () =>
        startEntryPoint(ENTERPRISE_ENTRY, {
          AF_ORG: org.slug,
          AF_LICENSE_PUBLIC_KEYS: key.publicKeys,
          AF_LICENSE_KEY: 'aflic_not-a-licence.also-not',
        }),
      /exited 2 before listening[\s\S]*license key was refused/,
    )
  })
})
