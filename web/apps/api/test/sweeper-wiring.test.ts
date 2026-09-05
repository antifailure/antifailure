// Every sweeper on the sign-in path has a caller.
//
// THE FAILURE THIS IS FOR. sweepDeviceAuthorizations was written, given a
// comment saying exactly what it kept under control, and called from nowhere,
// so device_authorizations grew for the life of every process that ever ran.
// sweepSessions had a caller and no reachable rows. sweepOAuthStates did not
// exist at all. Three tables on the unauthenticated sign-in path, three
// different ways of not being swept, and in every one of them the code read as
// a working feature: the function was there, it was documented, and nothing
// connected it to the process.
//
// A test of the sweep itself cannot catch this. auth.test.ts calls
// sweepOAuthStates directly and would stay green for ever after somebody
// deleted the line in main.ts that calls it in production. So this asks the
// other half of the question, which is the half nobody was asking.
//
// WHAT IT DELIBERATELY DOES NOT CLAIM. It reads text. It proves that the call
// is written inside the housekeeping interval, not that the interval fires,
// not that the pool it is handed is the live one, and not that the DELETE
// reaches a row. The first is unobservable in under five minutes and the third
// is what auth.test.ts, device.test.ts and emailsignin.test.ts prove against a
// real database. This is the structural half and it is paired with those, not
// a substitute for them.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { readdir, readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const authDir = path.join(here, '..', 'src', 'auth')
const mainPath = path.join(here, '..', 'src', 'main.ts')

/** The two literals that bound the housekeeping interval in main.ts. */
const OPENS = 'const housekeeping = setInterval('
const CLOSES = 'housekeeping.unref()'

/**
 * Every exported sweeper declared under src/auth, with the file it is in.
 *
 * Read from the directory rather than listed here. A list would be a second
 * place to remember, and forgetting to add to it is the same failure one level
 * up: a sweeper nothing calls, missed by a check nothing told about it.
 */
async function sweepersUnderAuth(): Promise<{ name: string; file: string }[]> {
  const found: { name: string; file: string }[] = []
  for (const entry of await readdir(authDir)) {
    if (!entry.endsWith('.ts') || entry.endsWith('.test.ts')) continue
    const source = await readFile(path.join(authDir, entry), 'utf8')
    for (const m of source.matchAll(/^export (?:async )?function (sweep[A-Za-z0-9_]*)\s*\(/gm)) {
      found.push({ name: m[1]!, file: entry })
    }
  }
  return found.sort((a, b) => a.name.localeCompare(b.name))
}

describe('the housekeeping interval', () => {
  it('finds sweepers to check, so a green run is not an empty scan', async () => {
    // A scanner that matched nothing would pass every assertion below while
    // checking nothing at all, which is the shape of gate failure this
    // repository keeps finding in its own instruments. Four is the count
    // today; the assertion is that the pattern still matches source that has
    // not changed shape, not that the number is frozen.
    const sweepers = await sweepersUnderAuth()
    assert.ok(
      sweepers.length >= 4,
      `only ${sweepers.length} sweeper(s) matched under src/auth, so either they were renamed ` +
        `or this scanner stopped recognising them: ${JSON.stringify(sweepers)}`,
    )
  })

  it('is still where this test looks for it', async () => {
    // The instrument has to say when it could not check rather than pass. If
    // either boundary is renamed, everything below would silently look at an
    // empty string and agree with itself.
    const main = await readFile(mainPath, 'utf8')
    const open = main.indexOf(OPENS)
    const close = main.indexOf(CLOSES, open === -1 ? 0 : open)
    assert.notEqual(open, -1, `src/main.ts no longer contains ${JSON.stringify(OPENS)}`)
    assert.ok(close > open, `src/main.ts no longer contains ${JSON.stringify(CLOSES)} after the interval`)
  })

  it('calls every sweeper declared under src/auth', async () => {
    const main = await readFile(mainPath, 'utf8')
    const open = main.indexOf(OPENS)
    const close = main.indexOf(CLOSES, open)
    const body = main.slice(open, close)

    const uncalled = (await sweepersUnderAuth()).filter(({ name }) => !body.includes(`${name}(`))
    assert.deepEqual(
      uncalled.map((s) => `${s.name} (src/auth/${s.file})`),
      [],
      `these are exported as sweepers and the housekeeping interval calls none of them.\n` +
        `A sweeper with no caller is not a small bug: the table it names grows for the life of ` +
        `the process and the code reads as though it does not.`,
    )
  })

  it('does not count a call written outside the interval', async () => {
    // The negative control for the assertion above, and the reason it slices
    // main.ts rather than searching the whole file. A call somewhere else in
    // the module runs once at startup at best and never at worst, and a check
    // that accepted one would pass on exactly the arrangement it exists to
    // refuse.
    const main = await readFile(mainPath, 'utf8')
    const open = main.indexOf(OPENS)
    const close = main.indexOf(CLOSES, open)
    const outside = main.slice(0, open) + main.slice(close)
    const body = main.slice(open, close)
    for (const { name } of await sweepersUnderAuth()) {
      // The import line names it too, so what is asserted is that removing the
      // interval's copy leaves no CALL behind anywhere else.
      assert.ok(
        !outside.includes(`${name}(pool`),
        `${name} is called outside the housekeeping interval as well; a second caller means ` +
          `the assertion above can pass on a sweep that runs once or not at all`,
      )
      assert.ok(body.includes(`${name}(`), `${name} is not called inside the interval`)
    }
  })
})
