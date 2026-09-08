// Every sweeper in the server has a caller inside a timer.
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
// WHY IT NO LONGER LOOKS ONLY UNDER src/auth. It used to, and the fourth table
// to need a sweeper was engine_tokens, which is not on the sign-in path and
// whose sweeper therefore lives in src/tokens.ts. A gate that reads one
// directory would have watched a new sweeper be written with no caller and
// said nothing, which is the exact defect it was built for, arrived at by
// being too narrow rather than by being absent. So it reads every file under
// src, and it asks the question the name of this file asks.
//
// A test of the sweep itself cannot catch this. auth.test.ts calls
// sweepOAuthStates directly and would stay green for ever after somebody
// deleted the line in boot.ts that calls it in production. So this asks the
// other half of the question, which is the half nobody was asking.
//
// WHAT IT DELIBERATELY DOES NOT CLAIM. It reads text. It proves that the call
// is written inside one of the two intervals boot.ts starts, not that either
// interval fires, not that the pool it is handed is the live one, and not that
// the DELETE reaches a row. The first is unobservable in under five minutes
// and the third is what auth.test.ts, device.test.ts, emailsignin.test.ts and
// workflowtokensweep.test.ts prove against a real database. This is the
// structural half and it is paired with those, not a substitute for them.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { readdir, readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const srcDir = path.join(here, '..', 'src')
// boot.ts, which is where the two intervals now live.
//
// It was main.ts until the entry point was split so that a second edition could
// register its routes without a second copy of the configuration. The intervals
// moved with the body and nothing about them changed, and this path moved with
// them. Naming boot.ts rather than main.ts is the point of the split: main.ts
// is now three lines and one call, and a gate pointed at it would read a file
// that starts no timers and report every sweeper in the tree as uncalled.
const bootPath = path.join(here, '..', 'src', 'boot.ts')

/**
 * The two intervals boot.ts starts, by the literals that bound each one.
 *
 * Two rather than one because they are not the same kind of work and boot.ts
 * says so at length: the housekeeping pass is about table size and runs every
 * five minutes, and the pull request lifecycle pass is about a check that never
 * concludes and runs every minute. A sweeper called inside either is called;
 * one called inside neither is not, however many times it appears in the file.
 */
const INTERVALS = [
  { opens: 'const housekeeping = setInterval(', closes: 'housekeeping.unref()' },
  { opens: 'const lifecycleSweep = setInterval(', closes: 'lifecycleSweep.unref()' },
]

/**
 * Every exported sweeper declared anywhere under src, with the file it is in.
 *
 * Read from the tree rather than listed here. A list would be a second place to
 * remember, and forgetting to add to it is the same failure one level up: a
 * sweeper nothing calls, missed by a check nothing told about it.
 */
async function sweepers(): Promise<{ name: string; file: string }[]> {
  const found: { name: string; file: string }[] = []
  const walk = async (dir: string): Promise<void> => {
    for (const entry of await readdir(dir, { withFileTypes: true })) {
      const full = path.join(dir, entry.name)
      if (entry.isDirectory()) {
        await walk(full)
        continue
      }
      if (!entry.name.endsWith('.ts') || entry.name.endsWith('.test.ts')) continue
      const source = await readFile(full, 'utf8')
      for (const m of source.matchAll(/^export (?:async )?function (sweep[A-Za-z0-9_]*)\s*\(/gm)) {
        found.push({ name: m[1]!, file: path.relative(srcDir, full) })
      }
    }
  }
  await walk(srcDir)
  return found.sort((a, b) => a.name.localeCompare(b.name))
}

/**
 * Every file under src that enters the expiry sweeper role.
 *
 * The scanner above matches on a NAME, and a name is a convention rather than a
 * mechanism: renaming sweepExpiredWorkflowTokens to removeExpiredWorkflowTokens
 * makes it invisible to this gate, and a mutation proved that it did so
 * silently. This asks the same question about the one thing a sweeper cannot do
 * without: the pool scope that enters antifailure_sweeper. A file that reaches
 * for that role and exports nothing this gate recognises is a sweeper the gate
 * cannot see, which is the state it exists to refuse.
 *
 * It does not cover a sweeper that deletes through some other scope, and there
 * is no way for a text scanner to. What is claimed here is exactly this: the
 * role has no user the name check has missed.
 */
async function filesEnteringTheSweeperRole(): Promise<string[]> {
  const found: string[] = []
  const walk = async (dir: string): Promise<void> => {
    for (const entry of await readdir(dir, { withFileTypes: true })) {
      const full = path.join(dir, entry.name)
      if (entry.isDirectory()) {
        await walk(full)
        continue
      }
      if (!entry.name.endsWith('.ts') || entry.name.endsWith('.test.ts')) continue
      if ((await readFile(full, 'utf8')).includes('withExpirySweeper(')) {
        found.push(path.relative(srcDir, full))
      }
    }
  }
  await walk(srcDir)
  return found.sort()
}

/** The text of each interval body, and everything that is in neither. */
async function split(): Promise<{ inside: string; outside: string }> {
  const boot = await readFile(bootPath, 'utf8')
  let inside = ''
  let outside = boot
  for (const { opens, closes } of INTERVALS) {
    const open = outside.indexOf(opens)
    assert.notEqual(open, -1, `src/boot.ts no longer contains ${JSON.stringify(opens)}`)
    const close = outside.indexOf(closes, open)
    assert.ok(close > open, `src/boot.ts no longer contains ${JSON.stringify(closes)} after it`)
    inside += outside.slice(open, close)
    outside = outside.slice(0, open) + outside.slice(close)
  }
  // An import names a sweeper without calling it, and every sweeper has one, so
  // leaving the import lines in would make the negative control below fail on
  // every sweeper in the file including the ones that are wired correctly.
  outside = outside
    .split('\n')
    .filter((line) => !line.startsWith('import '))
    .join('\n')
  return { inside, outside }
}

describe('the housekeeping interval', () => {
  it('finds sweepers to check, so a green run is not an empty scan', async () => {
    // A scanner that matched nothing would pass every assertion below while
    // checking nothing at all, which is the shape of gate failure this
    // repository keeps finding in its own instruments. Six is the count today
    // and four of them are the ones this file was written for; the assertion
    // is that the pattern still matches source that has not changed shape, not
    // that the number is frozen.
    const found = await sweepers()
    assert.ok(
      found.length >= 6,
      `only ${found.length} sweeper(s) matched under src, so either they were renamed ` +
        `or this scanner stopped recognising them: ${JSON.stringify(found)}`,
    )
    // The widening is the point of this version of the file, so it is asserted
    // rather than assumed. A scanner that had quietly gone back to reading one
    // directory would still satisfy the count above.
    assert.ok(
      found.some((s) => !s.file.startsWith('auth/')),
      `every sweeper found is under src/auth, so this scanner cannot tell whether it is ` +
        `reading the whole tree or only the directory it used to read: ${JSON.stringify(found)}`,
    )
  })

  it('is still where this test looks for it', async () => {
    // The instrument has to say when it could not check rather than pass. If
    // any boundary is renamed, everything below would silently look at an
    // empty string and agree with itself. split() asserts each one.
    const { inside } = await split()
    assert.ok(inside.length > 0, 'neither interval body could be read out of src/boot.ts')
  })

  it('calls every sweeper declared under src', async () => {
    const { inside } = await split()
    const uncalled = (await sweepers()).filter(({ name }) => !inside.includes(`${name}(`))
    assert.deepEqual(
      uncalled.map((s) => `${s.name} (src/${s.file})`),
      [],
      `these are exported as sweepers and neither interval in boot.ts calls them.\n` +
        `A sweeper with no caller is not a small bug: the table it names grows for the life of ` +
        `the process and the code reads as though it does not.`,
    )
  })

  it('can see every sweeper that enters the sweeper role, whatever it is called', async () => {
    // The name check above is a convention. This one is the mechanism, and it
    // was added because a mutation renamed a sweeper out of the convention and
    // this file stayed green through it.
    const declared = new Set((await sweepers()).map((s) => s.file))
    const unseen = (await filesEnteringTheSweeperRole()).filter((f) => !declared.has(f))
    assert.deepEqual(
      unseen,
      [],
      `these files enter antifailure_sweeper and export nothing this gate recognises as a ` +
        `sweeper, so nothing here checks that they are ever called.\n` +
        `Name the exported function sweepSomething, or this gate is blind to it.`,
    )
    // And the scanner has to be finding the role's users at all, or the
    // assertion above is an empty set compared against an empty set.
    assert.ok(
      (await filesEnteringTheSweeperRole()).length >= 2,
      'no file under src enters antifailure_sweeper, so this scanner stopped recognising the scope',
    )
  })

  it('does not count a call written outside the intervals', async () => {
    // The negative control for the assertion above, and the reason it slices
    // boot.ts rather than searching the whole file. A call somewhere else in
    // the module runs once at startup at best and never at worst, and a check
    // that accepted one would pass on exactly the arrangement it exists to
    // refuse.
    const { inside, outside } = await split()
    for (const { name } of await sweepers()) {
      assert.ok(
        !outside.includes(`${name}(`),
        `${name} is called outside both intervals as well; a second caller means ` +
          `the assertion above can pass on a sweep that runs once or not at all`,
      )
      assert.ok(inside.includes(`${name}(`), `${name} is not called inside either interval`)
    }
  })
})
