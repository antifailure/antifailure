// Somebody asking to buy, from the form to the row to the reader.
//
// The route this replaces is the reason the assertions are shaped the way they
// are. A waitlist wrote an address into a store nothing could read and mailed
// nobody, on a domain that authorizes no sender, and every one of its own tests
// passed: they proved the row landed. Nothing proved anybody would ever see it.
//
// So the chain here is asserted end to end, in the order a person experiences
// it: the browser can post at all, the row lands, the notification is sent when
// there is anywhere to send it, the RESPONSE says which of those happened, and
// an operator can read the row back on a credential the serving process does
// not have. Any one of those failing is the form doing nothing while looking
// like it worked.

import { describe, it, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { sql } from 'drizzle-orm'
import { validateLead, leadMessage, leadNotifierFrom } from '../src/enterprise/leads.ts'
import { listLeads, handleLead, LeadsRefused } from '../src/enterprise/leadstore.ts'
import { siteOriginFrom } from '../src/siteorigin.ts'
import { RecordingMailer } from '../src/auth/mail.ts'
import { available, startApi, adminUrl, type ApiHarness } from './harness.ts'

const hasDb = await available()
const SITE = 'https://site.test'

/** Each caller in its own rate limit bucket. POST /v1/leads is one a second
 *  with a burst of ten, keyed on the address, so without this the file shares
 *  one bucket and a later test reads a 429 as a validation failure. */
let caller = 0
function asNewCaller(extra: Record<string, string> = {}): Record<string, string> {
  caller += 1
  return {
    'content-type': 'application/json',
    'x-forwarded-for': `198.51.100.${caller % 250}`,
    ...extra,
  }
}

function aLead(overrides: Record<string, unknown> = {}) {
  return {
    name: 'Ada Lovelace',
    email: `ada-${randomUUID().slice(0, 8)}@example.test`,
    company: 'Analytical Engines',
    seats: 40,
    message: 'We need seats, single sign-on and a security review.',
    source: 'contact',
    ...overrides,
  }
}

describe('what a person typed, checked before it is written', () => {
  it('takes a complete lead and normalises the address', () => {
    const checked = validateLead({
      name: '  Ada  ',
      email: '  ADA@Example.Test ',
      company: ' Engines ',
      message: ' forty seats ',
      source: 'contact',
      seats: 40,
    })
    assert.ok('lead' in checked)
    assert.equal(checked.lead.email, 'ada@example.test')
    assert.equal(checked.lead.name, 'Ada')
    assert.equal(checked.lead.seats, 40)
  })

  it('names one missing field at a time, in the order they appear on the page', () => {
    // A list of four complaints about a five field form is a wall somebody
    // reads none of. The order matters as much as the count: the message has to
    // point at the first thing they would fix.
    const empty = { name: '', email: '', company: '', message: '', source: 'contact' }
    assert.match(assertError(validateLead(empty)), /your name/)
    assert.match(assertError(validateLead({ ...empty, name: 'Ada' })), /email address/)
    assert.match(
      assertError(validateLead({ ...empty, name: 'Ada', email: 'ada@example.test' })),
      /who you work for/,
    )
    assert.match(
      assertError(
        validateLead({ ...empty, name: 'Ada', email: 'ada@example.test', company: 'Engines' }),
      ),
      /what you need/,
    )
  })

  it('does not refuse a lead over the one field somebody cannot answer', () => {
    // Seats is the field a person evaluating a product genuinely does not know
    // yet. Absent, empty, zero and nonsense all mean the same thing and all
    // become null, so "unknown" has one representation rather than four.
    for (const seats of [undefined, null, 0, -3, Number.NaN]) {
      const checked = validateLead(aLead({ seats }))
      assert.ok('lead' in checked, `seats ${String(seats)} was refused`)
      assert.equal(checked.lead.seats, null)
    }
  })

  it('accepts the address shapes a strict expression would turn away', () => {
    // Turning away somebody who wants to buy costs far more than storing a row
    // that bounces, which is the same judgement invitations.ts makes.
    for (const email of [
      'a+tag@example.test',
      "o'brien@example.test",
      'first.last@sub.domain.example',
      'ADA@EXAMPLE.TEST',
    ]) {
      assert.ok('lead' in validateLead(aLead({ email })), `${email} was refused`)
    }
    for (const email of ['not-an-address', 'a@b', 'a@@b.test', 'two addresses@a.test']) {
      assert.ok('error' in validateLead(aLead({ email })), `${email} was accepted`)
    }
  })

  it('does not answer differently for an address it has seen before', () => {
    // There is deliberately no uniqueness rule. The same person asking twice
    // from two companies is two leads, and an endpoint that answered "we
    // already have you" would be an oracle for who has contacted us.
    const first = validateLead(aLead({ email: 'same@example.test' }))
    const second = validateLead(aLead({ email: 'same@example.test' }))
    assert.deepEqual(Object.keys(first), Object.keys(second))
  })
})

describe('the origin allowed to post one', () => {
  it('normalises an origin and refuses anything that is not one', () => {
    assert.equal(siteOriginFrom('https://Example.COM'), 'https://example.com')
    assert.equal(siteOriginFrom('https://example.com:443'), 'https://example.com')
    assert.equal(siteOriginFrom(undefined), undefined)
    assert.equal(siteOriginFrom('  '), undefined)
    // A path can never equal an Origin header, so a value carrying one would
    // allow nobody while looking configured. That is the failure worth a throw.
    assert.throws(() => siteOriginFrom('https://example.com/contact'), /origin and nothing more/)
    assert.throws(() => siteOriginFrom('example.com'), /absolute http or https/)
    assert.throws(() => siteOriginFrom('ftp://example.com'), /absolute http or https/)
    // There is no value meaning "any origin", and asserting that is what stops
    // somebody adding one as a convenience.
    assert.throws(() => siteOriginFrom('*'), /absolute http or https/)
  })
})

describe('where a lead is announced', () => {
  it('says which of the two absences it is, because they need different fixes', () => {
    const mailer = new RecordingMailer()
    const none = leadNotifierFrom({}, mailer)
    assert.equal(none.notifier, null)
    assert.match(none.summary, /AF_LEAD_NOTIFY_EMAIL is not set/)

    // The dangerous one: an address is configured and there is no way to reach
    // it. Without this line that deployment records leads and looks fine.
    const noMailer = leadNotifierFrom({ AF_LEAD_NOTIFY_EMAIL: 'sales@example.test' }, undefined)
    assert.equal(noMailer.notifier, null)
    assert.match(noMailer.summary, /CANNOT be told/)
    assert.match(noMailer.summary, /AF_RESEND_API_KEY/)

    const both = leadNotifierFrom({ AF_LEAD_NOTIFY_EMAIL: 'sales@example.test' }, mailer)
    assert.equal(both.notifier?.to, 'sales@example.test')
  })

  it('puts everything the person typed in the message, so a reply needs nothing else', () => {
    const checked = validateLead(aLead({ email: 'ada@example.test', seats: 40 }))
    assert.ok('lead' in checked)
    const message = leadMessage({ product: 'Antifailure', lead: checked.lead, id: 'lead-1' })
    for (const fragment of [
      'Ada Lovelace',
      'ada@example.test',
      'Analytical Engines',
      '40',
      'seats, single sign-on',
      'lead-1',
    ]) {
      assert.ok(message.text.includes(fragment), `the message drops ${fragment}`)
      assert.ok(message.html.includes(fragment), `the html drops ${fragment}`)
    }
    assert.match(message.subject, /Analytical Engines/)
  })
})

describe('posting one, over HTTP, from the origin the site is on', {
  skip: hasDb ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let h: ApiHarness
  let mailer: RecordingMailer
  const written: string[] = []

  before(async () => {
    mailer = new RecordingMailer()
    h = await startApi({
      siteOrigin: SITE,
      leadNotifier: { mailer, to: 'sales@example.test', productName: 'Antifailure' },
    })
  })
  after(async () => {
    for (const id of written) {
      await h.admin`DELETE FROM enterprise_leads WHERE id = ${id}::uuid`
    }
    await h.close()
  })

  async function post(body: unknown, headers: Record<string, string> = {}) {
    const res = await h.fetch('/v1/leads', {
      method: 'POST',
      headers: asNewCaller({ origin: SITE, ...headers }),
      body: JSON.stringify(body),
    })
    const text = await res.text()
    let parsed: Record<string, unknown> = {}
    try {
      parsed = JSON.parse(text) as Record<string, unknown>
    } catch {
      // Left empty; the assertion prints the text.
    }
    if (typeof parsed.id === 'string') written.push(parsed.id)
    return { res, body: parsed, text }
  }

  it('records the lead, mails somebody, and says it did both', async () => {
    const lead = aLead()
    const { res, body, text } = await post(lead)
    assert.equal(res.status, 201, text)
    assert.equal(body.ok, true)
    assert.equal(typeof body.id, 'string')
    // The half a form can silently skip. `notified` in the response is what the
    // page reads, and it is what makes a deployment with no mailer visible on
    // the screen rather than comfortable in a log.
    assert.equal(body.notified, true)

    const sent = mailer.lastTo('sales@example.test')
    assert.ok(sent, 'the lead was recorded and nobody was told')
    assert.ok(sent.text.includes(lead.email), 'the notification does not carry the address to reply to')

    const rows = await h.admin<{ email: string; company: string; seats: number }[]>`
      SELECT email, company, seats FROM enterprise_leads WHERE id = ${String(body.id)}::uuid`
    assert.equal(rows[0]!.email, lead.email)
    assert.equal(rows[0]!.company, lead.company)
    assert.equal(Number(rows[0]!.seats), 40)
  })

  it('lets the browser read the answer, and refuses any other page', async () => {
    // Without the header the browser reports a CORS failure and the page shows
    // "could not reach the server" for a request the server answered perfectly.
    const allowed = await post(aLead())
    assert.equal(allowed.res.headers.get('access-control-allow-origin'), SITE)
    assert.equal(allowed.res.headers.get('vary'), 'origin')
    // Never on the response. Credentials on a cross origin route that writes is
    // the combination that would make this forgeable on somebody's behalf, and
    // it is absent rather than false so no header can be misread.
    assert.equal(allowed.res.headers.get('access-control-allow-credentials'), null)

    const other = await post(aLead(), { origin: 'https://evil.test' })
    // The row is still written, because a POST from curl has no origin at all
    // and is not an attack. What is withheld is the header that would let
    // another page read the answer.
    assert.equal(other.res.headers.get('access-control-allow-origin'), null)
  })

  it('does not allow an origin that merely ends with the allowed one', async () => {
    // `endsWith` on a host is how https://evil-site.test gets allowed, and it
    // is the mistake that is invisible in review because the string looks right.
    const res = await h.fetch('/v1/leads', {
      method: 'OPTIONS',
      headers: asNewCaller({ origin: 'https://evil-site.test' }),
    })
    assert.equal(res.status, 403)
    assert.equal(res.headers.get('access-control-allow-origin'), null)
  })

  it('answers the preflight the browser sends before the post', async () => {
    const res = await h.fetch('/v1/leads', {
      method: 'OPTIONS',
      headers: asNewCaller({ origin: SITE }),
    })
    assert.equal(res.status, 204)
    assert.equal(res.headers.get('access-control-allow-origin'), SITE)
    assert.match(res.headers.get('access-control-allow-methods') ?? '', /POST/)
    // content-type is what makes the request non-simple in the first place. A
    // preflight that does not allow it is a preflight the browser refuses.
    assert.match(res.headers.get('access-control-allow-headers') ?? '', /content-type/)
  })

  it('turns a refusal into a sentence rather than a stack trace', async () => {
    const { res, body } = await post(aLead({ email: 'not-an-address' }))
    assert.equal(res.status, 400)
    assert.match(String(body.error), /does not look like an email address/)

    const notJson = await h.fetch('/v1/leads', {
      method: 'POST',
      headers: asNewCaller({ origin: SITE }),
      body: 'this is not json',
    })
    assert.equal(notJson.status, 400)
  })
})

describe('the deployment with nowhere to send it', {
  skip: hasDb ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let h: ApiHarness
  const written: string[] = []

  before(async () => {
    // No notifier, which is our own production today: antifailure.dev publishes
    // no mail exchanger and an SPF policy authorizing no sender, so there is
    // nowhere for a notification to go.
    h = await startApi({ siteOrigin: SITE })
  })
  after(async () => {
    for (const id of written) {
      await h.admin`DELETE FROM enterprise_leads WHERE id = ${id}::uuid`
    }
    await h.close()
  })

  it('still records the lead and says plainly that nobody was told', async () => {
    const res = await h.fetch('/v1/leads', {
      method: 'POST',
      headers: asNewCaller({ origin: SITE }),
      body: JSON.stringify(aLead()),
    })
    assert.equal(res.status, 201)
    const body = (await res.json()) as { id: string; notified: boolean }
    written.push(body.id)
    // The whole point. A form that answered a plain success here would be the
    // waitlist again: written down, nobody told, and nothing on the screen
    // saying so.
    assert.equal(body.notified, false)

    const rows = await h.admin<{ n: number }[]>`
      SELECT count(*)::int AS n FROM enterprise_leads WHERE id = ${body.id}::uuid`
    assert.equal(rows[0]!.n, 1, 'no mailer must not mean no record')
  })
})

describe('reading the leads back, on a credential the server does not have', {
  skip: hasDb ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let h: ApiHarness
  const written: string[] = []
  let operatorEmail: string

  before(async () => {
    h = await startApi({ siteOrigin: SITE })
    operatorEmail = `leads-operator-${randomUUID().slice(0, 8)}@example.test`
    await h.admin`
      INSERT INTO admin_users (email, name, role) VALUES (${operatorEmail}, 'Leads Operator', 'super_admin')`
  })
  after(async () => {
    for (const id of written) {
      await h.admin`DELETE FROM enterprise_leads WHERE id = ${id}::uuid`
    }
    await h.admin`DELETE FROM admin_users WHERE email = ${operatorEmail}`
    await h.close()
  })

  async function leave(overrides: Record<string, unknown> = {}): Promise<string> {
    const res = await h.fetch('/v1/leads', {
      method: 'POST',
      headers: asNewCaller({ origin: SITE }),
      body: JSON.stringify(aLead(overrides)),
    })
    // Read ONCE. A template literal in an assert message is evaluated whether
    // or not the assertion fails, so `await res.text()` there consumes the body
    // on the success path and the next line reports "Body is unusable" from a
    // test that passed. admin-signin-route.test.ts records the same trap.
    const text = await res.text()
    assert.equal(res.status, 201, text)
    const body = JSON.parse(text) as { id: string }
    written.push(body.id)
    return body.id
  }

  it('the serving credential cannot read the table at all', async () => {
    // The property migration 0031 is arranged around, asserted rather than
    // assumed. The route is anonymous, so a role that could also SELECT here is
    // one query bug away from publishing every prospect's contact details.
    await leave()
    await assert.rejects(
      () => h.pool.withoutTenant((db) => db.execute(sql`SELECT id FROM enterprise_leads LIMIT 1`)),
      // The database's own words, off `cause`, not the driver's. drizzle wraps a
      // failure as "Failed query: <sql>" and hangs the server's message off
      // cause, so matching the top level message asserts nothing about WHY the
      // statement failed and would pass on a typo in the table name.
      (err: unknown) => {
        const cause = (err as { cause?: unknown }).cause
        const said = cause instanceof Error ? cause.message : String(err)
        assert.match(
          said,
          /permission denied/i,
          `the application role can read enterprise_leads, which is the whole boundary. Postgres said: ${said}`,
        )
        return true
      },
    )
  })

  it('lists the unanswered ones oldest first, and marks one handled', async () => {
    const id = await leave({ company: `Queue ${randomUUID().slice(0, 6)}` })

    const before = await listLeads({ adminUrl })
    assert.ok(
      before.some((lead) => lead.id === id),
      'a lead posted through the route is not in the list an operator reads',
    )
    // Oldest first, because the person waiting longest is the one about to give
    // up and a newest-first queue leaves them at the bottom forever.
    for (let i = 1; i < before.length; i++) {
      assert.ok(
        before[i - 1]!.createdAt.getTime() <= before[i]!.createdAt.getTime(),
        'the queue is not oldest first',
      )
    }

    const handled = await handleLead({ adminUrl, id, as: operatorEmail, note: 'called them back' })
    assert.equal(handled.id, id)
    assert.ok(handled.handledAt)

    const after = await listLeads({ adminUrl })
    assert.equal(
      after.some((lead) => lead.id === id),
      false,
      'a handled lead is still in the unanswered queue',
    )
    assert.ok(
      (await listLeads({ adminUrl, includeHandled: true })).some((lead) => lead.id === id),
      '--all does not show a handled lead',
    )
  })

  it('refuses to handle one twice, one that does not exist, and one for nobody', async () => {
    const id = await leave()
    await handleLead({ adminUrl, id, as: operatorEmail })

    // Marking it again would overwrite who dealt with it and when, which is the
    // record somebody would come here to read.
    await assert.rejects(
      () => handleLead({ adminUrl, id, as: operatorEmail }),
      (err: unknown) => err instanceof LeadsRefused && /already marked handled/.test(err.message),
    )
    await assert.rejects(
      () => handleLead({ adminUrl, id: randomUUID(), as: operatorEmail }),
      (err: unknown) => err instanceof LeadsRefused && /No lead has the id/.test(err.message),
    )
    // A record naming an operator account that does not exist is a record of
    // nothing, so it is refused rather than written with a null.
    const unclaimed = await leave()
    await assert.rejects(
      () => handleLead({ adminUrl, id: unclaimed, as: 'nobody@example.test' }),
      (err: unknown) => err instanceof LeadsRefused && /No operator has the address/.test(err.message),
    )
  })
})

function assertError(result: { error: string } | { lead: unknown }): string {
  assert.ok('error' in result, 'expected a refusal and got a lead')
  return result.error
}
