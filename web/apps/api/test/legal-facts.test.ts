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
    const beacon = await read('www/lib/analytics.ts').catch(() => null)

    if (beacon === null) {
      assert.match(
        page,
        /This site loads no analytics and no third-party script/,
        'there is no www/lib/analytics.ts, so the subprocessor page must still say the site ' +
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
      !/https?:\/\/(?!127\.0\.0\.1)/.test(beacon.replace(/CONTROL_PLANE_URL/g, '')),
      'the site beacon now names an external address, so the no-third-party claim needs revisiting',
    )
  })
})
