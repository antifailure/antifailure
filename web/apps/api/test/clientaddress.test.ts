// Which entry of X-Forwarded-For the control plane believes, and what that
// entry keys.
//
// THE FAILURE. Every proxy appends the peer it saw to the END of the header
// and leaves whatever the caller sent in front, and Azure Container Apps
// ingress, the only proxy in production, documents exactly that: "Only the
// rightmost IP is provided by Azure Container Apps. Any other values must be
// validated by the user to prevent IP spoofing." clientaddress.ts took the
// FIRST entry. A caller who sent `X-Forwarded-For: 10.0.0.1` arrived as
// `10.0.0.1, <their real address>`, the auth limiter keyed on 10.0.0.1, a new
// value per request was a new bucket per request, and the sign-in audit trail
// recorded whatever they typed. One header walked around every limit on
// sign-in, the magic link, the device code and the OAuth callback.
//
// TWO HALVES, deliberately. The first half is the helper on its own, because
// the selection rule is a pure function and every shape it has to handle can
// be named in a table. The second half goes through server.ts, because the
// helper being right proves nothing about the limiter: the limiter keyed on
// clientIP, clientIP had its own rule, and a fix to clientAddress alone would
// have left the bypass in place with every helper test green. So the second
// half sends twenty one requests through /auth/github, each with a different
// forged first entry and the same real last one, and requires the twenty first
// to be refused. That is the sentence the evaluator could not verify against
// production without tripping the limiter, and it is the one that matters.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import {
  DEFAULT_TRUSTED_PROXY_HOPS,
  clientAddress,
  clientIP,
  describeTrustedProxyHops,
  trustedProxyHopsFrom,
} from '../src/clientaddress.ts'
import { hashPassword } from '../src/admin/session.ts'
import { available, startApi, type ApiHarness } from './harness.ts'

const hasDb = await available()

/** The auth limiter's burst FOR THESE TESTS, smaller than the one it ships
 *  with, and the reason is a mutation that survived. /auth/github sits behind
 *  two limiters keyed on the same address: the per-endpoint limit in
 *  limits.ts, burst 20 on the address alone, and authLimiter, burst 20 on the
 *  address and user agent. With the shipped numbers a test that sends twenty
 *  one requests sees a 429 whichever of the two is right, so restoring the
 *  old first-entry rule in clientKey alone left every assertion green. A
 *  burst of three makes authLimiter the one that refuses first, so the test
 *  is about the thing it names. The per-endpoint limiter has a test of its
 *  own below, on a route authLimiter does not guard. */
const AUTH_BURST = 3

/** The per-endpoint burst limits.ts gives GET /.well-known/oauth-protected-resource,
 *  a public document nothing else limits, keyed on the address alone. */
const DISCOVERY_BURST = 30

describe('which X-Forwarded-For entry is the client', () => {
  it('takes a single entry as the client', () => {
    assert.equal(clientAddress('198.51.100.4'), '198.51.100.4')
    assert.equal(clientIP('198.51.100.4'), '198.51.100.4')
  })

  it('takes the LAST entry when the first was forged by the caller', () => {
    // What the ingress delivers when a caller sends the header themselves:
    // their value, then the address the ingress saw.
    assert.equal(clientAddress('10.0.0.1, 198.51.100.4'), '198.51.100.4')
    assert.equal(clientIP('10.0.0.1, 198.51.100.4'), '198.51.100.4')
  })

  it('counts trusted hops from the end, so two proxies means the second entry from the right', () => {
    // A Front Door in front of the ingress: the caller's forgery, then what
    // the Front Door saw, then what the ingress saw, which is the Front Door.
    assert.equal(clientAddress('10.0.0.1, 198.51.100.4, 203.0.113.5', 2), '198.51.100.4')
    assert.equal(clientIP('10.0.0.1, 198.51.100.4, 203.0.113.5', 2), '198.51.100.4')
    // And with one hop the same header names the inner proxy's peer, which is
    // the right answer for a deployment that has only one proxy.
    assert.equal(clientAddress('10.0.0.1, 198.51.100.4, 203.0.113.5', 1), '203.0.113.5')
  })

  it('takes the first entry when there are fewer entries than trusted hops', () => {
    // Every entry present was written by a trusted proxy, so the leftmost is
    // the earliest trustworthy observation. Nothing to its left can be the
    // caller's: their entries would have been pushed further left still.
    assert.equal(clientAddress('198.51.100.4', 2), '198.51.100.4')
    assert.equal(clientIP('198.51.100.4', 3), '198.51.100.4')
  })

  it('strips brackets and ports from the chosen entry, not from the forged one', () => {
    assert.equal(clientAddress('10.0.0.1, [2001:db8::1]:443'), '2001:db8::1')
    assert.equal(clientAddress('10.0.0.1, 203.0.113.9:44321'), '203.0.113.9')
    assert.equal(clientAddress('[2001:db8::1]:443, 203.0.113.9:44321', 2), '2001:db8::1')
    // A bare IPv6 literal is full of colons and must not lose its tail to the
    // port stripping.
    assert.equal(clientAddress('10.0.0.1, 2001:db8::1'), '2001:db8::1')
  })

  it('ignores a garbage first entry when the last one is real', () => {
    assert.equal(clientAddress('<script>alert(1)</script>, 198.51.100.4'), '198.51.100.4')
    assert.equal(clientIP('unknown, 198.51.100.4'), '198.51.100.4')
    assert.equal(clientIP('999.999.999.999, 198.51.100.4'), '198.51.100.4')
  })

  it('puts a header that is only garbage in the shared unknown bucket, never in its own', () => {
    // The limiter half of the old defect: clientIP was "deliberately NOT
    // validated", so a caller who could reach the process without a proxy in
    // front of it got a fresh bucket for every string they typed. Now an entry
    // that is not an address is nobody in particular, and nobody in particular
    // shares one bucket.
    assert.equal(clientAddress('not-an-address'), undefined)
    assert.equal(clientIP('not-an-address'), 'unknown')
    assert.equal(clientIP('garbage-1'), clientIP('garbage-2'))
    assert.equal(clientAddress('999.0.0.1'), undefined)
    assert.equal(clientIP('999.0.0.1'), 'unknown')
    // The literal RFC 7239 placeholder is not an address either.
    assert.equal(clientAddress('unknown'), undefined)
  })

  it('treats an absent or empty header as no address and the shared bucket', () => {
    assert.equal(clientAddress(undefined), undefined)
    assert.equal(clientIP(undefined), 'unknown')
    assert.equal(clientAddress(''), undefined)
    assert.equal(clientIP(''), 'unknown')
    // Stray separators are not entries.
    assert.equal(clientAddress(' , ,'), undefined)
    assert.equal(clientAddress(', 198.51.100.4,'), '198.51.100.4')
  })

  it('defaults to one trusted hop, which is the ingress alone', () => {
    assert.equal(DEFAULT_TRUSTED_PROXY_HOPS, 1)
    assert.equal(clientAddress('10.0.0.1, 198.51.100.4'), clientAddress('10.0.0.1, 198.51.100.4', 1))
  })
})

describe('AF_TRUSTED_PROXY_HOPS', () => {
  it('is one when unset or blank', () => {
    assert.equal(trustedProxyHopsFrom(undefined), 1)
    assert.equal(trustedProxyHopsFrom(null), 1)
    assert.equal(trustedProxyHopsFrom(''), 1)
    assert.equal(trustedProxyHopsFrom('  '), 1)
  })

  it('reads a small positive integer', () => {
    assert.equal(trustedProxyHopsFrom('1'), 1)
    assert.equal(trustedProxyHopsFrom('2'), 2)
    assert.equal(trustedProxyHopsFrom(' 3 '), 3)
    assert.equal(trustedProxyHopsFrom('16'), 16)
  })

  it('stops the process on anything else, naming the variable', () => {
    // Zero would select nothing, so every caller would share one bucket and the
    // whole product would answer 429. That has to be refused at start-up, not
    // discovered from the outside.
    for (const bad of ['0', '-1', '17', '100', '1.5', 'one', '1e1', '0x2', '2,']) {
      assert.throws(
        () => trustedProxyHopsFrom(bad),
        /AF_TRUSTED_PROXY_HOPS must be a whole number from 1 to 16/,
        `${JSON.stringify(bad)} was accepted`,
      )
    }
  })

  it('says at start-up which entry is being read', () => {
    assert.match(describeTrustedProxyHops(1), /last X-Forwarded-For entry/)
    assert.match(describeTrustedProxyHops(1), /AF_TRUSTED_PROXY_HOPS is not set/)
    assert.match(describeTrustedProxyHops(2), /2 from the end/)
    assert.match(describeTrustedProxyHops(2), /AF_TRUSTED_PROXY_HOPS=2/)
  })
})

/** Sends `count` requests to `path`, each with the header `header(i)` gives
 *  it, or none when it gives null, and returns the status of each. */
async function knock(
  h: ApiHarness,
  path: string,
  count: number,
  header: (i: number) => string | null,
) {
  const statuses: number[] = []
  for (let i = 0; i < count; i += 1) {
    const value = header(i)
    const res = await h.fetch(path, {
      redirect: 'manual',
      ...(value === null ? {} : { headers: { 'x-forwarded-for': value } }),
    })
    statuses.push(res.status)
  }
  return statuses
}

/** A forged first entry that differs on every request: the bypass as an
 *  attacker would run it. */
const forged = (i: number) => `10.0.${Math.floor(i / 250)}.${i % 250}`

describe(
  'the auth rate limiter, through server.ts',
  { skip: hasDb ? false : 'no Postgres at AF_TEST_DATABASE_URL' },
  () => {
    let h: ApiHarness

    before(async () => {
      h = await startApi({ authLimit: { rate: 1, burst: AUTH_BURST } })
    })

    after(async () => {
      await h?.close()
    })

    it('limits a caller who forges a new first entry on every request', async () => {
      // A fresh value in front, the same real address behind, which is what
      // the ingress appends. Before the fix every one of these landed in a
      // bucket of its own and the last was as welcome as the first.
      const real = '198.51.100.4'
      const statuses = await knock(h, '/auth/github', AUTH_BURST + 1, (i) => `${forged(i)}, ${real}`)
      assert.deepEqual(
        statuses.slice(0, AUTH_BURST),
        new Array(AUTH_BURST).fill(302),
        'the burst itself was refused, so the test is not measuring the limiter',
      )
      assert.equal(statuses[AUTH_BURST], 429, 'one more forged request was let through')

      // And it was THAT caller who was limited, not everybody: a different
      // real address behind the same forgery is a different bucket.
      const [other] = await knock(h, '/auth/github', 1, () => `10.0.0.1, 198.51.100.250`)
      assert.equal(other, 302, 'a different client was limited along with the forger')
    })

    it('limits a caller whose header is nothing but garbage, in one shared bucket with no header at all', async () => {
      // The other half: an unparseable entry must never be a bucket of its
      // own. It is "unknown", and unknown is one bucket. A direct request with
      // no header is the same nobody and lands in the same bucket, which is
      // what proves the sharing rather than merely the refusal.
      const statuses = await knock(h, '/auth/github', AUTH_BURST + 1, (i) => `garbage-${randomUUID()}-${i}`)
      assert.equal(statuses[AUTH_BURST], 429, 'garbage bought a fresh bucket per request')
      const [direct] = await knock(h, '/auth/github', 1, () => null)
      assert.equal(direct, 429, 'a request with no header did not share the unknown bucket')
    })

    it('limits the per-endpoint bucket on the last entry too', async () => {
      // The second limiter, on a route the auth limiter does not guard, so
      // that a 429 here can only be limits.ts keying on clientIP. Thirty
      // forged entries in front of one real address are one caller.
      const statuses = await knock(
        h,
        '/.well-known/oauth-protected-resource',
        DISCOVERY_BURST + 1,
        (i) => `${forged(i)}, 198.51.100.6`,
      )
      assert.deepEqual(
        statuses.slice(0, DISCOVERY_BURST),
        new Array(DISCOVERY_BURST).fill(200),
        'the burst itself was refused, so the test is not measuring the limiter',
      )
      assert.equal(statuses[DISCOVERY_BURST], 429, 'the per-endpoint limit keyed on the forged entry')
      const [other] = await knock(h, '/.well-known/oauth-protected-resource', 1, () => `10.0.0.1, 198.51.100.7`)
      assert.equal(other, 200, 'a different client was limited along with the forger')
    })
  },
)

describe(
  'the auth rate limiter behind two trusted proxies',
  { skip: hasDb ? false : 'no Postgres at AF_TEST_DATABASE_URL' },
  () => {
    let h: ApiHarness

    before(async () => {
      h = await startApi({ trustedProxyHops: 2, authLimit: { rate: 1, burst: AUTH_BURST } })
    })

    after(async () => {
      await h?.close()
    })

    it('keys on the entry two from the end, so the inner proxy is not the client', async () => {
      // A Front Door at 203.0.113.5 in front of the ingress. With one hop
      // every request would key on the Front Door and the whole internet
      // would share one bucket; with two, the client behind it is the key.
      const statuses = await knock(h, '/auth/github', AUTH_BURST + 1, (i) => `${forged(i)}, 198.51.100.4, 203.0.113.5`)
      assert.equal(statuses[AUTH_BURST], 429, 'the forged first entry was still the key')
      const [other] = await knock(h, '/auth/github', 1, () => `10.0.0.1, 198.51.100.5, 203.0.113.5`)
      assert.equal(other, 302, 'a different client behind the same two proxies was limited with the first')
    })

    it('keys the per-endpoint bucket on the same entry', async () => {
      const statuses = await knock(
        h,
        '/.well-known/oauth-protected-resource',
        DISCOVERY_BURST + 1,
        (i) => `${forged(i)}, 198.51.100.8, 203.0.113.5`,
      )
      assert.equal(statuses[DISCOVERY_BURST], 429, 'the per-endpoint limit ignored the hop count')
      const [other] = await knock(h, '/.well-known/oauth-protected-resource', 1, () => `10.0.0.1, 198.51.100.9, 203.0.113.5`)
      assert.equal(other, 200, 'a different client behind the same two proxies was limited with the first')
    })
  },
)

describe(
  'the sign-in audit trail',
  { skip: hasDb ? false : 'no Postgres at AF_TEST_DATABASE_URL' },
  () => {
    let h: ApiHarness
    const password = 'a-provisioned-password-nobody-shipped'

    before(async () => {
      h = await startApi()
      // The harness clock starts wherever FakeClock starts; a session issued
      // against it would be expired against the wall clock the route compares.
      h.clock.advance(Date.now() - h.clock.now().getTime())
    })

    after(async () => {
      await h?.close()
    })

    it('records the address the proxy saw on an operator sign-in, not the one the caller wrote', async () => {
      // Two defects in one route. The operator sign-in passed the header to
      // the database RAW, so the list a proxy chain produces would have
      // failed the `inet` cast and answered 500, and a single forged entry
      // would have been recorded as the operator's address. Now the trusted
      // entry is what the row holds.
      const email = `operator-${randomUUID().slice(0, 8)}@example.test`
      const creds = await hashPassword(password)
      await h.admin`
        INSERT INTO admin_users (email, name, role, password_hash, password_salt, password_set_at)
        VALUES (${email}, 'Audit Operator', 'super_admin', ${creds.hash}, ${creds.salt}, ${new Date().toISOString()})`

      const res = await h.fetch('/v1/admin/signin', {
        method: 'POST',
        headers: { 'content-type': 'application/json', 'x-forwarded-for': '10.0.0.1, 198.51.100.4' },
        body: JSON.stringify({ email, password }),
      })
      assert.equal(res.status, 200, `sign-in behind a proxy chain answered ${res.status}`)

      const [row] = await h.admin<{ ip: string | null }[]>`
        SELECT host(s.ip) AS ip FROM admin_sessions s
        JOIN admin_users u ON u.id = s.admin_user_id
        WHERE u.email = ${email}`
      assert.equal(row?.ip ?? null, '198.51.100.4', `the session recorded ${row?.ip ?? 'nothing'}`)
    })
  },
)
