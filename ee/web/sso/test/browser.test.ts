// The two browser rules the Keycloak end to end test relies on.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// CodeQL #4 and #8. The redirect loop stopped at the first location that
// started with https://antifailure.test, which is also how
// https://antifailure.test.evil gets treated as home: a prefix is not an origin.
// And decodeHtml turned &amp; into & before it decoded &lt;, so a form value
// that carried the text "&lt;b&gt;" came out as "<b>". Both are test helpers,
// and both are the kind of line that gets copied into code that ships.
import { describe, test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { decodeHtml, isOrigin } from './browser.ts'

describe('isOrigin', () => {
  test('the callback on the expected origin is home', () => {
    assert.equal(isOrigin('https://antifailure.test/sso/oidc/acme/callback?code=x', 'https://antifailure.test'), true)
  })
  test('a host that only starts with the expected one is not', () => {
    assert.equal(isOrigin('https://antifailure.test.evil/sso/oidc/acme/callback', 'https://antifailure.test'), false)
  })
  test('another port on the same host is not', () => {
    assert.equal(isOrigin('https://antifailure.test:8443/callback', 'https://antifailure.test'), false)
  })
  test('another scheme is not', () => {
    assert.equal(isOrigin('http://antifailure.test/callback', 'https://antifailure.test'), false)
  })
  test('a value that is not a URL is not', () => {
    assert.equal(isOrigin('antifailure.test/callback', 'https://antifailure.test'), false)
  })
})

describe('decodeHtml', () => {
  test('an escaped entity stays escaped, because &amp; is decoded last', () => {
    assert.equal(decodeHtml('&amp;lt;b&amp;gt;'), '&lt;b&gt;')
    assert.equal(decodeHtml('&amp;quot;'), '&quot;')
  })
  test('the entities a Keycloak form carries are decoded', () => {
    assert.equal(decodeHtml('https:&#x2F;&#x2F;kc.test&#x2F;x?a=1&amp;b=2'), 'https://kc.test/x?a=1&b=2')
    assert.equal(decodeHtml('&lt;&gt;&quot;&#x27;&apos;'), '<>"\'\'')
  })
})

describe('keycloak.test.ts', () => {
  // That file runs only against a real Keycloak, which CI does not start, so
  // nothing but this would notice it growing its own copy of either rule again.
  test('takes both rules from browser.ts rather than writing its own', () => {
    const source = readFileSync(new URL('./keycloak.test.ts', import.meta.url), 'utf8')
    assert.match(source, /import \{ decodeHtml, isOrigin \} from '\.\/browser\.ts'/)
    assert.match(source, /if \(isOrigin\(outUrl, 'https:\/\/antifailure\.test'\)\)/)
    assert.doesNotMatch(source, /\.startsWith\('https:\/\/antifailure\.test'\)/)
    assert.doesNotMatch(source, /function decodeHtml\(/)
  })
})
