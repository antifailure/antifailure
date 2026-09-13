// The markdown twins keep the text a page shows, and the check that compares a
// page with its twin can tell when they do not.
//
// CodeQL #9 and #10. markdown-twins.mjs decoded &amp; before &lt;, &gt; and
// &quot;, so a page that SHOWS the text "&lt;b&gt;" (written in its HTML as
// &amp;lt;b&amp;gt;) got a twin that said "<b>", which is a different sentence.
// check-seo.mjs compared page cells with the twin using the same order, so it
// agreed with the twin and could never report it. Nothing published carries
// such text today; the first page that does would have had a wrong twin and a
// green check.
import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { decodeEntities } from '../scripts/entities.mjs'
import { renderTwin } from '../scripts/twin.mjs'
import { twinCells } from '../scripts/twin-cells.mjs'

// One set of inputs for the writer and for the check, because the defect was
// the two agreeing on the wrong answer.
const SHOWN = [
  { html: '&amp;lt;b&amp;gt;', text: '&lt;b&gt;' },
  { html: '&amp;quot;', text: '&quot;' },
]

const PAGE = `<!doctype html><html><head><title>How a page shows markup · Antifailure</title>
<link rel="canonical" href="https://antifailure.dev/fixture">
<meta name="description" content="A page that shows markup as text"></head>
<body><main><h1>How a page shows markup</h1>
<table><tr><th>Typed</th><th>Shown</th></tr>
<tr><td>an escaped tag</td><td>${SHOWN[0]!.html}</td></tr>
<tr><td>an escaped quote</td><td>${SHOWN[1]!.html}</td></tr></table>
</main></body></html>`

describe('HTML entities in the markdown twins', () => {
  it('decodes &amp; last, so an escaped entity stays escaped', () => {
    for (const { html, text } of SHOWN) assert.equal(decodeEntities(html), text)
    assert.equal(decodeEntities('a &amp;amp; b'), 'a &amp; b')
  })

  it('still decodes the ordinary entities', () => {
    assert.equal(decodeEntities('&lt;b&gt;'), '<b>')
    assert.equal(decodeEntities('&quot;x&quot;'), '"x"')
    assert.equal(decodeEntities('it&#x27;s'), "it's")
    assert.equal(decodeEntities('&#8217;'), '’')
    assert.equal(decodeEntities('a&nbsp;b'), 'a b')
    assert.equal(decodeEntities('R&amp;D'), 'R&D')
  })

  it('writes a twin that carries the text the page shows', () => {
    const twin = renderTwin(PAGE, 'fixture.html')
    assert.ok(twin, 'the fixture page produced no twin')
    for (const { text } of SHOWN) assert.ok(twin.markdown.includes(text), `twin lost ${text}:\n${twin.markdown}`)
    assert.ok(!twin.markdown.includes('<b>'), `twin turned shown text into markup:\n${twin.markdown}`)
  })

  it('finds every cell of a page in its own twin', () => {
    const twin = renderTwin(PAGE, 'fixture.html')!
    const result = twinCells(PAGE, twin.markdown)
    assert.equal(result.checked, 6)
    assert.deepEqual(result.absent, [])
  })

  it('says no when a twin says something other than the page', () => {
    const twin = renderTwin(PAGE, 'fixture.html')!
    const mangled = twin.markdown.replace('&lt;b&gt;', '<b>')
    const result = twinCells(PAGE, mangled)
    assert.deepEqual(result.absent.map((cell) => cell.value), ['&lt;b&gt;'])
  })

  it('is what the build and the check actually call', () => {
    const writer = readFileSync(new URL('../scripts/markdown-twins.mjs', import.meta.url), 'utf8')
    const check = readFileSync(new URL('../scripts/check-seo.mjs', import.meta.url), 'utf8')
    assert.match(writer, /import \{ renderTwin \} from "\.\/twin\.mjs"/)
    assert.match(writer, /renderTwin\(html, /)
    assert.match(check, /import \{ twinCells \} from "\.\/twin-cells\.mjs"/)
    assert.match(check, /twinCells\(readFileSync\(file/)
    for (const [name, source] of [['markdown-twins.mjs', writer], ['check-seo.mjs cell block', check.slice(check.indexOf('Markdown twins carry'), check.indexOf('Machine-readable corpus'))]] as const) {
      assert.doesNotMatch(source, /\.replace\(\/&amp;\/g/, `${name} decodes entities itself instead of calling decodeEntities`)
    }
  })
})
