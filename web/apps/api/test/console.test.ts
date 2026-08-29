// The console's escaping, which is the only part of it that can be a security
// bug rather than an ugly page.
//
// Almost every value these pages render arrived from somewhere else: a branch
// name, a repository name, a client label somebody passed to af login. The
// templates escape by default and the only way to emit markup is raw(), so
// these tests hold that rule rather than testing the layout, which a screenshot
// checks better than an assertion can.

import { test, describe } from 'node:test'
import assert from 'node:assert/strict'
import { html, raw, escape, chip, when, empty } from '../src/console/layout.ts'
import { devicePage, signInPage } from '../src/console/pages.ts'

const viewer = {
  userId: '00000000-0000-0000-0000-000000000000',
  label: 'Somebody',
  organization: 'antifailure',
  role: 'admin',
  csrfToken: 'csrf-value',
}

describe('escaping', () => {
  test('an interpolated value cannot open a tag', () => {
    const nasty = '<script>alert(1)</script>'
    const out = String(html`<p>${nasty}</p>`)
    assert.equal(out, '<p>&lt;script&gt;alert(1)&lt;/script&gt;</p>')
    assert.doesNotMatch(out, /<script>/)
  })

  test('an interpolated value cannot break out of an attribute', () => {
    // The shape that matters most: a value inside quotes closing them and
    // adding an event handler.
    //
    // The assertion is about the QUOTE, not about the word. `onload=` survives
    // as text inside the attribute value and is inert there; asserting its
    // absence would be asserting the wrong thing, and it would pass for an
    // escaper that only stripped that one word. What makes the attack fail is
    // that the value can no longer close the attribute.
    const nasty = '" onload="alert(1)'
    const out = String(html`<img alt="${nasty}">`)
    assert.equal(out, '<img alt="&quot; onload=&quot;alert(1)">')
    // Exactly two real quotes in the output: the ones this template wrote.
    assert.equal((out.match(/"/g) ?? []).length, 2)
  })

  test('single quotes are escaped too, for single-quoted attributes', () => {
    assert.match(String(html`<a title='${"it's"}'>`), /&#39;/)
  })

  test('arrays are escaped element by element', () => {
    const out = String(html`<ul>${['<b>a</b>', '<i>b</i>']}</ul>`)
    assert.doesNotMatch(out, /<b>|<i>/)
  })

  test('raw is the only way through, and it is deliberate', () => {
    assert.equal(String(html`${raw('<b>bold</b>')}`), '<b>bold</b>')
  })

  test('null and undefined render as nothing, not as the words', () => {
    // A page reading "undefined" where a branch name should be is the most
    // common way a template says "this data is missing" without meaning to.
    assert.equal(escape(null), '')
    assert.equal(escape(undefined), '')
    assert.equal(String(html`<td>${null}</td>`), '<td></td>')
  })
})

describe('pages', () => {
  test('a hostile client label is escaped on the approval screen', () => {
    // af login sends this, and it is whatever the machine's hostname and user
    // are. On a machine somebody else named, that is attacker-controlled.
    const out = String(
      devicePage({
        viewer,
        code: 'BCDF-GHJK',
        pending: {
          clientLabel: '<img src=x onerror=alert(1)>',
          scopes: ['environments.view'],
          expiresAt: new Date('2026-08-28T05:00:00Z'),
        },
      }),
    )
    assert.doesNotMatch(out, /<img src=x/)
    assert.match(out, /&lt;img src=x/)
  })

  test('every page declares a charset, a viewport and a title', () => {
    for (const out of [String(signInPage()), String(devicePage({ viewer, code: '', pending: null }))]) {
      assert.match(out, /<meta charset="utf-8"/)
      assert.match(out, /name="viewport"/)
      assert.match(out, /<title>[^<]+ — Antifailure<\/title>/)
      // Not indexed: this is somebody's tenant, not a marketing page.
      assert.match(out, /name="robots" content="noindex"/)
    }
  })

  test('every page has a skip link, because keyboard users land on the rail', () => {
    assert.match(String(signInPage()), /class="skip" href="#main"/)
  })

  test('the sign-in page has no session-bearing content', () => {
    // It is rendered for somebody with no session, so it must not carry a CSRF
    // token or a name. A page that did would be a page cached for one person
    // and served to another.
    const out = String(signInPage())
    assert.doesNotMatch(out, /csrf/i)
  })

  test('the approval form carries the CSRF token', () => {
    const out = String(
      devicePage({
        viewer,
        code: 'BCDF-GHJK',
        pending: { clientLabel: 'a laptop', scopes: [], expiresAt: new Date() },
      }),
    )
    assert.match(out, /name="csrf" value="csrf-value"/)
  })
})

describe('state is never colour alone', () => {
  test('a chip carries the word as well as the tone', () => {
    // About one man in twelve cannot tell the red one from the green one.
    for (const s of ['running', 'failed', 'flaky', 'torn_down']) {
      assert.match(String(chip(s)), new RegExp(`>${s}<`))
    }
  })

  test('a missing timestamp says never rather than rendering blank', () => {
    assert.match(String(when(null)), />never</)
  })

  test('an empty state says what it is and why it is empty', () => {
    const out = String(empty('No environments yet', 'An environment appears here when af up runs.'))
    assert.match(out, /No environments yet/)
    assert.match(out, /appears here/)
  })
})
