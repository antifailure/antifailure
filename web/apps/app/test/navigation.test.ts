// A redirect is not a failure.
//
// `redirect()` and `notFound()` work by throwing an exception the framework
// recognises on its way out of the render. Every page here wraps its data
// fetching in a try/catch so that an unreachable API becomes a readable
// message rather than a stack trace, and the same catch swallowed the
// navigation: a signed out visitor asking for any page got a full page error
// reading "The control plane did not answer. Error: NEXT_REDIRECT", and could
// not reach the sign-in page from anywhere in the application.
//
// It was found by an agent, which reported a page with nothing on it to press.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { isNavigation } from '../lib/navigation.ts'

test('a thrown redirect is recognised as navigation', () => {
  assert.equal(isNavigation({ digest: 'NEXT_REDIRECT;replace;/login;307;' }), true)
  assert.equal(isNavigation({ digest: 'NEXT_NOT_FOUND' }), true)
})

test('a real failure is not', () => {
  assert.equal(isNavigation(new Error('the control plane did not answer')), false)
  assert.equal(isNavigation({ digest: 'something-else' }), false)
  assert.equal(isNavigation({ digest: 42 }), false)
  assert.equal(isNavigation(null), false)
  assert.equal(isNavigation(undefined), false)
  assert.equal(isNavigation('NEXT_REDIRECT'), false)
})

// The rule, enforced rather than remembered: a page that catches has to let a
// navigation through. Without this the next catch somebody adds reintroduces
// the same bug, and it is invisible until a signed out person opens the page.
test('every page that catches lets a navigation through', () => {
  const pages: string[] = []
  const walk = (dir: string) => {
    for (const entry of readdirSync(dir)) {
      const full = join(dir, entry)
      if (statSync(full).isDirectory()) walk(full)
      else if (entry === 'page.tsx') pages.push(full)
    }
  }
  walk(join(import.meta.dirname, '..', 'app'))
  assert.ok(pages.length >= 5, `found only ${pages.length} pages`)

  for (const page of pages) {
    const source = readFileSync(page, 'utf8')
    const catches = source.match(/\} catch \(err\) \{/g)?.length ?? 0
    if (catches === 0) continue
    const rethrows = source.match(/if \(isNavigation\(err\)\) throw err;/g)?.length ?? 0
    assert.equal(
      rethrows,
      catches,
      `${page} has ${catches} catch blocks and ${rethrows} that let a navigation through`,
    )
  }
})
