#!/usr/bin/env node
// Every operator page, at a phone width and a laptop width, signed in.
//
// WHY THE DEVTOOLS PROTOCOL AND NOT `headless_shell --screenshot=out.png URL`.
// The one-shot flag works and it cannot do the two things that make these
// pictures worth taking. It cannot carry a cookie, so every shot would be the
// sign-in screen, which is the exact failure this harness exists to prevent:
// twenty two identical pictures reported as a browsable portal. And it cannot
// measure anything, so horizontal overflow at 320, which is an automatic fail
// in this project, would still be something a person has to notice by eye.
//
// So the browser is started once with a debugging port, a session cookie is set
// on it, and each page is navigated, measured and captured over the protocol.
// There is no dependency to install: Node has had a WebSocket client built in
// since 22, and the protocol is JSON over that socket.
//
// THE ROUTES ARE READ, NEVER TYPED. They come out of console/lib/admin-nav.ts,
// which is the file the portal's own navigation is built from, so a section
// added by any lane is screenshotted the next time this runs and a list here
// cannot go stale. When the working tree has no such file, which is the case on
// main until the shell lands, it is read from origin/w-admin-shell instead.

import { spawn } from 'node:child_process'
import { execFileSync } from 'node:child_process'
import { mkdir, readFile, readdir, rm, writeFile } from 'node:fs/promises'
import { existsSync } from 'node:fs'
import path from 'node:path'
import os from 'node:os'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const root = path.resolve(here, '..', '..')

const base = process.env.AF_PREVIEW_URL ?? 'http://127.0.0.1:8100'
const outDir = process.env.AF_PREVIEW_SHOTS ?? path.join(root, '.preview', 'shots')
const widths = (process.env.AF_PREVIEW_WIDTHS ?? '320,1440').split(',').map((w) => Number(w.trim()))
const email = process.env.AF_PREVIEW_EMAIL ?? 'operator@preview.local'
const password = process.env.AF_PREVIEW_PASSWORD ?? 'preview-only-not-a-secret'

// ---------------------------------------------------------------------------
// The routes
// ---------------------------------------------------------------------------

/**
 * Every `href` the portal's navigation declares.
 *
 * A regex over the source rather than an import, because admin-nav.ts imports
 * a React component for each icon: importing it would mean bundling the
 * console to list its routes. The shape being matched is the one the file's own
 * type demands, so a new entry is found the moment it is added.
 */
function routesFrom(source) {
  const found = [...source.matchAll(/label:\s*"([^"]+)",\s*\n\s*href:\s*"(\/admin[^"]*)"/g)].map(
    (m) => ({ label: m[1], href: m[2] }),
  )
  const seen = new Set()
  return found.filter((item) => (seen.has(item.href) ? false : seen.add(item.href)))
}

async function readNav() {
  const local = path.join(root, 'console', 'lib', 'admin-nav.ts')
  if (existsSync(local)) {
    return { where: 'console/lib/admin-nav.ts', source: await readFile(local, 'utf8'), local: true }
  }
  // The shell has not landed on this branch. Read the same file from the branch
  // that owns it rather than falling back to a hand written list, which is the
  // copy that would go stale.
  const ref = process.env.AF_PREVIEW_NAV_REF ?? 'origin/w-admin-shell'
  const source = execFileSync('git', ['show', `${ref}:console/lib/admin-nav.ts`], {
    cwd: root,
    encoding: 'utf8',
    maxBuffer: 8 * 1024 * 1024,
  })
  return { where: `${ref}:console/lib/admin-nav.ts`, source, local: false }
}

// ---------------------------------------------------------------------------
// The browser
// ---------------------------------------------------------------------------

/** The headless Chromium already on this machine, newest build first. */
async function findBrowser() {
  if (process.env.AF_PREVIEW_CHROME) return process.env.AF_PREVIEW_CHROME
  const cache = path.join(os.homedir(), 'Library', 'Caches', 'ms-playwright')
  let entries = []
  try {
    entries = await readdir(cache)
  } catch {
    throw new Error(`no browser: set AF_PREVIEW_CHROME, or install one under ${cache}`)
  }
  const builds = entries
    .filter((name) => name.startsWith('chromium_headless_shell-'))
    .sort((a, b) => Number(b.split('-')[1]) - Number(a.split('-')[1]))
  for (const build of builds) {
    const candidate = path.join(cache, build, 'chrome-mac', 'headless_shell')
    if (existsSync(candidate)) return candidate
  }
  throw new Error(`no headless_shell under ${cache}: set AF_PREVIEW_CHROME to one`)
}

/** A tiny DevTools client. Request in, response out, events to listeners. */
function connect(url) {
  return new Promise((resolve, reject) => {
    const socket = new WebSocket(url)
    const pending = new Map()
    const listeners = new Set()
    let nextId = 1

    socket.addEventListener('message', (event) => {
      const message = JSON.parse(event.data)
      if (message.id !== undefined) {
        const waiting = pending.get(message.id)
        if (!waiting) return
        pending.delete(message.id)
        if (message.error) waiting.reject(new Error(`${message.error.message} (${JSON.stringify(message.error)})`))
        else waiting.resolve(message.result)
        return
      }
      for (const listener of listeners) listener(message)
    })
    socket.addEventListener('error', () => reject(new Error(`the browser socket failed: ${url}`)))
    socket.addEventListener('open', () =>
      resolve({
        send(method, params, sessionId) {
          const id = nextId++
          const frame = { id, method, params: params ?? {} }
          if (sessionId) frame.sessionId = sessionId
          socket.send(JSON.stringify(frame))
          return new Promise((ok, bad) => pending.set(id, { resolve: ok, reject: bad }))
        },
        on(listener) {
          listeners.add(listener)
          return () => listeners.delete(listener)
        },
        close: () => socket.close(),
      }),
    )
  })
}

/** Waits for one protocol event, or gives up. Never hangs the run. */
function waitFor(client, method, sessionId, timeoutMs) {
  return new Promise((resolve) => {
    const timer = setTimeout(() => {
      off()
      resolve(false)
    }, timeoutMs)
    const off = client.on((message) => {
      if (message.method !== method) return
      if (sessionId && message.sessionId !== sessionId) return
      clearTimeout(timer)
      off()
      resolve(true)
    })
  })
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

/**
 * How many requests this page has in flight, and how long it has had none.
 *
 * The only honest answer to "has this page finished loading" for a console that
 * fetches its rows after it paints. Counted from the protocol's own events
 * rather than from a timer.
 */
function watchTraffic(client, sessionId) {
  let inFlight = 0
  let idleSince = Date.now()
  client.on((message) => {
    if (message.sessionId !== sessionId) return
    if (message.method === 'Network.requestWillBeSent') {
      inFlight += 1
      return
    }
    if (
      message.method === 'Network.loadingFinished' ||
      message.method === 'Network.loadingFailed'
    ) {
      inFlight = Math.max(0, inFlight - 1)
      if (inFlight === 0) idleSince = Date.now()
    }
  })
  return {
    reset() {
      inFlight = 0
      idleSince = Date.now()
    },
    quiet(forMs) {
      return inFlight === 0 && Date.now() - idleSince >= forMs
    },
  }
}

// ---------------------------------------------------------------------------
// The session
// ---------------------------------------------------------------------------

/**
 * Signs the operator in over HTTP and returns the cookie to give the browser.
 *
 * Over HTTP rather than by driving the sign-in form, because the form is one of
 * the things the lanes are still building and a screenshot run that breaks when
 * somebody renames a field is a screenshot run nobody keeps using.
 */
async function signIn() {
  const res = await fetch(`${base}/v1/admin/signin`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ email, password }),
  })
  if (!res.ok) {
    throw new Error(
      `the operator could not sign in at ${base}: ${res.status} ${await res.text()}. ` +
        'Run tools/preview/up.sh first.',
    )
  }
  const header = res.headers.getSetCookie?.()[0] ?? res.headers.get('set-cookie')
  if (!header) throw new Error('signing in returned no cookie')
  const [name, ...rest] = header.split(';')[0].split('=')
  return { name, value: rest.join('=') }
}

// ---------------------------------------------------------------------------
// One page
// ---------------------------------------------------------------------------

/**
 * What the page is showing, measured in the page rather than guessed from it.
 *
 * `label` is the section's own name out of admin-nav.ts, and asking whether the
 * page rendered it is the strongest signal available that this is the section
 * and not something standing in for it. admin-nav.ts calls the labels the
 * specification and every screen reads them from that list, so a page missing
 * its own is a page that did not get as far as naming itself.
 */
const measureFor = (label) => `(() => {
  const LABEL = ${JSON.stringify(label)};
  const doc = document.documentElement;
  const viewport = window.innerWidth;
  const widest = [];
  for (const el of document.querySelectorAll('body *')) {
    const box = el.getBoundingClientRect();
    if (box.width === 0 && box.height === 0) continue;
    const right = box.right + window.scrollX;
    if (right > viewport + 1) {
      widest.push({
        right: Math.round(right),
        tag: el.tagName.toLowerCase(),
        cls: (typeof el.className === 'string' ? el.className : '').slice(0, 80),
      });
    }
  }
  widest.sort((a, b) => b.right - a.right);
  const text = (document.body.innerText || '').replace(/\\s+/g, ' ').trim();
  return {
    scrollWidth: Math.max(doc.scrollWidth, document.body.scrollWidth),
    viewport,
    height: Math.max(doc.scrollHeight, document.body.scrollHeight),
    text: text.slice(0, 400),
    words: text.length,
    titled: text.includes(LABEL),
    signInForm: !!document.querySelector('input[type=password]'),
    // The console announces a wait with role="status" and the word Loading,
    // for a screen reader. Counting that is how this script knows it is looking
    // at a skeleton rather than at a table with nothing in it, and it reads the
    // accessible announcement rather than a class name so a restyle does not
    // silently switch the check off.
    skeletons: [...document.querySelectorAll('[role=status]')].filter((el) =>
      /^\\s*loading/i.test(el.textContent || ''),
    ).length,
    offenders: widest.slice(0, 3),
  };
})()`

/**
 * What the control plane says about this route, asked before the browser goes
 * there.
 *
 * THIS IS THE CHECK THAT MAKES THE RUN MEAN ANYTHING. The export writes one
 * file per route, so a section nobody has built yet is a 404 that the console
 * answers with its own "that page is not here" page: a real render, at the
 * right width, with no overflow, which every measurement below calls fine. The
 * first run of this script reported forty six healthy screenshots and twenty
 * two of them were that page. A status code is the one signal that separates a
 * page from its absence, and it costs one request.
 */
async function statusOf(route, cookie) {
  const res = await fetch(`${base}${route}`, {
    headers: { cookie: `${cookie.name}=${cookie.value}` },
    redirect: 'manual',
  })
  await res.arrayBuffer()
  return res.status
}

/** The tallest page this captures whole. See the note in shoot(). */
const MAX_HEIGHT = 6000

/**
 * Fewer characters than this and the page did not render.
 *
 * Measured rather than picked: the thinnest real page in the portal is a
 * section nobody has built yet at 320, which carries its rail, its heading, its
 * one sentence summary and the words that say it is planned, and that is a
 * little over three hundred characters. A page caught before React mounts has
 * thirty.
 */
const THIN = 120

async function shoot(client, sessionId, traffic, item, width) {
  const route = item.href
  const measure = measureFor(item.label)
  await client.send(
    'Emulation.setDeviceMetricsOverride',
    { width, height: 900, deviceScaleFactor: 1, mobile: width < 700 },
    sessionId,
  )
  const loaded = waitFor(client, 'Page.loadEventFired', sessionId, 20_000)
  traffic.reset()
  await client.send('Page.navigate', { url: `${base}${route}` }, sessionId)
  await loaded

  // WAITING FOR THE FETCHES, NOT FOR THE TEXT TO HOLD STILL. Every page here
  // renders its heading, then a skeleton, then its rows, and the heading alone
  // is text that holds still: an earlier version of this settled on that and
  // captured the skeleton. The picture looked like a finished page with an
  // empty table, which is the single most misleading thing this script could
  // produce, because it is indistinguishable from a query that returns nothing.
  //
  // So the condition is the network: no request in flight, held for a moment,
  // and the measured text stable across two polls on top of that.
  let previous = -1
  let stable = 0
  let measured = null
  for (let attempt = 0; attempt < 60; attempt += 1) {
    await sleep(150)
    const { result } = await client.send(
      'Runtime.evaluate',
      { expression: measure, returnByValue: true },
      sessionId,
    )
    measured = result.value
    if (!traffic.quiet(400)) {
      stable = 0
      previous = measured.words
      continue
    }
    stable = measured.words === previous ? stable + 1 : 0
    previous = measured.words
    if (measured.words > 0 && stable >= 1) break
  }

  // Grown to the page's own height before the capture rather than captured
  // beyond the viewport.
  //
  // captureBeyondViewport paints a tall image and leaves every `position:
  // fixed` element at the height it had, so the portal's rail stopped a third
  // of the way down a long page and read as a rail that had been cut off.
  // Resizing means the rail is laid out against the height being photographed,
  // which is what a person scrolling actually sees.
  const height = Math.min(Math.max(measured?.height ?? 900, 900), MAX_HEIGHT)
  const truncated = (measured?.height ?? 0) > MAX_HEIGHT
  await client.send(
    'Emulation.setDeviceMetricsOverride',
    { width, height, deviceScaleFactor: 1, mobile: width < 700 },
    sessionId,
  )
  await sleep(150)

  const { data } = await client.send('Page.captureScreenshot', { format: 'png' }, sessionId)
  const name = `${route === '/admin' ? 'admin-overview' : route.replace(/^\//, '').replace(/\//g, '-')}@${width}.png`
  await writeFile(path.join(outDir, name), Buffer.from(data, 'base64'))
  return { name, truncated, ...measured }
}

// ---------------------------------------------------------------------------
// The run
// ---------------------------------------------------------------------------

async function main() {
  const nav = await readNav()
  const routes = routesFrom(nav.source)
  if (routes.length === 0) throw new Error(`no /admin routes found in ${nav.where}`)
  console.log(`${routes.length} operator routes, read from ${nav.where}`)

  const cookie = await signIn()
  console.log(`signed in as ${email}, carrying ${cookie.name}`)

  await rm(outDir, { recursive: true, force: true })
  await mkdir(outDir, { recursive: true })

  // Routes this build does not have. Held apart from the failures above because
  // they are one sentence rather than two per width, and because the fix is a
  // different person's: a missing page is a page nobody has built yet.
  const missingRoutes = new Set()
  /** Routes that produced at least one problem, so the summary can count the
   *  ones that did not rather than claiming every present route was fine. */
  const troubled = new Set()

  const browser = await findBrowser()
  const profile = path.join(os.tmpdir(), `af-preview-chrome-${process.pid}`)
  const port = 9222 + (process.pid % 500)
  const child = spawn(
    browser,
    [
      '--headless',
      '--disable-gpu',
      '--no-sandbox',
      '--hide-scrollbars',
      '--force-color-profile=srgb',
      `--user-data-dir=${profile}`,
      `--remote-debugging-port=${port}`,
      'about:blank',
    ],
    { stdio: ['ignore', 'ignore', 'pipe'] },
  )

  let client
  const failures = []
  try {
    let version = null
    for (let attempt = 0; attempt < 100; attempt += 1) {
      await sleep(100)
      try {
        version = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json()
        break
      } catch {
        // Not listening yet. The loop is the wait.
      }
    }
    if (!version) throw new Error(`the browser never opened a debugging port on ${port}`)

    client = await connect(version.webSocketDebuggerUrl)
    const { targetId } = await client.send('Target.createTarget', { url: 'about:blank' })
    const { sessionId } = await client.send('Target.attachToTarget', { targetId, flatten: true })
    await client.send('Page.enable', {}, sessionId)
    await client.send('Network.enable', {}, sessionId)
    const traffic = watchTraffic(client, sessionId)
    await client.send(
      'Network.setCookie',
      {
        name: cookie.name,
        value: cookie.value,
        domain: new URL(base).hostname,
        path: '/',
        httpOnly: true,
        sameSite: 'Lax',
      },
      sessionId,
    )

    const rows = []
    for (const item of routes) {
      const route = item.href
      const status = await statusOf(route, cookie)
      for (const width of widths) {
        const shot = await shoot(client, sessionId, traffic, item, width)
        rows.push({ route, width, status, ...shot })

        const missing = status !== 200
        const overflow = shot.scrollWidth > shot.viewport
        // A sign-in screen is a failed shot however good the PNG looks, and the
        // password field is the way to know: the portal's own pages have none.
        const signedOut =
          shot.signInForm || /the control plane did not answer|control plane answered/i.test(shot.text)
        const skeleton = !missing && shot.skeletons > 0
        // The positive assertion, and the one that is hard to pass by accident.
        // Everything else here can only notice a page going wrong in a way
        // somebody predicted; this one asks whether the section rendered at all.
        //
        // ONLY WHEN THE NAVIGATION CAME FROM THIS CHECKOUT. Read from another
        // branch, the labels belong to that branch and asserting them here
        // asserts the wrong thing: on main, whose /admin predates the shell,
        // this reported the front door as unrendered while it was plainly
        // rendering. A borrowed navigation gets the weaker check, which is a
        // floor rather than a claim about what the page should say.
        const unnamed = !missing && (nav.local ? !shot.titled : shot.words < THIN)

        if (unnamed || skeleton || overflow || (signedOut && !missing)) troubled.add(route)
        if (unnamed) {
          failures.push(
            nav.local
              ? `${route} at ${width}: the page never rendered its own name, "${item.label}". ` +
                `It has ${shot.words} characters on it.`
              : `${route} at ${width}: only ${shot.words} characters rendered, which is less than ` +
                'an empty section of this portal has.',
          )
        }
        if (skeleton) {
          failures.push(
            `${route} at ${width}: captured while ${shot.skeletons} placeholders were still loading`,
          )
        }
        if (missing) {
          missingRoutes.add(route)
        } else if (signedOut) {
          failures.push(`${route} at ${width}: the browser is not signed in, the page is asking for a password`)
        }
        if (overflow) {
          failures.push(
            `${route} at ${width}: scrollWidth ${shot.scrollWidth} against a ${shot.viewport} viewport` +
              (shot.offenders.length
                ? `, widest is <${shot.offenders[0].tag} class="${shot.offenders[0].cls}"> reaching ${shot.offenders[0].right}`
                : ''),
          )
        }

        const mark = missing
          ? `HTTP ${status}`
          : overflow
            ? 'OVERFLOWS'
            : signedOut
              ? 'SIGNED OUT'
              : skeleton
                ? 'LOADING'
                : unnamed
                  ? 'UNNAMED'
                  : 'ok'
        console.log(
          `  ${mark.padEnd(10)} ${route.padEnd(38)} ${String(width).padStart(4)}  ` +
            `${shot.scrollWidth}x${shot.height}  ${shot.words} characters`,
        )
      }
    }

    await writeFile(path.join(outDir, 'shots.json'), `${JSON.stringify(rows, null, 2)}\n`)
    console.log(`\n${rows.length} screenshots in ${outDir}`)
  } finally {
    client?.close()
    child.kill()
    await rm(profile, { recursive: true, force: true })
  }

  if (missingRoutes.size > 0) {
    console.log(
      `\n${missingRoutes.size} of ${routes.length} routes are not in this build. ` +
        'Their pictures are the console\'s "that page is not here" screen, not a section:',
    )
    for (const route of missingRoutes) console.log(`  ${route}`)
    console.log('  That is expected on a branch where the section has not landed yet.')
  }
  if (failures.length > 0) {
    console.log('\nProblems, which are the point of running this:')
    for (const failure of failures) console.log(`  ${failure}`)
  }
  const built = routes.length - missingRoutes.size
  const clean = built - troubled.size
  console.log(
    `\n${built} of ${routes.length} routes are in this build. ` +
      `${clean} of those rendered cleanly at ${widths.join(' and ')}, ` +
      `${failures.length} problems on ${troubled.size}.`,
  )
  if (failures.length > 0 || (missingRoutes.size > 0 && process.env.AF_PREVIEW_ALLOW_MISSING !== '1')) {
    process.exit(1)
  }
}

try {
  await main()
} catch (err) {
  console.error(err instanceof Error ? (err.stack ?? err.message) : String(err))
  process.exit(1)
}
