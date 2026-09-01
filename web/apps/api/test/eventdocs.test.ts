// The App setup runbook, against the events the code actually acts on.
//
// The runbook tells whoever creates the GitHub App which events to subscribe
// to, and an event nobody subscribes to is not an error anywhere: GitHub simply
// never sends it, the handler for it never runs, and the feature it powers is
// quietly missing. This list has already been wrong once. It named Check run
// and not Check suite, directly above a paragraph explaining that subscribing
// to only one of the two leaves the other Re-run button doing nothing at all,
// so following the list literally produced exactly the defect the prose warned
// about.
//
// So the list is checked against the switch rather than against attention.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const lifecyclePath = path.join(here, '..', 'src', 'github', 'lifecycle.ts')
const runbookPath = path.join(
  here, '..', '..', '..', '..',
  'docs', 'src', 'content', 'docs', 'self-hosting', 'production.md',
)

/** The events handleLifecycleDelivery dispatches on, read from its switch. */
async function handledEvents(): Promise<string[]> {
  const source = await readFile(lifecyclePath, 'utf8')
  const start = source.indexOf('export async function handleLifecycleDelivery')
  assert.notEqual(start, -1, 'handleLifecycleDelivery is gone or renamed, so this gate reads nothing')
  const body = source.slice(start, source.indexOf('\n}', start))
  const events = [...body.matchAll(/^\s*case '([a-z_]+)':/gm)].map((m) => m[1]!)
  // A parse that finds nothing must fail here rather than pass every assertion
  // below by having nothing to assert.
  assert.ok(events.length >= 4, `parsed ${events.length} events from the switch, expected at least 4`)
  return events
}

/** The events the runbook tells somebody to subscribe to. */
async function subscribedEvents(): Promise<string[]> {
  const doc = await readFile(runbookPath, 'utf8')
  const start = doc.indexOf('Subscribe to events:')
  assert.notEqual(start, -1, 'the runbook no longer says "Subscribe to events:", so this gate reads nothing')
  const sentence = doc.slice(start, doc.indexOf('\n\n', start))
  const names = [...sentence.matchAll(/\*\*([^*]+)\*\*/g)].map((m) =>
    // "Check suite" is the name GitHub shows on the settings page; check_suite
    // is the name it puts in the header. The line break the paragraph wraps on
    // lands inside a bolded name, so newlines collapse before the join.
    m[1]!.replace(/\s+/g, ' ').trim().toLowerCase().replace(/ /g, '_'),
  )
  assert.ok(names.length >= 5, `parsed ${names.length} events from the runbook, expected at least 5`)
  return names
}

describe('the App setup runbook', () => {
  it('tells somebody to subscribe to every event the lifecycle acts on', async () => {
    const subscribed = new Set(await subscribedEvents())
    const missing = (await handledEvents()).filter((e) => !subscribed.has(e))
    assert.deepEqual(
      missing,
      [],
      `the lifecycle handles these events and the runbook does not ask for them:\n` +
        `  ${missing.join('\n  ')}\n` +
        `Nobody subscribes to them, so GitHub never sends them and the handler never runs.`,
    )
  })
})
