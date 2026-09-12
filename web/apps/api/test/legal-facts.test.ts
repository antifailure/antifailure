// The published legal pages, against the thing that decides them.
//
// THE CLASS THIS EXISTS FOR, which is the finding rather than any one instance.
//
// Seven published claims were found false in one night: backup retention saying
// fourteen days while production runs thirty-five, log retention documented
// nowhere, the privacy page saying there was no billing and that nothing
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
// the top of www/lib/legal-facts.ts, and it stays a judgement. It also cannot
// see a claim nobody thought to encode: a page can still say something false
// about a subject this file does not know about. What it does do is make the
// half that IS checkable fail loudly at the moment the code moves, which is the
// moment all seven of these went wrong.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { readFile, readdir } from 'node:fs/promises'
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
const here = path.dirname(fileURLToPath(import.meta.url))
const repoRoot = path.join(here, '..', '..', '..', '..')

const read = (p: string) => readFile(path.join(repoRoot, p), 'utf8')

/** An http or https address that is not the loopback a local control plane
 *  serves on. Named once so the gate and its negative control cannot drift. */
const NAMES_A_HOST = /https?:\/\/(?!127\.0\.0\.1)/

/**
 * A PostHog host written as a DESTINATION rather than named in a sentence.
 *
 * Anchored on a scheme for the same reason NAMES_A_HOST is: the published copy
 * has to be able to say "your browser does not talk to a posthog.com host",
 * which is prose and not a network destination. Named here once so the two
 * gates below and the negative control at the end cannot drift apart.
 */
const POSTHOG_URL = /https?:\/\/[a-z0-9.-]*posthog\.com[^"'`\s)]*/g

/**
 * The hosts a browser must never be pointed at.
 *
 * `i.posthog.com` is the suffix of us.i, eu.i, us-assets.i and eu-assets.i, so
 * naming it once covers every ingestion and asset host in both regions, and
 * app.posthog.com is the legacy spelling posthog-js still rewrites internally.
 * The assets half matters as much as the ingestion half: the recorder bundle is
 * the largest and most blockable request the library makes.
 */
const INGESTION_HOST = /\b(?:[a-z0-9-]+\.)*i\.posthog\.com|\bapp\.posthog\.com/

/**
 * A PRESENT TENSE denial that this site engages PostHog.
 *
 * The published sentence was "There is no Sentry, no Datadog, no PostHog, no
 * Google Analytics", so the shape is an "is no" enumeration reaching PostHog
 * before the sentence ends. Past tense is deliberately not matched: the page
 * keeps a change log which has to be able to say what it used to claim.
 */
/**
 * The page promising a reader can turn the counting off.
 *
 * A LIST OF PHRASINGS RATHER THAN ONE STRING, and this is not defensive
 * generality, it is a defect that was live. The gate below fired on the literal
 * "switch measurement off" and returned early otherwise. The copy was rewritten
 * to say "the switch on the privacy page", the phrase disappeared, and the whole
 * gate silently asserted NOTHING while continuing to report a pass. Everything
 * it checks, that the beacon exports a setter, that a control calls it, that a
 * page renders it, and that PostHog consults the same flag, went unchecked at
 * exactly the moment a second analytics vendor was added.
 *
 * A skip reads as a pass. That is the sentence this file opens with, and this is
 * the file's own gate doing it. The companion test below fails if this stops
 * matching, so a future rewording turns the suite red rather than quiet.
 */
const PROMISES_A_SWITCH =
  /switch\s+measurement\s+off|switch\s+the\s+measurement\s+off|turn\s+measurement\s+off|the\s+switch\s+on\s+the\s+privacy\s+page|the\s+switch\s+below|measurement\s+can\s+be\s+switched\s+off/i

const DENIES_POSTHOG = /\b(?:is|are)\s+no\b[^.]{0,200}?\bno PostHog\b|\bthere is no PostHog\b/i

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

/**
 * Every source file of the marketing site that the browser actually runs.
 *
 * Used by the PostHog rules below, which are about where a reader's browser is
 * POINTED. That is a property of code, so this reads code.
 *
 * ONE FILE IS EXCLUDED BY NAME AND THE EXCLUSION IS THE INTERESTING PART.
 * www/lib/legal-facts.ts is prose stored as string literals: it is the published
 * legal copy, and that copy has to be able to NAME the vendor and its hosts in a
 * sentence. `withoutComments` cannot help,
 * because a sentence in a string literal is code as far as any parser here is
 * concerned. This is the same problem the comment stripping already solved one
 * level down, and it has the same answer: a gate people cannot explain
 * themselves next to is a gate they route around by deleting the explanation.
 *
 * Nothing is lost by the exclusion. Those two files are the SUBJECT of the
 * disclosure pair above rather than a place the SDK could be configured, and a
 * site that tried to configure posthog-js from inside its own subprocessor list
 * has a problem this gate is not the right instrument for.
 */
async function siteSources(): Promise<{ file: string; text: string }[]> {
  const EXCLUDED = new Set(['www/lib/legal-facts.ts'])
  // Nothing under www/test reaches a browser, which is the reason
  // tools/routecheck skips it too. It also has to be skipped rather than
  // merely being harmless: the site's own test asserts that the vendor's
  // ingestion hosts are ABSENT from what is handed to the library, and an
  // assertion that a string is absent has to be able to name the string. A
  // gate that failed on it would be refusing the test written to enforce the
  // same rule.
  const out: { file: string; text: string }[] = []
  const walk = async (dir: string): Promise<void> => {
    let entries
    try {
      entries = await readdir(path.join(repoRoot, dir), { withFileTypes: true })
    } catch {
      return
    }
    for (const e of entries) {
      const rel = `${dir}/${e.name}`
      if (e.isDirectory()) {
        if (e.name === 'node_modules' || e.name === 'out' || e.name === '.next') continue
        await walk(rel)
      } else if (/\.tsx?$/.test(e.name) && !EXCLUDED.has(rel) && !rel.startsWith('www/test/')) {
        out.push({ file: rel, text: await read(rel) })
      }
    }
  }
  await walk('www')
  return out
}

const facts = await read('www/lib/legal-facts.ts')

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

describe('the privacy page describes the code that exists', () => {
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
    // as the privacy page and fixing one would have left the other.
    const page = await read('www/components/pages/company/Legal.tsx')
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
        `the privacy page publishes a claim that is false about the code: ${why}`,
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
    //
    // WHAT CHANGED WHEN POSTHOG ARRIVED, and why this was rewritten rather than
    // deleted. Until tonight the claim this held was "this site loads no
    // analytics and no third-party script", and both halves of the pair were
    // about the first party beacon. That claim stops being true, so a gate that
    // only knew how to assert it would have had to be deleted, and a deleted
    // gate is how the next false sentence gets published. So the SUBJECT moves
    // and the SHAPE does not: it still keys on whether the code is in the tree,
    // it still asserts both states rather than skipping one, and the claim it
    // holds is now the true one. Analytics goes to PostHog, through an endpoint
    // on our own infrastructure, and PostHog is disclosed by name.
    const page = await read('www/components/pages/company/Legal.tsx')
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
        'there is no site beacon in this tree, so the privacy page must still say the site ' +
          'loads no analytics. It says something else, which means the claim was rewritten ' +
          'for a beacon that is not in this tree.',
      )
      return
    }

    // A NEGATIVE, NOT A PHRASE MATCH, and the difference cost a run against the
    // real copy. This asserted that one of two replacement sentences was
    // PRESENT, which holds the copy to a form of words rather than to a fact,
    // and the sentence that actually landed says the same thing in neither of
    // them. The fact is that the denial has to be gone; what replaces it is the
    // writer's to choose. So the exact string the other branch REQUIRES is the
    // string this branch REFUSES, which also makes the pair symmetrical instead
    // of two unrelated rules facing opposite directions.
    assert.doesNotMatch(
      page,
      /This site loads no analytics and no third-party script/,
      'a beacon exists and the privacy page still claims the site loads no analytics',
    )
    assert.ok(
      !NAMES_A_HOST.test(
        withoutComments(beacon).replace(/CONTROL_PLANE_URL/g, '').replace(/POSTHOG_PATH/g, ''),
      ),
      'the site beacon now names an external address, so the no-third-party claim needs revisiting',
    )
  })

  it('discloses PostHog exactly when the site actually loads it, in both directions', async () => {
    // THE PAIR THIS WAS BUILT FOR, and the reason it is a pair rather than an
    // assertion. The published sentence is currently "There is no Sentry, no
    // Datadog, no PostHog, no Google Analytics", and the moment posthog-js is
    // added to the site that sentence is a false statement in a legal page.
    // Asserting only the new state would let the dependency land while the page
    // still denies it, which is the failure; asserting only the old state would
    // block the change. So the code decides which sentence has to be there.
    //
    // KEYED ON THE DEPENDENCY, NOT ON A STRING IN THE PAGE. A gate that read
    // the page to decide what the page must say is a gate that agrees with
    // itself. www/package.json is the one place that cannot lie about whether
    // the browser bundle contains posthog-js.
    const page = await read('www/components/pages/company/Legal.tsx')
    const manifest = JSON.parse(await read('www/package.json')) as {
      dependencies?: Record<string, string>
      devDependencies?: Record<string, string>
    }
    const loadsPostHog = Boolean(
      manifest.dependencies?.['posthog-js'] ?? manifest.devDependencies?.['posthog-js'],
    )

    if (!loadsPostHog) {
      assert.match(
        page,
        DENIES_POSTHOG,
        'posthog-js is not a dependency of the site, so the privacy page must still deny ' +
          'PostHog by name. It no longer does, which means the disclosure was written for an ' +
          'analytics vendor that is not in this tree.',
      )
      assert.doesNotMatch(
        page,
        /PostHog(?:, Inc\.)? receives\b/,
        'posthog-js is not a dependency and the privacy page still discloses PostHog as a ' +
          'recipient, which publishes a vendor this site does not load. Copy without code is the ' +
          'same defect as code without copy, arriving from the other side.',
      )
      return
    }

    // PRESENT TENSE, WHICH IS THE WHOLE OF THE RULE. The page carries a change
    // log of its own, and that log has to be able to QUOTE the denial it
    // removed: the real entry reads "the entry below the list said in as many
    // words that there WAS no PostHog". A gate matching a bare "no PostHog"
    // refuses the honest record of the correction, which is the same class of
    // mistake as a gate that cannot be explained next to, and people route
    // around that one by deleting the explanation.
    assert.doesNotMatch(
      page,
      DENIES_POSTHOG,
      'posthog-js is a dependency of the site and the privacy page still says there is no ' +
        'PostHog. That is a false statement in a published legal page, and it is false from the ' +
        'moment the dependency lands rather than from the moment somebody notices.',
    )
    // THE PAGE HAS TO SAY THE TRUE THING, NOT MERELY NAME THE VENDOR. This is
    // the half that a proxy makes easy to get wrong, and the reason it is
    // checked here rather than left to prose review.
    //
    // A proxy changes the destination the browser connects to. It does not
    // change who receives the data. PostHog, Inc. receives every event, every
    // autocaptured interaction and every session recording whether the request
    // went direct or through us. A page that named the vendor while implying the
    // proxy kept anything inside our own boundary would be worse than the
    // denial it replaced, because a reader could open a network tab, see no
    // vendor host, and take that as verification of a claim that is false.
    assert.match(
      page,
      /PostHog(?:, Inc\.)? receives\b/,
      'the privacy page never says that PostHog receives the data. The proxy is transport and not ' +
        'a boundary, so a page that does not say who receives it describes an arrangement the ' +
        'reader would have to infer, and the obvious inference from a first party endpoint is ' +
        'the wrong one.',
    )
    assert.match(
      page,
      /PostHog Cloud (?:US|EU)|United States|European Union/,
      'the privacy page does not name the cloud region the data is processed in, which is the ' +
        'first thing a security review asks of a processor and the one fact a reader cannot ' +
        'work out from the endpoint they can see.',
    )

    // AND NO PAGE MAY STILL CLAIM NOBODY RECEIVES IT. A closed list of the
    // specific containment sentences this site has actually published or nearly
    // published, rather than a clever pattern: a broad one would refuse the
    // honest sentences beside them, and "no cookie is set" or "PostHog never
    // receives your IP address" are both true and both have to survive.
    for (const claim of [
      /No third party sees anything/,
      /this site loads no analytics/i,
      /no third party (?:receives|sees|gets) (?:any|your) data/i,
      /(?:stays|stay|remains|never leaves) (?:inside |within )?(?:our|your) (?:boundary|infrastructure|servers)/i,
    ]) {
      assert.doesNotMatch(
        page,
        claim,
        `the site loads posthog-js and the privacy page still publishes ${claim}. The proxy ` +
          'is transport: it changes which host the browser connects to and not who receives the ' +
          'data, so that sentence is false and it is false in the direction a reader cannot check.',
      )
    }
  })

  it('sends analytics to an endpoint we run, and never to a posthog.com ingestion host', async () => {
    // WHAT THIS DOES AND DOES NOT ASSERT, because getting that wrong here would
    // be the same mistake in a gate that the copy was rewritten to stop making.
    //
    // A proxy changes the DESTINATION THE BROWSER CONNECTS TO. It does not
    // change WHO RECEIVES THE DATA: PostHog, Inc. receives every event, every
    // autocaptured interaction and every session recording either way. So this
    // asserts a transport property and nothing more. It is worth asserting,
    // because a direct vendor host is the proxy being bypassed rather than
    // used, and because the recorder bundle is the largest and most blockable
    // request posthog-js makes, so a blocked one kills replay while ingest goes
    // on looking healthy. It is NOT evidence that the data stayed anywhere, and
    // nothing in this test's messages may suggest that it is. The gate that
    // holds the receiving claim is the disclosure pair above.
    //
    // IT WOULD BE ONE CHARACTER TO BREAK. posthog-js takes api_host, and the
    // value in the vendor's own quickstart is https://us.i.posthog.com.
    // Pasting it does not break a build, fail a type check, or change a
    // rendered pixel.
    //
    // ANCHORED ON A SCHEME, WHICH IS THE FIX FOR THE FIRST VERSION OF THIS.
    // That version forbade a bare `posthog.com` anywhere but a ui_host line,
    // and it would have refused the sentence that makes the disclosure honest:
    // the privacy page says "Your browser does not talk to a posthog.com host".
    // That is prose, not a destination. A destination has a scheme, which is
    // the same distinction NAMES_A_HOST above already draws, so it is drawn the
    // same way here rather than by keeping a list of pages to skip.
    const sources = await siteSources()
    assert.ok(sources.length > 0, 'no site source was read, so this gate checked nothing')

    for (const { file, text } of sources) {
      const code = withoutComments(text)
      for (const [url] of code.matchAll(POSTHOG_URL)) {
        assert.doesNotMatch(
          url,
          INGESTION_HOST,
          `${file} points the browser at ${url}, which is a PostHog ingestion or asset host. ` +
            'The browser is supposed to reach PostHog only through the proxy on our own control ' +
            'plane, so this is the proxy being bypassed rather than used, and a content blocker ' +
            'that matches that host silently removes part of the measurement.',
        )
        // A non-ingestion posthog.com URL is permitted only as ui_host, which
        // posthog-js uses to build links a person clicks and never fetches.
        // Matched case insensitively because the value is held in a constant
        // read from NEXT_PUBLIC_POSTHOG_UI_HOST, so the identifier on the line
        // is spelled in capitals.
        const line = code.split('\n').find((l) => l.includes(url)) ?? ''
        assert.match(
          line,
          /ui_host/i,
          `${file} names ${url} on a line that has nothing to do with ui_host: ${line.trim()}. ` +
            'ui_host builds links a reader clicks through to and is never fetched by the browser, ' +
            'which is the only reason a posthog.com host is allowed in this tree at all.',
        )
      }
    }
  })

  it('points posthog-js at the mount the control plane actually serves', async () => {
    // THE OTHER HALF, and without it the rule above is satisfied by a site with
    // no api_host at all, which then falls back to the vendor's own default.
    // Refusing the wrong host is not the same as requiring the right one: the
    // first is satisfied by silence and the second is not.
    //
    // READS A URL RATHER THAN AN `api_host:` LINE, which is the second fix the
    // site's real code forced. The configured value is a constant,
    // `api_host: apiHost`, so a gate looking for a literal beside that key
    // would have read the identifier and concluded the site was pointed at a
    // host called "apiHost". So this looks for the URL wherever it is written,
    // and holds its path against the mount taken from the proxy's own source
    // rather than from a second copy of the string.
    const proxy = await read('web/apps/api/src/analytics/posthog.ts')
    const mount = proxy.match(/export const POSTHOG_MOUNT = '([^']+)'/)
    assert.ok(mount, 'the proxy no longer declares POSTHOG_MOUNT, so this gate cannot know the path')

    const sources = (await siteSources()).filter((f) => /posthog/i.test(f.file))
    if (sources.length === 0) return

    const urls = sources.flatMap(({ text }) =>
      [...withoutComments(text).matchAll(/https?:\/\/[^"'`\s)]+/g)].map((m) => m[0]),
    )
    assert.ok(
      urls.some((u) => {
        try {
          return new URL(u).pathname.replace(/\/$/, '') === mount[1]
        } catch {
          return false
        }
      }),
      `nothing in the site's PostHog configuration names ${mount[1]}, which is the path the ` +
        `control plane actually forwards. The URLs it does name are: ${urls.join(', ') || 'none'}. ` +
        'Either every event is being sent somewhere this repository does not forward, or the ' +
        'site has no api_host and posthog-js has fallen back to the vendor default.',
    )
  })

  it('is still reading a page that promises the switch, so the gate above cannot skip quietly', async () => {
    // THE COMPANION TO A CONDITIONAL GATE, and the reason it exists is that the
    // gate above had already skipped. Its trigger was one literal phrase, the
    // copy was rewritten, and it went from checking four things to checking
    // none without changing its result. Nothing in the suite could tell.
    //
    // So the trigger itself is now asserted. This site does promise a reader
    // can turn the counting off, on a page it publishes, and if that stops
    // being true the honest outcome is a red test asking whether the promise
    // was withdrawn on purpose, not a green one that quietly stopped looking.
    const page = await read('www/components/pages/company/Legal.tsx')
    assert.match(
      page,
      PROMISES_A_SWITCH,
      'the privacy page no longer promises the reader a way to switch measurement off in ' +
        'any wording this knows. If the promise was withdrawn, delete this test and the gate ' +
        'above with it. If it was reworded, add the wording, because until you do that gate is ' +
        'passing without checking anything.',
    )
  })

  it('would still see an address in code, and a PostHog host, which is what makes the cases above worth anything', () => {
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

    // THE SAME CONTROL ON THE POSTHOG RULE, and it is the one that matters
    // most, because that rule is a regular expression over host names and a
    // regular expression that matches nothing passes every file in the tree.
    // So it is driven against each spelling it has to catch, one at a time.
    for (const host of [
      'https://us.i.posthog.com',
      'https://eu.i.posthog.com',
      'https://i.posthog.com',
      'https://us-assets.i.posthog.com',
      'https://eu-assets.i.posthog.com',
      'https://app.posthog.com',
    ]) {
      assert.ok(
        INGESTION_HOST.test(withoutComments(`const api_host = "${host}"`)),
        `the ingestion host rule does not catch ${host}, so it would pass a site pointed there`,
      )
    }
    // And the one spelling that is allowed, which must NOT be caught by the
    // ingestion rule, or the exception below it could never be reached.
    assert.ok(
      !INGESTION_HOST.test(withoutComments('ui_host: "https://us.posthog.com"')),
      'the ingestion rule catches ui_host, so the permitted case is unreachable and the gate ' +
        'refuses a correct configuration',
    )

    // THE SCHEME ANCHOR, driven from both sides. A destination has a scheme; a
    // sentence naming the vendor does not, and the published privacy page has
    // to be able to say one.
    assert.equal(
      [...'Your browser does not talk to a posthog.com host'.matchAll(POSTHOG_URL)].length,
      0,
      'the destination rule reads a host named in prose as a destination, so it refuses the ' +
        'sentence that makes the disclosure honest',
    )
    assert.equal(
      [...'const h = "https://us.i.posthog.com"'.matchAll(POSTHOG_URL)].length,
      1,
      'the destination rule does not see a real URL, so it would pass a site pointed at one',
    )

    // THE DENIAL PREDICATE, and this is the one most able to be quietly wrong,
    // because a regular expression written to spare a change log is one edit
    // away from sparing everything. Both sides are driven: the sentence that
    // was actually false has to be caught, and the change log's past tense
    // record of removing it must not be.
    assert.ok(
      DENIES_POSTHOG.test(
        'There is no Sentry, no Datadog, no PostHog, no Google Analytics, and this site loads no script from another origin.',
      ),
      'the denial rule does not catch the exact sentence that was published and false, so it ' +
        'would have passed the day posthog-js landed',
    )
    assert.ok(
      DENIES_POSTHOG.test('There is no PostHog on this site.'),
      'the denial rule only catches one phrasing of the claim',
    )
    assert.ok(
      !DENIES_POSTHOG.test(
        'the entry below the list said in as many words that there was no PostHog, and a correction that hid what it corrected would be worse',
      ),
      'the denial rule refuses the page\'s own record of removing the denial, so the honest ' +
        'change log is what fails the gate',
    )
    assert.ok(
      !DENIES_POSTHOG.test('PostHog IS engaged now, and it has its own row on the list above.'),
      'the denial rule fires on the disclosure that replaced the denial',
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
    const page = await read('www/components/pages/company/Legal.tsx')
    if (!PROMISES_A_SWITCH.test(page)) return

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

    // THE HALF THAT ARRIVED WITH POSTHOG, and it is the same defect one level
    // out. Everything above proves a reader can PRESS the switch. None of it
    // proves that pressing it stops the counting, and until tonight that did
    // not matter because there was one counter and the switch was wired to it.
    //
    // With a second analytics vendor in the browser, a switch that silences the
    // first party beacon and leaves PostHog capturing is a published promise
    // that is false for the reader who believed it. That is worse than the
    // original defect, not a smaller version of it: the page no longer merely
    // describes something unreachable, it describes something the reader
    // reaches and is then wrong about.
    //
    // Held structurally, on whichever file configures the SDK, because the
    // wiring is ph-web's to write and the property is not: the code that starts
    // PostHog has to consult the same measurement flag the switch sets.
    const manifest = JSON.parse(await read('www/package.json')) as {
      dependencies?: Record<string, string>
    }
    if (!manifest.dependencies?.['posthog-js']) return

    const starts = (await siteSources()).filter((f) =>
      /posthog\.init\(|from ['"]posthog-js['"]/.test(withoutComments(f.text)),
    )
    assert.ok(
      starts.length > 0,
      'posthog-js is a dependency and nothing in the site imports it, so either the dependency ' +
        'is dead or this gate is reading the wrong tree',
    )
    for (const { file, text } of starts) {
      const code = withoutComments(text)
      assert.match(
        code,
        /measurementStatus|setMeasurement|measurementOn/,
        `${file} starts PostHog without consulting the measurement flag the switch sets. The ` +
          'published page promises a reader can switch measurement off; they can press the ' +
          'control, the beacon stops, and PostHog keeps capturing.',
      )

      // THE PART THAT LOOKS DONE AND IS NOT, and this is measured rather than
      // reasoned: ph-web pressed the switch and watched the network.
      //
      // `opt_out_capturing()` alone reads exactly as though it worked. It
      // writes the flag, renders correctly, and produces no request. The next
      // navigation then sent a 47KB $snapshot of the page the reader had just
      // objected to, plus the $autocapture for the click on the switch itself,
      // flushed by posthog-js's own unload handler out of buffers that opt out
      // does not empty. The line in posthog-js that discards the recorder
      // buffer sits inside a branch that only runs under a project setting this
      // site does not have.
      //
      // So the opt out has to do two more things, and their absence is
      // invisible in review and in every screenshot: discard the recording
      // explicitly, and turn request batching off, because the unload handler
      // flushes the request and retry queues only while batching is on. Held
      // structurally on the names, because this gate cannot drive a browser and
      // the alternative to naming them is checking nothing.
      assert.match(
        code,
        /stopSessionRecording/,
        `${file} opts out without discarding the session recording, so the buffered snapshot of ` +
          'the page the reader just objected to is flushed on the next navigation. Measured: ' +
          'seven proxy requests before the press and nine after.',
      )
      assert.match(
        code,
        /request_batching/,
        `${file} opts out without turning request batching off, so posthog-js's unload handler ` +
          'still has a request queue to flush and sends what was already buffered.',
      )
    }
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
      'internal/verify/dialect.go',
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
   * This used to pin a literal six type allowlist, `data_type IN ('text',
   * 'character varying', 'character', 'json', 'jsonb', 'xml')`, because that
   * was what the scan read and the terms said so in the weaker words "the
   * column types that can hold a sentence". The scan was widened after it was
   * found saying clean about a bytea column holding sealed key material, and
   * this assertion is what sent the change to the terms page: a pinned list
   * that grows makes the page understate the product, and one that shrinks
   * makes it overstate it. It did its job, so it is kept, pointed at the
   * mechanism the scan has now instead of at the list it used to have.
   *
   * What the page may now say, and what these assertions hold it to: the scan
   * reads every column it can read as text, names the ones it cannot along
   * with their types, and fails rather than passes when such a column has no
   * rule and a name that says it holds a secret. It still samples rows, and it
   * is still not a proof that no personal data survives.
   */
  it('pins the mechanism the verification scan uses, which the terms describe as a limit', async () => {
    // The classification moved when the scanner gained a second engine: it
    // used to be one Postgres query in scan.go and it is now the shared step
    // every source goes through, in dialect.go. The claim did not move, so
    // this follows the code rather than being relaxed, and it is pinned two
    // ways instead of one.
    const source = await engine('internal/verify/dialect.go')
    // Not an allowlist of readable types any more. Every column is listed and
    // classified, and only the structural types, the numbers, times, booleans
    // and uuids that cannot hold a sentence at all, are dropped. Every other
    // answer assigns a kind rather than skipping, which is what makes the
    // page's "names the ones it cannot" true.
    assert.match(
      source,
      /switch s\.d\.Kind\(c\.Type\) \{\n\t\tcase "structural":[\s\S]{0,400}?return nil\n\t\tcase "bytea":[\s\S]{0,200}?default:\n\t\t\tc\.kind = kindText/,
      'the verification scan no longer lists every column and classifies it. The terms page says ' +
        'it reads every column it can read as text and names the ones it cannot, and that ' +
        'sentence is only true while the column listing drops nothing but the structural types.',
    )
    // The narrowing this exists to catch, stated as the thing that must NOT
    // come back. The original defect was a literal six type allowlist in the
    // listing statement, and a statement that filters by type again is how the
    // page starts overstating the product without any assertion above noticing.
    assert.doesNotMatch(
      source,
      /data_type IN \(/,
      'the column listing statement filters by type again. That is the allowlist the scan was ' +
        'widened away from after it said clean about a bytea holding sealed key material, and ' +
        'the terms page describes the wider mechanism.',
    )
    // The half that turns "I could not read it" into a refusal. Without this
    // the page's second sentence, that such a column fails rather than passes,
    // is false.
    const scan = await engine('internal/verify/scan.go')
    assert.match(
      scan,
      /const DetectorUnreadSensitive = "unread-sensitive-name"/,
      'the finding raised for a column the scan cannot read, that nothing masks, and whose name ' +
        'says it holds a secret is gone. The terms page says that column fails the scan.',
    )
  })

  it('keeps the masking default covering the types it says it covers', async () => {
    // The scan and this list used to be the same six entries, and the terms
    // described the pair together. They are deliberately different now: the
    // scan reads everything it can read, and this is the narrower question of
    // which unclassified column the built in rules EMPTY. Widening it would
    // empty columns nobody asked to have emptied, so it stays, and the page
    // no longer describes the two as one list.
    const rules = await engine('internal/masking/rules.go')
    assert.match(
      rules,
      /func looksSensitive[\s\S]{0,400}case "text", "character varying", "character", "json", "jsonb", "xml":/,
      'looksSensitive no longer covers the six text types the built in rules empty an ' +
        'unclassified column of. The terms page says a project with no rules file still gets the ' +
        'built in set, and this is what that set acts on.',
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
