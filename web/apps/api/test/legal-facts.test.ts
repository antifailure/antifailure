// The published legal pages, against the thing that decides them.
//
// THE CLASS THIS EXISTS FOR, which is the finding rather than any one instance.
//
// Seven published claims were found false in one night: backup retention saying
// fourteen days while production runs thirty-five, log retention documented
// nowhere, the subprocessor page saying there was no billing and that nothing
// could send mail while the repository held a real Stripe client and a real
// mailer, provider-key removal called deletion when it is revocation, a privacy
// sheet saying a waitlist address never leaves the browser after it started
// being posted to a server.
//
// Every one was TRUE WHEN IT WAS WRITTEN. That is the whole point. They are not
// carelessness, they are drift, and prose has no compiler. A legal page that has
// drifted is worse than a documentation page that has drifted, because somebody
// relies on it in a way they cannot check.
//
// So this holds the mechanical half to reality, the same way config-docs.test.ts
// holds the control plane's environment variables to the source that reads them.
//
// WHAT IT CANNOT SEE, written next to the assertions rather than in a report.
//
// It holds NUMBERS and the existence of NAMED CAPABILITIES. It cannot hold a
// sentence. "We do not use Stripe" and "Stripe cannot be used" differ by a
// promise, and nothing here can tell them apart; the rule for that is prose, at
// the top of www/lib/subprocessors.ts, and it stays a judgement. It also cannot
// see a claim nobody thought to encode: a page can still say something false
// about a subject this file does not know about. What it does do is make the
// half that IS checkable fail loudly at the moment the code moves, which is the
// moment all seven of these went wrong.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

/**
 * The facts are READ AS TEXT rather than imported, and that is not laziness.
 *
 * www is a separate npm project with its own module resolution, and importing
 * across the boundary made this file fail to compile under the API's
 * verbatimModuleSyntax while the tests themselves still ran, which is the worst
 * of both: a gate that works and a build that does not. config-docs.test.ts
 * reads the documentation it checks the same way, for the same reason.
 *
 * The cost is that a parse which stops matching reads as an empty set and every
 * assertion over it passes. The first test below is the negative control on
 * exactly that.
 */
interface RetentionFact {
  days: number
  words: string
}

const here = path.dirname(fileURLToPath(import.meta.url))
const repoRoot = path.join(here, '..', '..', '..', '..')

const read = (p: string) => readFile(path.join(repoRoot, p), 'utf8')

/** An http or https address that is not the loopback a local control plane
 *  serves on. Named once so the gate and its negative control cannot drift. */
const NAMES_A_HOST = /https?:\/\/(?!127\.0\.0\.1)/

/**
 * The same source with its comments taken out.
 *
 * WHY A GATE OVER SOURCE HAS TO DO THIS. The address check below is looking for
 * a host this code CONTACTS. A comment is prose, and prose about crawlers has
 * to be able to quote the address a crawler announces itself with without the
 * gate reading it as a new network destination. That is not hypothetical: this
 * branch already moved one comment out of a SQL SET clause for exactly this
 * reason, and then tripped the same gate a second time with a comment in
 * bots.ts explaining why an address was REMOVED from the matcher.
 *
 * A gate that cannot be explained next to is a gate people route around, and
 * the way they route around it is by deleting the explanation.
 *
 * Block comments go first, then a line comment, and the line comment is only
 * cut where the `//` is outside a quote, so that a string holding a URL is
 * still the code it is. The negative control below drives that case.
 */
function withoutComments(source: string): string {
  const noBlocks = source.replace(/\/\*[\s\S]*?\*\//g, ' ')
  return noBlocks
    .split('\n')
    .map((line) => {
      let quote: string | null = null
      for (let i = 0; i < line.length; i += 1) {
        const c = line[i]!
        if (c === '\\') {
          i += 1
          continue
        }
        if (quote) {
          if (c === quote) quote = null
          continue
        }
        if (c === '"' || c === "'" || c === '`') {
          quote = c
          continue
        }
        if (c === '/' && line[i + 1] === '/') return line.slice(0, i)
      }
      return line
    })
    .join('\n')
}

const facts = await read('www/lib/legal-facts.ts')

/** One `{ days: N, words: "x" }` out of the facts file, by the constant and the
 *  environment that hold it. */
function published(constant: string, environment: string): RetentionFact | null {
  const block = facts.match(new RegExp(`export const ${constant} = \\{[\\s\\S]*?\\n\\};`, 'm'))
  if (!block) return null
  const found = block[0].match(
    new RegExp(`${environment}:\\s*\\{\\s*days:\\s*(\\d+),\\s*words:\\s*"([a-z-]+)"`, 'm'),
  )
  return found ? { days: Number(found[1]), words: found[2]! } : null
}

const BACKUP_RECOVERY = {
  production: published('BACKUP_RECOVERY', 'production'),
  staging: published('BACKUP_RECOVERY', 'staging'),
}
const LOG_RETENTION = {
  production: published('LOG_RETENTION', 'production'),
  staging: published('LOG_RETENTION', 'staging'),
}

/** Every conditional processor, as vendor, module and the variables that switch
 *  it on. */
function conditionalProcessors(): { vendor: string; module: string; variables: string[] }[] {
  // Split on the vendor and read each entry's own slice, rather than one
  // pattern spanning all three fields. The first version required them
  // adjacent, and the moment an entry gained an explanatory comment between
  // vendor and module it silently matched one processor instead of two, which
  // is a parser reporting on its own shape rather than on the file.
  const out: { vendor: string; module: string; variables: string[] }[] = []
  const starts = [...facts.matchAll(/vendor:\s*"([^"]+)"/g)]
  for (let i = 0; i < starts.length; i += 1) {
    const from = starts[i]!.index!
    const to = i + 1 < starts.length ? starts[i + 1]!.index! : facts.length
    const slice = facts.slice(from, to)
    const module = slice.match(/module:\s*"([^"]+)"/)
    const variables = slice.match(/variables:\s*\[([\s\S]*?)\]/)
    if (!module || !variables) continue
    out.push({
      vendor: starts[i]![1]!,
      module: module[1]!,
      variables: [...variables[1]!.matchAll(/"([^"]+)"/g)].map((m) => m[1]!),
    })
  }
  return out
}

/** A Terraform assignment, from the file that actually sets it. */
function tfvar(source: string, name: string): number | null {
  const found = source.match(new RegExp(`^\\s*${name}\\s*=\\s*(\\d+)\\s*$`, 'm'))
  return found ? Number(found[1]) : null
}

/** A module default, for the values an environment leaves unset. */
function tfDefault(source: string, name: string): number | null {
  const block = source.match(new RegExp(`variable\\s+"${name}"\\s*\\{[\\s\\S]*?\\n\\}`, 'm'))
  if (!block) return null
  const found = block[0].match(/^\s*default\s*=\s*(\d+)\s*$/m)
  return found ? Number(found[1]) : null
}

const NUMBER_WORDS: Record<number, string> = {
  14: 'fourteen', 30: 'thirty', 35: 'thirty-five', 90: 'ninety',
}

describe('the published retention numbers are the ones the infrastructure sets', () => {
  it('reads the Terraform it is comparing against, so an empty parse cannot pass', async () => {
    // Every assertion below is vacuously true against a file that did not load
    // or a regular expression that stopped matching, and a null read from a
    // pattern is exactly what a broken instrument prints.
    const production = await read('infra/terraform/stacks/control-plane/production.tfvars')
    const variables = await read('infra/terraform/stacks/control-plane/variables.tf')
    assert.ok(production.length > 100, 'production.tfvars did not load')
    assert.ok(
      tfvar(production, 'backup_retention_days') !== null,
      'the tfvars parser found no backup_retention_days, so it is measuring itself',
    )
    assert.ok(
      tfDefault(variables, 'log_retention_days') !== null,
      'the variable-default parser found no log_retention_days',
    )
    assert.ok(
      BACKUP_RECOVERY.production !== null && LOG_RETENTION.staging !== null,
      'the facts parser read nothing out of www/lib/legal-facts.ts, so every assertion below ' +
        'is vacuously true and this gate is checking nothing',
    )
    assert.ok(
      conditionalProcessors().length >= 2,
      `the facts parser found ${conditionalProcessors().length} conditional processors`,
    )
  })

  it('publishes production backup recovery as the tfvars set it', async () => {
    const production = await read('infra/terraform/stacks/control-plane/production.tfvars')
    assert.equal(
      BACKUP_RECOVERY.production?.days,
      tfvar(production, 'backup_retention_days'),
      'the legal pages publish a production recovery window that production does not run. ' +
        'This is the exact drift that had three pages saying fourteen days while production ' +
        'ran thirty-five.',
    )
  })

  it('publishes staging backup recovery as the stack default, which staging leaves unset', async () => {
    const variables = await read('infra/terraform/stacks/control-plane/variables.tf')
    const staging = await read('infra/terraform/stacks/control-plane/staging.tfvars')
    assert.equal(
      tfvar(staging, 'backup_retention_days'),
      null,
      'staging.tfvars now sets backup_retention_days, so the published number can no longer ' +
        'come from the stack default and this test is reading the wrong source',
    )
    assert.equal(BACKUP_RECOVERY.staging?.days, tfDefault(variables, 'backup_retention_days'))
  })

  it('publishes log retention for both environments, which was documented nowhere', async () => {
    const production = await read('infra/terraform/stacks/control-plane/production.tfvars')
    const variables = await read('infra/terraform/stacks/control-plane/variables.tf')
    assert.equal(LOG_RETENTION.production?.days, tfvar(production, 'log_retention_days'))
    assert.equal(LOG_RETENTION.staging?.days, tfDefault(variables, 'log_retention_days'))
  })

  it('spells each number the way the prose reads it', () => {
    // The pages are written in words, so the number and the word are two
    // representations of one fact and either can drift from the other. A page
    // saying "fourteen days" beside a config saying 35 is the failure; a page
    // saying "thirty-five" beside a fact object saying 14 is the same failure
    // one level in.
    const published: [string, RetentionFact | null][] = [
      ['production backup', BACKUP_RECOVERY.production],
      ['staging backup', BACKUP_RECOVERY.staging],
      ['production logs', LOG_RETENTION.production],
      ['staging logs', LOG_RETENTION.staging],
    ]
    for (const [label, fact] of published) {
      assert.ok(fact, `${label} is not published at all`)
      assert.equal(
        fact.words,
        NUMBER_WORDS[fact.days],
        `${label} is published as ${fact.days} days and spelled "${fact.words}"`,
      )
    }
  })

  it('renders the fact rather than a copy of it, so the prose cannot drift on its own', async () => {
    // Stronger than checking the page contains the right word, which is what
    // this asserted first and which broke the moment the prose started
    // interpolating: a literal in the prose is a second copy of the number and
    // a second copy is the thing that drifted. The page has to READ the fact.
    const legal = await read('www/components/pages/company/Legal.tsx')
    for (const constant of ['BACKUP_RECOVERY', 'LOG_RETENTION']) {
      assert.ok(
        legal.includes(`${constant}.production`) && legal.includes(`${constant}.staging`),
        `the legal pages do not render ${constant} for both environments, so a number there is ` +
          `a hand-maintained copy and nothing holds it to the infrastructure`,
      )
    }

    // And NO hand-written copy of a published number anywhere in the file.
    //
    // The first version of this checked two specific stale sentences, which is
    // a list rather than a property, and it passed over a third: the service
    // levels page still spelled both numbers out. It was CORRECT, which is
    // exactly how the other three started, and it is the shape that drifts.
    // Found by a colleague asking whether the fix covered a line I had not
    // looked at, not by the gate.
    //
    // `thirty` is deliberately absent from this list. It is also the blob
    // soft-delete window on the masked dumps row, which is a different fact
    // from a different source, and forbidding the word would refuse a sentence
    // this module has no opinion about.
    for (const word of ['thirty-five', 'fourteen', 'ninety']) {
      assert.ok(
        !new RegExp(`\\b${word}\\b`, 'i').test(legal),
        `the legal pages spell "${word}" out by hand somewhere. Every published retention ` +
          `number comes from legal-facts.ts, and a second copy is the thing that drifted.`,
      )
    }
  })
})

describe('the deletion wording matches what the schema actually does', () => {
  /**
   * The strongest claim a deletion page can make is that a row CANNOT be
   * removed, and that claim is only true if a constraint enforces it.
   *
   * This exists because a migration comment asserted exactly that:
   * `audit_entries.actor_user_id` references `users` with NO ACTION, so the
   * database refuses to delete a person who has ever acted. It does not.
   * `0001_init.sql` declares ON DELETE SET NULL, nothing since alters it, and a
   * live database reads `confdeltype = 'n'`. The comment was written about a
   * guarantee nobody had built, and it was one review away from becoming
   * published legal text.
   *
   * So the gate is a conditional rather than an assertion of today's state: the
   * strong wording is permitted only alongside the strong constraint. It passes
   * now, when the pages make the weaker claim and the constraint is weak. It
   * passes later, when a migration adds the constraint and the pages are
   * updated. It fails on the combination that is a lie.
   */
  const IRREMOVABLE = [
    /cannot be removed/i,
    /cannot be deleted/i,
    /refuses to delete a person/i,
  ]
  // `/the database refuses/` was in this list and is not, because a substring
  // cannot tell a claim from its negation. The accurate sentence on the page
  // reads "not because the database refuses", and the pattern matched it, so
  // the gate refused the true wording and would have pushed whoever hit it
  // toward the false one. That is worse than not gating the phrase at all.
  //
  // A lookbehind would paper over this one sentence and fail on the next
  // phrasing. The three patterns left are ones whose negations nobody writes:
  // there is no natural sentence containing "cannot be removed" that means the
  // row can be. This is the boundary the header calls out, met in practice.
  // NOT in that list, deliberately: wording that says the row is KEPT, or
  // retained, or not removed by choice. That is the weak claim and it is the
  // true one. An earlier version matched "the row is retained so the audit",
  // which would have refused the accurate sentence and pushed whoever hit it
  // toward the inaccurate one, which is the opposite of the point.

  /** What the migrations say happens to an audit entry when its actor goes. */
  async function onDeleteForActor(): Promise<string | null> {
    const dir = path.join(repoRoot, 'web/packages/db/migrations')
    const { readdir } = await import('node:fs/promises')
    const files = (await readdir(dir)).filter((f) => f.endsWith('.sql')).sort()
    let answer: string | null = null
    for (const file of files) {
      const sql = await readFile(path.join(dir, file), 'utf8')
      // The column declaration, and any later constraint that replaces it. Last
      // one wins, which is the order the migrations apply in.
      for (const m of sql.matchAll(
        /actor_user_id[\s\S]{0,120}?REFERENCES\s+users\s*\(\s*id\s*\)\s*(?:ON DELETE (SET NULL|NO ACTION|RESTRICT|CASCADE))?/gi,
      )) {
        // A missing ON DELETE clause means NO ACTION in SQL, and defaulting to
        // it silently is the dangerous direction: NO ACTION is the STRONG
        // constraint this gate permits the strong wording against. An earlier
        // version had an off-by-one in the capture group, read SET NULL as
        // undefined, fell back to NO ACTION, and would have certified exactly
        // the false claim it exists to catch. So absence is reported as
        // absence and the caller decides.
        answer = (m[1] ?? 'ABSENT').toUpperCase()
      }
    }
    return answer
  }

  it('reads the constraint it is reasoning about, so an empty parse cannot pass', async () => {
    const found = await onDeleteForActor()
    assert.ok(
      found !== null,
      'no REFERENCES users(id) was found for audit_entries.actor_user_id, so this gate is ' +
        'reasoning about nothing. Either the column was renamed or the pattern stopped matching.',
    )
  })

  it('permits the strong deletion wording only where a constraint enforces it', async () => {
    const pages =
      (await read('www/components/pages/company/Legal.tsx')) +
      (await read('www/components/ContentSheet.tsx'))
    const claimed = IRREMOVABLE.filter((p) => p.test(pages))
    if (claimed.length === 0) return

    const onDelete = await onDeleteForActor()
    assert.ok(
      // ABSENT is deliberately NOT accepted here even though SQL reads a
      // missing clause as NO ACTION. An unstated constraint is one nobody wrote
      // down on purpose, and this gate is about a promise somebody published.
      onDelete === 'NO ACTION' || onDelete === 'RESTRICT',
      `a legal page states that a row cannot be removed, and audit_entries.actor_user_id is ` +
        `ON DELETE ${onDelete}, so the database would remove it and null the reference. Either ` +
        `add the constraint or say the weaker thing, which is that the personal data is erased ` +
        `and the row is kept.`,
    )
  })

  it('records what the constraint is today, so a change to it is noticed here', async () => {
    // Not a claim that SET NULL is right. It is where the fact is written down,
    // so that a migration changing it turns this red and whoever changes it is
    // sent to the page that describes deletion.
    assert.equal(
      await onDeleteForActor(),
      'SET NULL',
      'the actor reference on audit_entries changed. The deletion section of the retention ' +
        'page describes what happens to a person who asks to be removed, and it was written ' +
        'against SET NULL.',
    )
  })
})

describe('the subprocessor page describes the code that exists', () => {
  it('names a module and variables that are really there, for every conditional processor', async () => {
    // The claim being held is the weak one and the only one checkable: the code
    // CONTAINS this integration and it is reached through these variables. That
    // an integration was REMOVED, or its variables renamed, would leave the page
    // describing a vendor nothing can reach, which is a different falsehood in
    // the same family.
    for (const processor of conditionalProcessors()) {
      const source = await read(processor.module).catch(() => '')
      assert.ok(
        source.length > 0,
        `${processor.vendor} is published as conditionally engaged through ${processor.module}, ` +
          `which does not exist`,
      )
      for (const variable of processor.variables) {
        assert.ok(
          source.includes(variable),
          `${processor.vendor} is published as switched on by ${variable}, and ${processor.module} ` +
            `does not read it`,
        )
      }
    }
  })

  it('does not claim a vendor is unreachable while its client is in the tree', async () => {
    // The two sentences that were false, as a guard. Both were true when
    // written. Both became false the day a branch landed, and nothing said so.
    // Both files, because the same false claim was on the privacy page as well
    // as the subprocessor page and fixing one would have left the other.
    const page =
      (await read('www/lib/subprocessors.ts')) +
      (await read('www/components/pages/company/Legal.tsx'))
    const forbidden: [RegExp, string][] = [
      [
        // No trailing period. The privacy page said "There is no billing, so
        // there are no payment records" and the pattern required a full stop,
        // so the same false claim in different punctuation walked straight
        // through this gate. Found by building the site and reading the output,
        // not by the gate, which is the whole argument for doing both.
        /There is no billing/,
        'billing/plans.ts builds a live Stripe client from AF_STRIPE_SECRET_KEY',
      ],
      [
        /Nothing in the product can send email or a message\./,
        'auth/mail.ts posts to api.resend.com and main.ts wires it',
      ],
      [
        /only Stripe code in the repository is an offline simulator/,
        'RealStripeClient ships in billing/stripe.ts',
      ],
    ]
    for (const [pattern, why] of forbidden) {
      assert.ok(
        !pattern.test(page),
        `the subprocessor page publishes a claim that is false about the code: ${why}`,
      )
    }
  })

  it('keeps the analytics claim and the analytics code in step, in both directions', async () => {
    // CONDITIONAL ON THE FILE, NOT ON A FLAG, and that idea is integrator7's
    // rather than mine. I had deleted this assertion on the branch where the
    // beacon does not exist, which means somebody has to remember to put it
    // back the day it does. Keying it on whether the file is there makes it
    // start asserting on its own, which is the exact failure mode this whole
    // file exists to prevent.
    //
    // The refinement is that BOTH states are asserted rather than one being a
    // silent skip. A skip reads as a pass, and the two halves of this pair can
    // drift apart in either direction: a beacon added while the page still says
    // the site loads no analytics, or the page rewritten to describe a beacon
    // that is not there. One of those was a real near miss: the branch that
    // adds the beacon also rewrites this claim, and landing the rewrite without
    // the beacon would have published a site that counts page views when it
    // does not.
    const page = await read('www/lib/subprocessors.ts')
    // EVERY file the beacon is made of, not just the one it started in. The
    // queue, the session rules and the endpoint moved out of analytics.ts into
    // beacon.ts so that a test runner could load them, and this gate went on
    // reading analytics.ts, which by then held a React hook and no address at
    // all. A gate pointed at the wrong file passes for the same reason an empty
    // one does. Missing files are allowed here because the pair below asserts
    // both states; a file that is present has to hold up.
    const parts = await Promise.all(
      ['www/lib/analytics.ts', 'www/lib/beacon.ts', 'www/lib/bots.ts'].map((f) =>
        read(f).catch(() => null),
      ),
    )
    const beacon = parts.some((p) => p !== null) ? parts.filter((p) => p !== null).join('\n') : null

    if (beacon === null) {
      assert.match(
        page,
        /This site loads no analytics and no third-party script/,
        'there is no site beacon in this tree, so the subprocessor page must still say the site ' +
          'loads no analytics. It says something else, which means the claim was rewritten ' +
          'for a beacon that is not in this tree.',
      )
      return
    }

    assert.match(
      page,
      /no script from another origin/,
      'a beacon exists and the subprocessor page still claims the site loads no analytics',
    )
    assert.ok(
      !NAMES_A_HOST.test(withoutComments(beacon).replace(/CONTROL_PLANE_URL/g, '')),
      'the site beacon now names an external address, so the no-third-party claim needs revisiting',
    )
  })

  it('offers the switch it tells the reader they have', async () => {
    // THE CLASS: a privacy claim that describes a capability with no reachable
    // way to use it. The subprocessor page says, of the counting this site
    // does, that "if you switch measurement off" a flag is kept in this
    // browser. For as long as the only way to switch it off was a query
    // parameter documented in one source comment, that sentence described
    // something no reader could do, which is the same defect as a gate function
    // with no caller: everything built except the part that makes it happen.
    //
    // Held to three things rather than to the words: the beacon exports a way
    // to set it, a component calls that, and a page renders the component. Any
    // one of the three going missing leaves a promise on a published page.
    const page = await read('www/lib/subprocessors.ts')
    if (!/switch measurement off/.test(page)) return

    const beacon = await read('www/lib/beacon.ts')
    assert.match(
      beacon,
      /export function setMeasurement/,
      'the page says measurement can be switched off and the beacon exports no way to do it',
    )

    const control = await read('www/components/MeasurementSwitch.tsx').catch(() => null)
    assert.ok(
      control !== null,
      'the page says measurement can be switched off and there is no control that does it',
    )
    assert.match(
      control,
      /setMeasurement\(/,
      'the measurement control does not call setMeasurement, so pressing it changes nothing',
    )

    const privacy = await read('www/components/pages/company/Legal.tsx')
    assert.match(
      privacy,
      /<MeasurementSwitch \/>/,
      'the control exists and no page renders it, so no reader can reach it',
    )
  })

  it('would still see an address in code, which is what makes the case above worth anything', () => {
    // THE NEGATIVE CONTROL on the comment stripping immediately above. Taking
    // comments out of the subject of a gate is exactly the kind of loosening
    // that quietly turns a check into a check of nothing, and the failure would
    // be invisible: the suite stays green either way. So the same predicate is
    // driven against source that does name a host, in the three places a host
    // could actually be named.
    const inCode = [
      `const ENDPOINT = "https://plausible.io/api/event"`,
      `fetch('https://cdn.example.com/a.js')`,
      'const hosts = [`https://analytics.example.com`]',
    ]
    for (const line of inCode) {
      assert.ok(
        NAMES_A_HOST.test(withoutComments(line)),
        `stripping comments hid a real address: ${line}`,
      )
    }
    // And a comment quoting one is not a destination, which is the case that
    // sent this gate red on a branch that had added no address at all.
    assert.ok(
      !NAMES_A_HOST.test(withoutComments('// yandex announces "+http://yandex.com/bots"')),
      'a comment quoting a crawler address still reads as a network destination',
    )
  })
})

describe('the site does not publish a mailbox that cannot receive mail', () => {
  // The instance: the legal pages said "Security reports go to
  // security@antifailure.dev today" and "security@antifailure.dev reaches a
  // person who can act on it", while the CONTACT PAGE OF THE SAME SITE carried
  // a callout titled "Email is not a contact route" saying the domain has no
  // mail exchanger and its SPF policy authorises no senders. Both were live on
  // antifailure.dev at once, and the contact page is the one telling the truth:
  //
  //   $ dig +short MX antifailure.dev     (empty)
  //   $ dig +short TXT antifailure.dev    "v=spf1 -all"
  //
  // The class: a published address is a promise that somebody is on the other
  // end of it. Publishing one at a domain that cannot receive mail sends a
  // security researcher, a person asking for their data to be deleted, and a
  // customer with a problem all into the same silence, and none of them can
  // tell. It is worse than saying nothing, because saying nothing at least
  // makes them look for another route.
  //
  // This asserts the property rather than the two sentences that were wrong,
  // because a list of known-bad sentences is what let the third one through
  // further up this file.
  const PAGES = [
    'www/components/pages/company/Legal.tsx',
    'www/components/pages/company/Contact.tsx',
  ]

  for (const page of PAGES) {
    it(`publishes no address at antifailure.dev in ${path.basename(page)}`, async () => {
      const text = await read(page)
      const found = [...text.matchAll(/[A-Za-z0-9._%+-]+@antifailure\.dev/g)].map((m) => m[0])
      assert.deepEqual(
        [...new Set(found)],
        [],
        `${page} publishes an address at a domain with no mail exchanger. Mail sent there is ` +
          `delivered nowhere, and the site's own contact page says so. Name the route that ` +
          `works, which today is GitHub private vulnerability reporting, or add an MX record ` +
          `and a mailbox first.`,
      )
    })
  }

  it('is reading pages that mention the domain at all, so an empty result means something', async () => {
    // The negative control on the parse. A renamed or moved file reads as an
    // empty string here and every assertion above passes over nothing, which is
    // exactly the failure mode this file warns about at the top.
    for (const page of PAGES) {
      const text = await read(page)
      assert.match(
        text,
        /antifailure\.dev/,
        `${page} no longer mentions the domain at all, so the check above is reasoning about ` +
          `nothing. Either the file moved or the pattern stopped matching.`,
      )
    }
  })
})

describe('the terms describe guards that are really in the engine', () => {
  /**
   * The terms page now makes four claims about what the software can touch,
   * and each one is a claim about a mechanism rather than an intention. That
   * is the only reason they are publishable: an intention drifts silently and
   * a mechanism fails a test when somebody removes it.
   *
   * These are deliberately keyed on the CONSTRUCT rather than on a sentence.
   * Asserting the page contains a phrase would check that two files were
   * edited together, which is what a reviewer already does. Asserting the
   * engine still opens a read only transaction checks the thing the customer
   * is actually relying on.
   */
  const engineRoot = path.join(repoRoot, 'engine')
  const engine = (p: string) => readFile(path.join(engineRoot, p), 'utf8')

  it('reads the engine sources it is reasoning about, so an empty parse cannot pass', async () => {
    // Same negative control as the retention block above. Every assertion that
    // follows is a substring search, and a substring search over a file that
    // failed to load is a quiet pass.
    for (const file of [
      'internal/subset/execute.go',
      'internal/dockerutil/dockerutil.go',
      'internal/masking/rules.go',
      'internal/verify/scan.go',
      'internal/env/golden.go',
    ]) {
      const source = await engine(file).catch(() => '')
      assert.ok(source.length > 500, `${file} did not load, so the assertions over it prove nothing`)
    }
  })

  it('opens the customer source database in a read only transaction', async () => {
    // The terms say a connection string with more rights than it needs still
    // cannot be written through. That sentence is only true because Postgres
    // is enforcing it, not because the code declines to write.
    const source = await engine('internal/subset/execute.go')
    assert.match(
      source,
      /BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY/,
      'the terms page tells customers their production database is opened read only and that a ' +
        'over-privileged connection string still cannot be written through. Nothing in the ' +
        'subset path sets a read only transaction any more, so that sentence is now a promise ' +
        'rather than a mechanism.',
    )
  })

  it('refuses to remove a container Antifailure did not label', async () => {
    // The terms say teardown removes only resources carrying our own labels
    // and refuses anything else. The refusal is the claim; selecting by label
    // would not be enough, because a selection can be widened by a caller.
    const source = await engine('internal/dockerutil/dockerutil.go')
    assert.match(
      source,
      /ErrNotOurs/,
      'the terms page tells customers teardown refuses anything Antifailure did not create. ' +
        'The ownership refusal is gone, so teardown now removes whatever it was handed.',
    )
    assert.match(
      source,
      /func RemoveContainer[\s\S]{0,600}IsOurs\(insp\.Config\.Labels\)/,
      'RemoveContainer no longer checks ownership before removing, so the published claim that ' +
        'it refuses a container it does not own is false.',
    )
  })

  it('keeps masking on by default, with no way to switch it off', async () => {
    // The terms say there is no setting that disables masking and a project
    // with no rules file still gets the built-in set. Both halves come from
    // NewRuleSet appending the defaults underneath whatever was declared.
    const source = await engine('internal/masking/rules.go')
    // The APPEND, not a mention of DefaultRules anywhere in the function. The
    // first version of this matched the capacity hint on NewRuleSet's first
    // line, `make([]Rule, 0, len(rules)+len(DefaultRules()))`, so deleting the
    // loop that actually appends the defaults left the assertion passing. It
    // was an instrument that could not say no, found by breaking the code on
    // purpose and watching this stay green.
    assert.match(
      source,
      /for _, r := range DefaultRules\(\) \{/,
      'the terms page says a project with no rules file still gets the built-in rule set. ' +
        'NewRuleSet no longer appends DefaultRules, so an unconfigured project now masks nothing.',
    )
  })

  it('never publishes a golden whose verification found real data', async () => {
    const source = await engine('internal/env/golden.go')
    assert.match(
      source,
      /if !report\.Clean\(\)/,
      'the terms page says a golden whose verification scan finds real data is never published. ' +
        'The refusal is gone.',
    )
  })

  /**
   * THE ONE THAT IS A LIMIT RATHER THAN A GUARANTEE, and the reason it is here.
   *
   * The terms deliberately say the verification scan reads "the column types
   * that can hold a sentence" and samples rows, rather than saying it reads
   * every column. That wording is exact, and it is exact because it is
   * currently generous: the scan's type list and the masking default's type
   * list are the same six entries, so a citext or text[] column is masked by
   * neither and read by neither.
   *
   * Writing the stronger sentence would have been a lie. Writing this weaker
   * one and leaving it unguarded would let somebody later widen the scan,
   * making the page understate the product, or narrow it, making the page
   * overstate it. So the list itself is pinned. Changing it sends whoever
   * changed it to the sentence on the terms page that describes it.
   */
  it('pins the column types the verification scan can see, which the terms describe as a limit', async () => {
    const source = await engine('internal/verify/scan.go')
    assert.match(
      source,
      /c\.data_type IN \('text', 'character varying', 'character', 'json', 'jsonb', 'xml'\)/,
      'the set of column types the verification scan reads has changed. The terms page describes ' +
        'this scan as covering "the column types that can hold a sentence" and as a check that a ' +
        'rule missed a column rather than a proof that no personal data survives. If the list ' +
        'grew, that sentence now understates the product. If it shrank, it overstates it. Either ' +
        'way the page needs rereading, and so does the matching list in ' +
        'internal/masking/rules.go looksSensitive, which is the same six types and is what ' +
        'decides whether an unclassified column is emptied.',
    )
  })

  it('keeps the scan and the masking default agreeing about which types matter', async () => {
    // The two lists are the reason the sentence on the terms page is worded as
    // a limit. If they ever disagree, one layer is covering something the
    // other is not, and the honest description of the pair changes.
    const rules = await engine('internal/masking/rules.go')
    assert.match(
      rules,
      /func looksSensitive[\s\S]{0,400}case "text", "character varying", "character", "json", "jsonb", "xml":/,
      'looksSensitive no longer covers the same types as the verification scan. The masking ' +
        'default and the scan that backstops it are supposed to be described together on the ' +
        'terms page, and they can no longer be.',
    )
  })
})

describe('the acceptable use and developer policy pages describe real mechanisms', () => {
  it('reads the pages it is checking, so an empty parse cannot pass', async () => {
    const legal = await read('www/components/pages/company/Legal.tsx')
    assert.ok(
      /export function AcceptableUsePage/.test(legal) &&
        /export function DeveloperPolicyPage/.test(legal),
      'the two new legal pages are not in Legal.tsx, so every assertion below is vacuous',
    )
  })

  it('does not claim a suspension mechanism that the control plane lacks', async () => {
    // The acceptable use page says an organization can be suspended, that this
    // stops new work, and that it leaves the data in place. That is a specific
    // capability and it is the only enforcement action the page claims.
    const legal = await read('www/components/pages/company/Legal.tsx')
    if (!/can be suspended/.test(legal)) return

    const schema = await read('web/packages/db/src/schema.ts')
    assert.match(
      schema,
      /suspended/,
      'the acceptable use page says an organization can be suspended and that suspension leaves ' +
        'the data in place. Nothing in the schema records suspension any more, so the page ' +
        'describes an enforcement action that does not exist.',
    )
  })

  it('does not claim every endpoint is rate limited unless the registry is real', async () => {
    // The developer policy says every public endpoint has a limit declared in
    // one registry that the middleware reads. The registry is the claim.
    const legal = await read('www/components/pages/company/Legal.tsx')
    if (!/declared in a single registry/.test(legal)) return

    const limits = await read('web/apps/api/src/limits.ts')
    assert.match(
      limits,
      /export const ENDPOINT_LIMITS/,
      'the developer policy says every public endpoint has a rate limit declared in one ' +
        'registry. ENDPOINT_LIMITS is gone, so the limits are wherever somebody remembered to ' +
        'put them, which is the thing the page says is not the case.',
    )
  })

  it('does not claim a Model Context Protocol surface that is not shipped', async () => {
    // The whole second half of the developer policy is about a model driving
    // the engine. A page describing a surface that was removed would be
    // telling somebody to be careful about nothing.
    const legal = await read('www/components/pages/company/Legal.tsx')
    if (!/Model Context Protocol/.test(legal)) return

    const { access } = await import('node:fs/promises')
    await assert.doesNotReject(
      access(path.join(repoRoot, 'engine/internal/mcp/engine.go')),
      'the developer policy devotes a section to the engine Model Context Protocol ' +
        'surface, and engine/internal/mcp is gone',
    )
  })
})

describe('the legal pages do not deny a control plane the webhook creates tenants in', () => {
  /**
   * THE CLAIM THAT WAS FALSE, and how it got there.
   *
   * /terms said "Sign-in is for the waitlist. There is no public production
   * control plane yet", and /privacy said "Sign-in today is for the waitlist".
   * Both were true when written and both stopped being true when the GitHub
   * App started creating organizations.
   *
   * `rememberInstallation` in github/webhook.ts inserts into `organizations`
   * on an installation delivery, and its own comment says why: an installation
   * IS the moment a tenant begins. It consults no allowlist. The row lands on
   * the plan the schema defaults to, which is a real plan with real quotas.
   *
   * The nuance the corrected wording carries, and the reason it is not simply
   * "there is a public control plane": nothing can be SPENT in that
   * organization until somebody signs in, because createEnvironment is an
   * orgProcedure and orgProcedure runs requireActor. Sign-in is where the
   * allowlist bites. So an organization can exist for an account nobody let
   * in, and it can do nothing.
   *
   * This gate is keyed on the MECHANISM rather than on the sentence. It fails
   * if a page denies a public control plane while the webhook still creates
   * organizations, which is the combination that was published.
   */
  it('reads the webhook it is reasoning about, so an empty parse cannot pass', async () => {
    const webhook = await read('web/apps/api/src/github/webhook.ts')
    assert.ok(webhook.length > 500, 'github/webhook.ts did not load')
    assert.match(
      webhook,
      /INSERT INTO organizations/,
      'the webhook no longer creates organizations, so this gate is reasoning about a ' +
        'mechanism that is gone and the pages it constrains may need rereading',
    )
  })

  it('does not deny a control plane while an installation still creates a tenant', async () => {
    const webhook = await read('web/apps/api/src/github/webhook.ts')
    const createsTenants = /INSERT INTO organizations/.test(webhook)
    if (!createsTenants) return

    const pages = await read('www/components/pages/company/Legal.tsx')
    const denials: [RegExp, string][] = [
      [
        /no public production control plane yet/i,
        'installing the GitHub App creates an organization, so there is one',
      ],
      [
        /[Ss]ign-in (?:today )?is for the waitlist/,
        'signing in grants membership of an organization the App created, not a place on a list',
      ],
    ]
    for (const [pattern, why] of denials) {
      assert.ok(
        !pattern.test(pages),
        `a legal page denies something the code does: ${why}. rememberInstallation in ` +
          `github/webhook.ts inserts into organizations on an installation delivery and ` +
          `consults no allowlist.`,
      )
    }
  })

  it('keeps the spending guard the corrected wording relies on', async () => {
    // The page says nothing can be run until somebody signs in. That is only
    // true while creating an environment requires an actor, so the sentence
    // and the middleware have to move together.
    const dispatch = await read('web/apps/api/src/routers/dispatch.ts')
    const trpc = await read('web/apps/api/src/trpc.ts')
    assert.match(
      dispatch,
      /export const createEnvironment = orgProcedure\(/,
      'createEnvironment is no longer an orgProcedure, so the claim on /terms that nothing ' +
        'can be run in an organization until somebody signs in may no longer hold',
    )
    assert.match(
      trpc,
      /export function orgProcedure[\s\S]{0,200}requireActor/,
      'orgProcedure no longer requires an actor, so an organization created by an ' +
        'installation could act with nobody signed in, and /terms says it cannot',
    )
  })
})

describe('the enterprise licence and the terms it points at agree', () => {
  /**
   * ONE PUBLISHED LEGAL DOCUMENT HELD TO ANOTHER, which is a step past the rest
   * of this file: everything above holds prose to CODE, and this holds prose to
   * prose, because the contradiction was between two documents and neither was
   * wrong on its own.
   *
   * WHAT WAS WRONG. ee/LICENSE.md permitted production use of the enterprise
   * directory only if you "have agreed to, and are in compliance with, the
   * Antifailure Terms of Service, available at https://antifailure.dev/terms,
   * or a substantially similar written agreement". The page at that address
   * says of itself that it is not a paid-service agreement, and leaves the
   * contracting entity, the registered address, the governing law and the
   * liability cap deliberately blank. So the condition a customer had to
   * satisfy resolved, for the route the licence named FIRST, to a document
   * stating it is not the kind of document that could satisfy it. A reader
   * could not comply by reading.
   *
   * It was bounded rather than total: the licence also accepted a negotiated
   * written agreement, so an enterprise deal with a signed contract was
   * unaffected. What was broken is the self serve path, which is the one a
   * reader can follow without talking to a human.
   *
   * HOW IT IS RESOLVED, and why this direction. Two ways out: make the page an
   * agreement, or stop naming it. Making it one would mean publishing a
   * contract with no contracting entity, no governing law and no cap, which is
   * not an agreement either, only one that hides its own gap better. So the
   * licence stops naming it, and this gate makes that a PAIR rather than a
   * single edit: the day somebody fills those blanks in and the page becomes a
   * real agreement, the second assertion tells them the licence may name it
   * again.
   *
   * The prepared version of this gate was written to be RED, as a way of
   * recording the contradiction until somebody decided. It is green because the
   * decision is made, and it is written so it goes red again if either half
   * moves without the other.
   */
  it('reads both documents, so an empty parse cannot pass', async () => {
    const licence = await read('ee/LICENSE.md')
    const pages = await read('www/components/pages/company/Legal.tsx')
    assert.ok(licence.length > 500, 'ee/LICENSE.md did not load')
    assert.ok(pages.length > 500, 'Legal.tsx did not load')
    // The operative sentence, so a licence rewritten past recognition fails
    // here rather than passing every assertion below by containing nothing.
    assert.match(
      licence,
      /may only be\s+used in production if you/,
      'ee/LICENSE.md no longer states a production-use condition at all, so nothing below is ' +
        'checking what it was written to check',
    )
  })

  it('does not condition production use on a page that disclaims being an agreement', async () => {
    const licence = await read('ee/LICENSE.md')
    const pages = await read('www/components/pages/company/Legal.tsx')

    // Only the OPERATIVE clause. The licence explains at length why it stopped
    // naming that URL and quotes the URL to do it, and a rule that could not
    // tell an explanation from a condition would force the correction to be
    // made silently, which is the opposite of what this repository wants.
    const clause = licence.slice(
      licence.indexOf('## Terms'),
      licence.indexOf('### Why this does not name a public terms page'),
    )
    const conditionsOnThePage = /antifailure\.dev\/terms/.test(clause)
    const pageDisclaims = /not a paid-service agreement/i.test(pages)

    assert.ok(
      !(conditionsOnThePage && pageDisclaims),
      'ee/LICENSE.md conditions production use of the enterprise directory on agreeing to the ' +
        'Terms of Service at https://antifailure.dev/terms, and that page says these terms are ' +
        'not a paid-service agreement. A customer following the self serve route arrives at a ' +
        'document disclaiming that it is the kind of document the licence requires. Either ' +
        '/terms becomes an agreement, entity and governing law and cap included, or the licence ' +
        'stops naming it.',
    )
  })

  it('tells whoever fills in the blanks that the licence may name the page again', async () => {
    // The direction the assertion above cannot see. It goes quiet the moment
    // the licence stops naming the page, and quiet is exactly how the pair
    // would drift back apart: somebody makes /terms a real agreement, nothing
    // says the licence could accept it, and the self serve route stays closed
    // for a reason that has gone away.
    const licence = await read('ee/LICENSE.md')
    const pages = await read('www/components/pages/company/Legal.tsx')
    const clause = licence.slice(
      licence.indexOf('## Terms'),
      licence.indexOf('### Why this does not name a public terms page'),
    )
    if (/antifailure\.dev\/terms/.test(clause)) return

    if (!/not a paid-service agreement/i.test(pages)) {
      assert.fail(
        '/terms no longer says it is not a paid-service agreement, so it may now be one, and ' +
          'ee/LICENSE.md has stopped naming it. The self serve route to an enterprise licence ' +
          'is closed for a reason that has gone away. Either restore the page\'s disclaimer or ' +
          'name the page in the licence again, and delete the section in ee/LICENSE.md that ' +
          'explains why it does not.',
      )
    }
  })

  it('leaves a reader of the licence somewhere to go', async () => {
    // The failure this replaces was a condition nobody could satisfy by
    // reading. Removing the route it named is only half a fix: a licence that
    // says "a written agreement" and gives no way to ask for one is the same
    // dead end wearing different words.
    const licence = await read('ee/LICENSE.md')
    assert.match(
      licence,
      /antifailure\.dev\/contact/,
      'ee/LICENSE.md requires a written agreement and names no way to ask for one',
    )
    // The address it used to name could not receive anything, on a domain with
    // no mail exchanger and an SPF policy authorizing no sender.
    assert.ok(
      !/licensing@antifailure\.dev\.\s*$/m.test(licence),
      'ee/LICENSE.md answers a licensing question with an email address on a domain that ' +
        'publishes no mail exchanger',
    )
  })

  it('the page says what the licence actually requires, so the two are readable together', async () => {
    const pages = await read('www/components/pages/company/Legal.tsx')
    assert.match(
      pages,
      /Running it in production requires a written agreement with Antifailure/,
      '/terms does not say what running the enterprise edition requires, so a reader sent there ' +
        'by ee/LICENSE.md learns nothing about the condition they are under',
    )
  })
})
