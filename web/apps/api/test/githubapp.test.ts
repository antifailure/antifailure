// Acting as the GitHub App, and believing a delivery.
//
// The claims worth testing are not "it signs a JWT" and "it computes an HMAC".
// They are the ones where getting it subtly wrong produces something that works
// in testing and fails in production, or worse, works everywhere and is not
// secure:
//
//   - the signature is checked over the RAW body, so a payload whose
//     re-serialisation differs still verifies;
//   - a wrong signature, a missing header and a signature for a different
//     secret are all refused, and refused the same way;
//   - a delivery can write rows for the account it names and for no other,
//     even when a handler is handed the wrong organization;
//   - an uninstall does not delete the history of what happened.

import { test, describe, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { createHmac, generateKeyPairSync } from 'node:crypto'
import {
  appConfigFrom,
  appJwt,
  GitHubAppError,
  InstallationTokens,
  normalisePem,
  verifySignature,
} from '../src/github/app.ts'
import { handleDelivery, slugFor } from '../src/github/webhook.ts'
import { available, startApi, dropOrg, type ApiHarness } from './harness.ts'
import type { Clock } from '../src/clock.ts'

const { privateKey } = generateKeyPairSync('rsa', {
  modulusLength: 2048,
  privateKeyEncoding: { type: 'pkcs8', format: 'pem' },
  publicKeyEncoding: { type: 'spki', format: 'pem' },
})

const SECRET = 'a-webhook-secret'

/** A clock that does not move, so the JWT claims are checkable arithmetic
 *  rather than a range. sleep resolves immediately: nothing here waits, and a
 *  test that actually slept would be one somebody eventually deletes. */
const frozen: Clock = {
  now: () => new Date('2026-08-28T12:00:00Z'),
  monotonicMs: () => 0,
  sleep: async () => {},
}

function sign(body: string, secret = SECRET): string {
  return 'sha256=' + createHmac('sha256', secret).update(body, 'utf8').digest('hex')
}

// ---------------------------------------------------------------------------

describe('reading the App configuration', () => {
  test('nothing set is null, not an error', () => {
    // A control plane with no GitHub App is a supported installation. Throwing
    // here would turn "you have not created an App yet" into a crash loop.
    assert.equal(appConfigFrom({}), null)
  })

  test('half configured is refused', () => {
    // The dangerous middle. A webhook secret with no private key produces an
    // endpoint that verifies deliveries perfectly and can do nothing with
    // them, which looks like it is working.
    assert.throws(
      () => appConfigFrom({ AF_GITHUB_APP_WEBHOOK_SECRET: SECRET }),
      /half configured/,
    )
    assert.throws(
      () => appConfigFrom({ AF_GITHUB_APP_ID: '1', AF_GITHUB_APP_PRIVATE_KEY: privateKey }),
      /half configured/,
    )
  })

  test('a PEM flattened by an environment variable is put back together', () => {
    // Docker -e, a Kubernetes secret edited by hand, and a shell that lost its
    // quoting all turn the newlines into backslash-n. The result is a
    // valid-looking key that Node refuses with a message about DECODER
    // routines, which sends whoever reads it somewhere else entirely.
    const flattened = privateKey.replace(/\n/g, '\\n')
    assert.notEqual(flattened, privateKey)
    assert.equal(normalisePem(flattened).trim(), privateKey.trim())
  })

  test('a base64 PEM is accepted, because it survives every transport', () => {
    const encoded = Buffer.from(privateKey, 'utf8').toString('base64')
    assert.equal(normalisePem(encoded).trim(), privateKey.trim())
  })

  test('an empty value is empty rather than a broken key', () => {
    assert.equal(normalisePem(''), '')
    assert.equal(normalisePem('   '), '')
  })
})

describe('the App JWT', () => {
  const clock = frozen
  const config = { appId: '4756201', privateKey, webhookSecret: SECRET }

  test('is issued in the past and expires inside ten minutes', () => {
    // GitHub refuses a token issued in the future and one older than ten
    // minutes. A container whose clock is a few seconds ahead of GitHub's is
    // ordinary, and the 401 it produces says nothing about clocks.
    const [, payload] = appJwt(config, clock).split('.')
    const claims = JSON.parse(Buffer.from(payload!, 'base64url').toString('utf8')) as {
      iat: number
      exp: number
      iss: string
    }
    const now = Math.floor(clock.now().getTime() / 1000)
    assert.ok(claims.iat < now, 'iat must be backdated')
    assert.ok(now - claims.iat <= 120, 'iat must not be backdated absurdly')
    assert.ok(claims.exp - now < 600, 'exp must be inside the ten minutes GitHub allows')
    assert.ok(claims.exp > now, 'exp must be in the future')
    assert.equal(claims.iss, '4756201')
  })

  test('is RS256 and carries a signature', () => {
    const [header, , signature] = appJwt(config, clock).split('.')
    assert.equal(
      JSON.parse(Buffer.from(header!, 'base64url').toString('utf8')).alg,
      'RS256',
    )
    assert.ok((signature ?? '').length > 100)
  })

  test('a key Node cannot read is named as such, not left to the cipher', () => {
    assert.throws(
      () => appJwt({ ...config, privateKey: 'not a key' }, clock),
      (err: Error) => err instanceof GitHubAppError && /not a private key/.test(err.message),
    )
  })
})

describe('verifying a delivery', () => {
  const body = JSON.stringify({ action: 'created', emoji: '🎉', n: 1.0 })

  test('a real signature passes', () => {
    assert.ok(verifySignature(SECRET, body, sign(body)))
  })

  test('the raw body is what is signed, not a re-serialised one', () => {
    // THE ONE THAT BREAKS IN PRODUCTION. A verifier that re-serialises works
    // until a payload contains a unicode escape, a float that formats
    // differently, or keys in an order JSON.stringify does not reproduce.
    const raw = '{"b":1,"a":2,"s":"\\u00e9"}'
    assert.notEqual(JSON.stringify(JSON.parse(raw)), raw)
    assert.ok(verifySignature(SECRET, raw, sign(raw)))
  })

  test('a missing header is refused rather than throwing', () => {
    assert.equal(verifySignature(SECRET, body, undefined), false)
    assert.equal(verifySignature(SECRET, body, ''), false)
  })

  test('a signature for another secret is refused', () => {
    assert.equal(verifySignature(SECRET, body, sign(body, 'someone-elses-secret')), false)
  })

  test('a signature of the wrong length is refused rather than throwing', () => {
    // timingSafeEqual throws on a length mismatch, and a throw here would be a
    // 500 for what is simply a wrong signature.
    assert.equal(verifySignature(SECRET, body, 'sha256=short'), false)
    assert.equal(verifySignature(SECRET, body, 'nonsense'), false)
  })

  test('a body changed by one byte is refused', () => {
    const signature = sign(body)
    assert.equal(verifySignature(SECRET, body.replace('created', 'deleted'), signature), false)
  })
})

describe('installation tokens', () => {
  const clock = frozen
  const config = { appId: '1', privateKey, webhookSecret: SECRET }

  test('are cached, so a page of ten repositories mints one token', async () => {
    let calls = 0
    const tokens = new InstallationTokens(config, clock, (async () => {
      calls++
      return new Response(
        JSON.stringify({ token: 'ghs_x', expires_at: '2026-08-28T13:00:00Z' }),
        { status: 200, headers: { 'content-type': 'application/json' } },
      )
    }) as unknown as typeof fetch)

    assert.equal(await tokens.for(42), 'ghs_x')
    assert.equal(await tokens.for(42), 'ghs_x')
    assert.equal(await tokens.for(42), 'ghs_x')
    assert.equal(calls, 1)
  })

  test('a token near its expiry is replaced, not used', async () => {
    // Expired a minute early on purpose. A token valid when this process checks
    // it and expired when GitHub checks it is a 401 in the middle of an
    // operation, and a minute is longer than any request here takes.
    let calls = 0
    const tokens = new InstallationTokens(config, clock, (async () => {
      calls++
      return new Response(
        JSON.stringify({ token: `ghs_${calls}`, expires_at: '2026-08-28T12:00:30Z' }),
        { status: 200, headers: { 'content-type': 'application/json' } },
      )
    }) as unknown as typeof fetch)

    assert.equal(await tokens.for(42), 'ghs_1')
    assert.equal(await tokens.for(42), 'ghs_2')
    assert.equal(calls, 2)
  })

  test('a 404 says what it usually means', async () => {
    // GitHub answers 404 for an installation this App cannot see, which is what
    // a wrong App ID looks like rather than a missing installation. Somebody
    // reading "404" goes and looks at the installation, which is fine.
    const tokens = new InstallationTokens(config, clock, (async () =>
      new Response('{}', { status: 404 })) as unknown as typeof fetch)
    await assert.rejects(() => tokens.for(42), /App ID does not match the private key/)
  })
})

describe('slugs', () => {
  test('an uppercase login becomes a slug the constraint accepts', () => {
    // organizations_slug_shape is ^[a-z0-9][a-z0-9-]{0,62}$. A login with a
    // capital letter would violate it, so an installation from such an account
    // would fail on a constraint rather than work.
    assert.equal(slugFor('AntiFailure'), 'antifailure')
    assert.equal(slugFor('Some.Org'), 'some-org')
    assert.match(slugFor('a'.repeat(80)), /^[a-z0-9][a-z0-9-]{0,62}$/)
  })

  test('a login with no usable characters is refused rather than producing ""', () => {
    // An empty slug would violate the constraint too, with an error naming the
    // constraint rather than the login.
    assert.throws(() => slugFor('---'), /no characters/)
  })
})

// ---------------------------------------------------------------------------

describe('what a delivery writes', {
  skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let api: ApiHarness
  const account = { login: 'delivery-test-org', type: 'Organization' }
  const clock = frozen

  before(async () => {
    api = await startApi()
  })
  after(async () => {
    const rows = await api.admin<{ id: string }[]>`
      SELECT id FROM organizations WHERE github_login IN ('delivery-test-org', 'other-org')`
    for (const row of rows) await dropOrg(api.admin, row.id)
    await api.close()
  })

  const installation = { id: 900123, account }

  test('an installation creates the organization and the installation row', async () => {
    // The row sign-in reads to decide which organizations somebody may enter.
    // Nothing wrote it before this: every person who signed in landed in no
    // organization, and the console rendered an empty state that looked like a
    // bug rather than like a missing installation.
    const out = await handleDelivery(api.pool, clock, 'installation', {
      action: 'created',
      installation,
      repositories: [{ id: 1, full_name: 'delivery-test-org/app', private: true, default_branch: 'main' }],
    })
    assert.equal(out.handled, true)

    const [org] = await api.admin<{ id: string; slug: string }[]>`
      SELECT id, slug FROM organizations WHERE github_login = 'delivery-test-org'`
    assert.ok(org, 'no organization was created')
    assert.equal(org!.slug, 'delivery-test-org')

    const [inst] = await api.admin<{ installation_id: string; suspended_at: Date | null }[]>`
      SELECT installation_id, suspended_at FROM github_installations WHERE installation_id = 900123`
    assert.ok(inst)
    assert.equal(inst!.suspended_at, null)

    const [repo] = await api.admin<{ full_name: string; archived_at: Date | null }[]>`
      SELECT full_name, archived_at FROM repositories WHERE org_id = ${org!.id}`
    assert.equal(repo!.full_name, 'delivery-test-org/app')
    assert.equal(repo!.archived_at, null)
  })

  test('the same installation arriving twice is not a second organization', async () => {
    // GitHub redelivers. An endpoint that created a tenant per delivery would
    // turn a retry into a duplicate customer.
    await handleDelivery(api.pool, clock, 'installation', {
      action: 'new_permissions_accepted',
      installation,
      repositories: [{ id: 1, full_name: 'delivery-test-org/app' }],
    })
    const rows = await api.admin<{ n: number }[]>`
      SELECT count(*)::int AS n FROM organizations WHERE github_login = 'delivery-test-org'`
    assert.equal(rows[0]!.n, 1)
  })

  test('adding a repository records it, removing one archives rather than deletes', async () => {
    await handleDelivery(api.pool, clock, 'installation_repositories', {
      action: 'added',
      installation,
      repositories_added: [{ id: 2, full_name: 'delivery-test-org/docs' }],
      repositories_removed: [],
    })
    const [added] = await api.admin<{ full_name: string }[]>`
      SELECT full_name FROM repositories WHERE full_name = 'delivery-test-org/docs'`
    assert.ok(added)

    await handleDelivery(api.pool, clock, 'installation_repositories', {
      action: 'removed',
      installation,
      repositories_added: [],
      repositories_removed: [{ id: 2, full_name: 'delivery-test-org/docs' }],
    })
    // THE ROW IS STILL THERE. A repository removed from an installation has
    // runs, verdicts and artifacts that happened, and deleting it cascades them
    // away. The history of what this product found is the product.
    const [archived] = await api.admin<{ archived_at: Date | null }[]>`
      SELECT archived_at FROM repositories WHERE full_name = 'delivery-test-org/docs'`
    assert.ok(archived, 'the repository row was deleted rather than archived')
    assert.notEqual(archived!.archived_at, null)
  })

  test('re-adding a repository un-archives it', async () => {
    await handleDelivery(api.pool, clock, 'installation_repositories', {
      action: 'added',
      installation,
      repositories_added: [{ id: 2, full_name: 'delivery-test-org/docs' }],
      repositories_removed: [],
    })
    const [row] = await api.admin<{ archived_at: Date | null }[]>`
      SELECT archived_at FROM repositories WHERE full_name = 'delivery-test-org/docs'`
    assert.equal(row!.archived_at, null)
  })

  test('an uninstall suspends rather than forgets', async () => {
    // Sign-in already ignores a suspended installation, so the access
    // consequence is identical. What is kept is the answer to "when did they
    // uninstall", which is the first question anybody asks about a customer
    // who left.
    await handleDelivery(api.pool, clock, 'installation', { action: 'deleted', installation })
    const [row] = await api.admin<{ suspended_at: Date | null }[]>`
      SELECT suspended_at FROM github_installations WHERE installation_id = 900123`
    assert.ok(row, 'the installation row was deleted')
    assert.notEqual(row!.suspended_at, null)
  })

  test('reinstalling clears the suspension', async () => {
    await handleDelivery(api.pool, clock, 'installation', { action: 'created', installation })
    const [row] = await api.admin<{ suspended_at: Date | null }[]>`
      SELECT suspended_at FROM github_installations WHERE installation_id = 900123`
    assert.equal(row!.suspended_at, null)
  })

  test('a delivery cannot write a repository into another tenant', async () => {
    // The claim the row-level security policy exists for. The payload names one
    // account; a repository whose full_name points somewhere else still lands
    // under that account's organization, because the policy keys on the
    // installation rather than on the name in the string.
    await handleDelivery(api.pool, clock, 'installation', {
      action: 'created',
      installation: { id: 900999, account: { login: 'other-org', type: 'Organization' } },
      repositories: [{ id: 9, full_name: 'delivery-test-org/app' }],
    })

    const rows = await api.admin<{ slug: string }[]>`
      SELECT o.slug FROM repositories r JOIN organizations o ON o.id = r.org_id
      WHERE r.full_name = 'delivery-test-org/app' ORDER BY o.slug`
    // One row per organization that legitimately has it, and the two are
    // separate rows in separate tenants rather than one row that moved.
    assert.deepEqual(rows.map((r) => r.slug), ['delivery-test-org', 'other-org'])
  })

  test('a repository event carries no installation.account, and is handled anyway', async () => {
    // The shape this got wrong for its whole life. GitHub sends the FULL
    // installation object -- the one with `account` on it -- only on
    // `installation` and `installation_repositories`. Every other event, this
    // one included, carries the minimal `{ id, node_id }`. Reading the account
    // off it meant every repository delivery answered "no repository in the
    // payload", with a 200, so nothing retried and nothing was logged as
    // wrong. Real deliveries from the staging App confirmed it: installation
    // keys were exactly ['id', 'node_id'].
    //
    // The owner is on the repository and on the organization, in the same
    // signed body, and is trusted for exactly the same reason.
    const out = await handleDelivery(api.pool, clock, 'repository', {
      action: 'edited',
      installation: { id: 900123, node_id: 'MDIzOk...' },
      organization: { login: 'delivery-test-org' },
      repository: {
        id: 77,
        full_name: 'delivery-test-org/renamed',
        private: false,
        default_branch: 'trunk',
        owner: { login: 'delivery-test-org', type: 'Organization' },
      },
    })
    assert.equal(out.handled, true, out.detail)

    const [row] = await api.admin<{ full_name: string; default_branch: string }[]>`
      SELECT full_name, default_branch FROM repositories
      WHERE full_name = 'delivery-test-org/renamed'`
    assert.ok(row, 'the repository event wrote nothing')
    assert.equal(row!.default_branch, 'trunk')
  })

  test('a repository event with only repository.owner still names its account', async () => {
    // A repository owned by a personal account has no `organization` on the
    // payload at all. The owner on the repository is the fallback, and it is
    // the reason this reads two places rather than one.
    const out = await handleDelivery(api.pool, clock, 'repository', {
      action: 'created',
      installation: { id: 900123, node_id: 'x' },
      repository: {
        id: 78,
        full_name: 'delivery-test-org/second',
        owner: { login: 'delivery-test-org', type: 'Organization' },
      },
    })
    assert.equal(out.handled, true, out.detail)
    const [row] = await api.admin<{ full_name: string }[]>`
      SELECT full_name FROM repositories WHERE full_name = 'delivery-test-org/second'`
    assert.ok(row)
  })

  test('a repository event that names no owner at all is answered, not thrown', async () => {
    const out = await handleDelivery(api.pool, clock, 'repository', {
      action: 'edited',
      installation: { id: 900123, node_id: 'x' },
      repository: { id: 79, full_name: 'nobody/nothing' },
    })
    assert.equal(out.handled, false)
    assert.match(out.detail, /no repository/)
  })

  test('a repository event archives on delete', async () => {
    await handleDelivery(api.pool, clock, 'repository', {
      action: 'deleted',
      installation: { id: 900123, node_id: 'x' },
      organization: { login: 'delivery-test-org' },
      repository: {
        id: 78,
        full_name: 'delivery-test-org/second',
        owner: { login: 'delivery-test-org', type: 'Organization' },
      },
    })
    const [row] = await api.admin<{ archived_at: Date | null }[]>`
      SELECT archived_at FROM repositories WHERE full_name = 'delivery-test-org/second'`
    assert.ok(row, 'the row was deleted rather than archived')
    assert.notEqual(row!.archived_at, null)
  })

  test('an event this control plane does not act on is answered, not failed', async () => {
    // GitHub retries a 5xx. Answering 500 to an event that will be refused
    // identically every time is a retry storm.
    for (const event of ['push', 'pull_request', 'member', 'organization', 'star']) {
      const out = await handleDelivery(api.pool, clock, event, { action: 'created' })
      assert.equal(out.handled, false, event)
      assert.ok(out.detail.length > 0, event)
    }
  })

  test('a ping is acknowledged, because that is what GitHub sends first', async () => {
    const out = await handleDelivery(api.pool, clock, 'ping', {})
    assert.equal(out.handled, true)
  })

  test('a payload with no installation is answered rather than throwing', async () => {
    const out = await handleDelivery(api.pool, clock, 'installation', { action: 'created' })
    assert.equal(out.handled, false)
    assert.match(out.detail, /no installation/)
  })

  // -------------------------------------------------------------------------
  // Orderings. GitHub does not promise that deliveries arrive in the order the
  // events happened: a delivery that answered 5xx is retried for hours, and two
  // deliveries in flight at once land in whatever order the network gives them.
  // So every ordering that can reach `suspended_at` gets a case here, because
  // that column is what sign-in reads to decide who may enter a tenant.
  // -------------------------------------------------------------------------

  test('a repository event arriving after a suspend does not un-suspend', async () => {
    // suspend-then-repository. The owner suspends the App; a `repository`
    // delivery from before the suspension is retried afterwards. The retry must
    // not restore access the owner took away.
    await handleDelivery(api.pool, clock, 'installation', { action: 'suspend', installation })
    await handleDelivery(api.pool, clock, 'repository', {
      action: 'edited',
      installation: { id: 900123, node_id: 'x' },
      organization: { login: 'delivery-test-org' },
      repository: {
        id: 77,
        full_name: 'delivery-test-org/renamed',
        owner: { login: 'delivery-test-org', type: 'Organization' },
      },
    })
    const [row] = await api.admin<{ suspended_at: Date | null }[]>`
      SELECT suspended_at FROM github_installations WHERE installation_id = 900123`
    assert.notEqual(
      row!.suspended_at,
      null,
      'a repository delivery restored a suspended installation, and sign-in grants membership on suspended_at IS NULL',
    )
  })

  test('an installation_repositories event arriving after an uninstall does not un-suspend', async () => {
    // deleted-then-installation_repositories. Same shape, different event, and
    // it is the one that carries the full installation object, so it looked
    // most like a legitimate reason to write the row.
    await handleDelivery(api.pool, clock, 'installation', { action: 'deleted', installation })
    await handleDelivery(api.pool, clock, 'installation_repositories', {
      action: 'added',
      installation,
      repositories_added: [{ id: 2, full_name: 'delivery-test-org/docs' }],
      repositories_removed: [],
    })
    const [row] = await api.admin<{ suspended_at: Date | null }[]>`
      SELECT suspended_at FROM github_installations WHERE installation_id = 900123`
    assert.notEqual(row!.suspended_at, null, 'an uninstalled App was reconnected by a repository list')
    // The repository still lands: recording what the account owns is harmless
    // and is not what grants anybody access.
    const [repo] = await api.admin<{ archived_at: Date | null }[]>`
      SELECT archived_at FROM repositories WHERE full_name = 'delivery-test-org/docs'`
    assert.ok(repo)
  })

  test('unsuspend and reinstall still clear the suspension', async () => {
    // The other half, so the fix above cannot be "never clear it". These are
    // the two deliveries that mean the installation is live again, and they are
    // the only two allowed to say so.
    await handleDelivery(api.pool, clock, 'installation', { action: 'unsuspend', installation })
    const [unsuspended] = await api.admin<{ suspended_at: Date | null }[]>`
      SELECT suspended_at FROM github_installations WHERE installation_id = 900123`
    assert.equal(unsuspended!.suspended_at, null)

    await handleDelivery(api.pool, clock, 'installation', { action: 'suspend', installation })
    await handleDelivery(api.pool, clock, 'installation', { action: 'created', installation })
    const [reinstalled] = await api.admin<{ suspended_at: Date | null }[]>`
      SELECT suspended_at FROM github_installations WHERE installation_id = 900123`
    assert.equal(reinstalled!.suspended_at, null)
  })
})

// ---------------------------------------------------------------------------

describe('the webhook endpoint', {
  skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  // The endpoint rather than the handler. What is new here is that this is the
  // only unauthenticated route in the server that writes anything, so the tests
  // are about what it refuses.

  let api: ApiHarness

  before(async () => {
    api = await startApi({ githubWebhookSecret: SECRET })
  })
  after(async () => {
    const rows = await api.admin<{ id: string }[]>`
      SELECT id FROM organizations
      WHERE github_login IN ('endpoint-test-org', 'replay-test-org', 'concurrent-test-org')`
    for (const row of rows) await dropOrg(api.admin, row.id)
    await api.admin`DELETE FROM github_deliveries WHERE delivery_id LIKE 'test-delivery-%'
      OR delivery_id LIKE 'replay-fence-%' OR delivery_id LIKE 'concurrent-fence-%'`
    await api.close()
  })

  // A DIFFERENT DELIVERY IDENTIFIER EVERY TIME, unless a test asks for the same
  // one. That is what GitHub does, and the default used to be one constant
  // here, which made every delivery after the first a replay the moment the
  // ledger existed. Worth keeping as a shape: a fixture that reuses an
  // identifier is a fixture that cannot see a replay fence working OR failing.
  let deliveries = 0
  async function deliver(
    event: string,
    payload: unknown,
    opts: { signature?: string; secret?: string; deliveryId?: string } = {},
  ) {
    const body = JSON.stringify(payload)
    const headers: Record<string, string> = {
      'content-type': 'application/json',
      'x-github-event': event,
      'x-github-delivery': opts.deliveryId ?? `test-delivery-${(deliveries += 1)}`,
    }
    const signature = opts.signature ?? sign(body, opts.secret ?? SECRET)
    if (signature) headers['x-hub-signature-256'] = signature
    const res = await api.fetch('/webhooks/github', { method: 'POST', headers, body })
    return { status: res.status, body: await res.text() }
  }

  test('an unsigned delivery is refused and writes nothing', async () => {
    const res = await deliver(
      'installation',
      {
        action: 'created',
        installation: { id: 901000, account: { login: 'endpoint-test-org', type: 'Organization' } },
      },
      { signature: '' },
    )
    assert.equal(res.status, 401)
    const rows = await api.admin<{ n: number }[]>`
      SELECT count(*)::int AS n FROM organizations WHERE github_login = 'endpoint-test-org'`
    assert.equal(rows[0]!.n, 0, 'an unsigned delivery created a tenant')
  })

  test('a delivery signed with the wrong secret is refused the same way', async () => {
    // The same answer for unsigned, wrongly signed, and signed for another
    // instance. A body that said which part failed would help somebody iterate
    // towards a valid signature.
    const res = await deliver(
      'installation',
      { action: 'created', installation: { id: 901000, account: { login: 'endpoint-test-org' } } },
      { secret: 'not-the-secret' },
    )
    assert.equal(res.status, 401)
    assert.match(res.body, /could not be verified/)
  })

  test('a signed delivery is acted on', async () => {
    const res = await deliver('installation', {
      action: 'created',
      installation: { id: 901000, account: { login: 'endpoint-test-org', type: 'Organization' } },
      repositories: [{ id: 5, full_name: 'endpoint-test-org/app' }],
    })
    assert.equal(res.status, 200)
    const [org] = await api.admin<{ id: string }[]>`
      SELECT id FROM organizations WHERE github_login = 'endpoint-test-org'`
    assert.ok(org, 'a verified delivery wrote nothing')
    const [repo] = await api.admin<{ full_name: string }[]>`
      SELECT full_name FROM repositories WHERE org_id = ${org!.id}`
    assert.equal(repo!.full_name, 'endpoint-test-org/app')
  })

  test('the signature is checked before the body is parsed', async () => {
    // Order matters: parsing attacker-controlled JSON to decide whether to
    // trust it has it backwards. Unparseable AND unsigned answers 401, not 400.
    const res = await api.fetch('/webhooks/github', {
      method: 'POST',
      headers: { 'content-type': 'application/json', 'x-github-event': 'installation' },
      body: 'this is not json',
    })
    assert.equal(res.status, 401)
  })

  test('a signed body that is not JSON is a 400, not a 500', async () => {
    const body = 'still not json'
    const res = await api.fetch('/webhooks/github', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        'x-github-event': 'installation',
        'x-hub-signature-256': sign(body),
      },
      body,
    })
    assert.equal(res.status, 400)
  })

  test('an event this control plane ignores is 200, so GitHub does not retry it', async () => {
    const res = await deliver('star', { action: 'created' })
    assert.equal(res.status, 200)
    assert.match(res.body, /"handled":false/)
  })

  test('with no App configured, a delivery is 503 rather than accepted unsigned', async () => {
    // 503, not 401: nothing is wrong with the request, this installation has no
    // App, and that is worth seeing in GitHub's delivery log as a
    // misconfiguration rather than as a rejection.
    const bare = await startApi()
    try {
      const body = JSON.stringify({ action: 'created' })
      const res = await bare.fetch('/webhooks/github', {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          'x-github-event': 'installation',
          'x-hub-signature-256': sign(body),
        },
        body,
      })
      assert.equal(res.status, 503)
    } finally {
      await bare.close()
    }
  })

  test('a delivery with no identifier is refused rather than handled unfenced', async () => {
    // The header has been on every delivery since webhooks existed, so a
    // delivery without one is not one GitHub sent. Handling it would mean
    // handling something that cannot be recorded, and the whole point of the
    // ledger is that nothing runs twice.
    const body = JSON.stringify({ action: 'created' })
    const res = await api.fetch('/webhooks/github', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        'x-github-event': 'installation',
        'x-hub-signature-256': sign(body),
      },
      body,
    })
    assert.equal(res.status, 400)
  })

  test('ordering: the same delivery sent twice is handled once', async () => {
    // The HMAC says a delivery is genuine. It says nothing at all about it
    // being new, so a delivery captured off the wire verifies exactly as well
    // the second time.
    const payload = {
      action: 'created',
      installation: { id: 901777, account: { login: 'replay-test-org', type: 'Organization' } },
      repositories: [{ id: 71, full_name: 'replay-test-org/one' }],
    }
    const first = await deliver('installation', payload, { deliveryId: 'replay-fence-1' })
    assert.equal(first.status, 200)
    assert.doesNotMatch(first.body, /"replay":true/)

    // The second copy is answered without the handler running. Proven by the
    // effect rather than by the answer: a delivery that ran again would
    // rewrite updated_at on the installation row.
    const [before] = await api.admin<{ updated_at: Date }[]>`
      SELECT updated_at FROM github_installations WHERE installation_id = 901777`
    const second = await deliver('installation', payload, { deliveryId: 'replay-fence-1' })
    assert.equal(second.status, 200)
    assert.match(second.body, /"replay":true/)
    const [after] = await api.admin<{ updated_at: Date }[]>`
      SELECT updated_at FROM github_installations WHERE installation_id = 901777`
    assert.equal(
      new Date(after!.updated_at).getTime(),
      new Date(before!.updated_at).getTime(),
      'the replay ran the handler again',
    )

    const [ledger] = await api.admin<{ n: number }[]>`
      SELECT count(*)::int AS n FROM github_deliveries WHERE delivery_id = 'replay-fence-1'`
    assert.equal(ledger!.n, 1)
  })

  test('ordering: two copies of one delivery arriving at once run the handler once', async () => {
    // Concurrent rather than sequential, which is the case a primary key alone
    // does not settle: both attempts insert, one wins, and the loser has to be
    // told to come back rather than told it succeeded.
    const payload = {
      action: 'created',
      installation: { id: 901778, account: { login: 'concurrent-test-org', type: 'Organization' } },
    }
    const [a, b] = await Promise.all([
      deliver('installation', payload, { deliveryId: 'concurrent-fence-1' }),
      deliver('installation', payload, { deliveryId: 'concurrent-fence-1' }),
    ])
    const statuses = [a!.status, b!.status].sort()
    // 200 for the one that took the claim, and 503 with a Retry-After for the
    // one that did not. Never two 200s: answering success for work that is
    // still running is how a delivery is lost silently when that work fails.
    assert.deepEqual(statuses, [200, 503])

    const [ledger] = await api.admin<{ n: number }[]>`
      SELECT count(*)::int AS n FROM github_deliveries WHERE delivery_id = 'concurrent-fence-1'`
    assert.equal(ledger!.n, 1)
  })

  test('the endpoint is in the rate limit table', async () => {
    // The server refuses an endpoint with no declared limit outright, so this
    // passing at all proves the entry exists. Asserted anyway, because the
    // entry is what stops an unsigned flood from being unbounded work.
    const { ENDPOINT_LIMITS } = await import('../src/limits.ts')
    assert.ok(ENDPOINT_LIMITS['POST /webhooks/github'])
  })
})
