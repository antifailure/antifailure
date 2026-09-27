import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import {
  assertWebsiteDocument, emptyWebsiteDocument, normalizeWebsiteDocument, referencedAssets,
  resetStyleOverride, resolveCollection, resolveField, resolveOrder, resolveStyle, safeBuiltinSource,
  safeHref, setFieldOverride, setStyleOverride, stableStringify, validateWebsiteDocument,
  CUSTOM_SHAPES, DIVIDER_VARIANTS, isCustomShape, isDividerVariant, resolveCustomShape, resolveDividerVariant,
} from '../src/index.ts'
import type { FieldValue, RichTextDocument } from '../src/index.ts'

const asset = '7e6d1890-144a-4acf-9a12-579a96ab91ac'
const font = 'bbed3f78-ec9a-4b63-931b-278eed66be15'
const customId = `custom-${asset}`
const richText: RichTextDocument = {
  type: 'doc', content: [
    { type: 'paragraph', content: [
      { type: 'text', text: 'Rehearse ', marks: [{ type: 'bold' }] },
      { type: 'text', text: 'the change', marks: [{ type: 'link', attrs: { href: '/docs/quickstart' } }] },
      { type: 'hardBreak' }, { type: 'text', text: 'Then deploy.' },
    ] },
    { type: 'bulletList', content: [{ type: 'listItem', content: [{ type: 'paragraph', content: [{ type: 'text', text: 'See the finding.' }] }] }] },
  ],
}

function frozen<T>(value: T): T {
  if (value && typeof value === 'object') {
    Object.freeze(value)
    for (const child of Object.values(value)) frozen(child)
  }
  return value
}

test('empty documents are independent, valid, and contain no copied defaults', () => {
  const one = emptyWebsiteDocument()
  const two = emptyWebsiteDocument()
  one.fields['hero.title'] = 'New title'
  assert.deepEqual(two.fields, {})
  assert.equal(validateWebsiteDocument(two).ok, true)
  assert.deepEqual(assertWebsiteDocument(two), two)
})

test('new source defaults flow through untouched fields; explicit edits and reset survive release changes', () => {
  const blank = emptyWebsiteDocument()
  assert.equal(resolveField(blank, 'hero.title', 'Old title'), 'Old title')
  assert.equal(resolveField(blank, 'hero.title', 'New source title'), 'New source title')
  const edited = setFieldOverride(blank, 'hero.title', 'Owner title', 'Old title')
  assert.equal(resolveField(edited, 'hero.title', 'New source title'), 'Owner title')
  assert.equal(resolveField(edited, 'hero.subtitle', 'New subtitle'), 'New subtitle')
  const reset = setFieldOverride(edited, 'hero.title', undefined)
  assert.deepEqual(reset.fields, {})
  assert.equal(resolveField(reset, 'hero.title', 'New source title'), 'New source title')
  assert.deepEqual(setFieldOverride(edited, 'hero.title', 'New source title', 'New source title').fields, {})
})

test('field resolution rejects stale incompatible types and does not coerce them', () => {
  const doc = emptyWebsiteDocument()
  doc.fields = { 'hero.title': 123, 'hero.width': 'wide', 'hero.enabled': 'true', 'hero.image': richText, 'hero.optional': { type: 'media', source: 'asset', assetId: asset } }
  assert.equal(resolveField(doc, 'hero.title', 'Headline'), 'Headline')
  assert.equal(resolveField(doc, 'hero.width', 800), 800)
  assert.equal(resolveField(doc, 'hero.enabled', false), false)
  assert.deepEqual(resolveField(doc, 'hero.image', { type: 'media', source: 'builtin', src: '/home/demo.webp' }), { type: 'media', source: 'builtin', src: '/home/demo.webp' })
  assert.deepEqual(resolveField(doc, 'hero.optional', null), doc.fields['hero.optional'])
})

test('explicit null removes source media while reset restores its current default', () => {
  const source = { type: 'media', source: 'builtin', src: '/home/image.webp', kind: 'image' } as const
  const doc = setFieldOverride(emptyWebsiteDocument(), 'hero.image', null, source)
  assert.equal(resolveField(doc, 'hero.image', source), null)
  assert.deepEqual(doc.fields, { 'hero.image': null })
  assert.deepEqual(resolveField(setFieldOverride(doc, 'hero.image', undefined), 'hero.image', source), source)
  assert.equal(resolveField({ ...doc, fields: { 'hero.title': null } }, 'hero.title', 'Source text'), 'Source text')
})

test('media kind survives round-trip for UUID URLs and old builtin media remains compatible', () => {
  const doc = emptyWebsiteDocument()
  doc.fields.video = { type: 'media', source: 'asset', assetId: asset, kind: 'video', alt: 'Recorded demo' }
  doc.fields.legacy = { type: 'media', source: 'builtin', src: '/home/demo.mp4' }
  assert.deepEqual(assertWebsiteDocument(doc), doc)
  for (const kind of ['font', 'audio', '<script>', 1]) assert.equal(validateWebsiteDocument({ ...doc, fields: { media: { type: 'media', source: 'asset', assetId: asset, kind } } }).ok, false)
})

test('shape and divider sections persist named choices and safe existing style controls', () => {
  const dividerId = `custom-${font}`
  const doc = emptyWebsiteDocument()
  doc.sections.custom = [
    { id: customId, kind: 'shape', group: 'hero', after: 'hero' },
    { id: dividerId, kind: 'divider', group: 'page', after: 'migrations' },
  ]
  doc.fields[`${customId}.shape`] = 'arch'
  doc.fields[`${dividerId}.variant`] = 'wave'
  doc.fields[`${dividerId}.label`] = 'Before you deploy'
  doc.styles[customId] = { desktop: { color: '#32c700', backgroundColor: '#f8f8f8', width: 320, minHeight: 160, borderRadius: 12 } }
  assert.deepEqual(assertWebsiteDocument(doc), doc)
  const reset = setFieldOverride(doc, `${customId}.shape`, undefined)
  assert.equal(resolveCustomShape(reset.fields[`${customId}.shape`]), 'circle')
  assert.equal(validateWebsiteDocument({ ...doc, sections: { ...doc.sections, custom: [{ id: customId, kind: 'svg', group: 'hero', after: null }] } }).ok, false)
})

test('shape and divider render choices reject markup, CSS, unsupported names, and non-text values', () => {
  for (const choice of CUSTOM_SHAPES) { assert.equal(isCustomShape(choice), true); assert.equal(resolveCustomShape(choice), choice) }
  for (const choice of DIVIDER_VARIANTS) { assert.equal(isDividerVariant(choice), true); assert.equal(resolveDividerVariant(choice), choice) }
  for (const value of ['<svg onload=alert(1)>', 'url(javascript:alert(1))', 'star', '__proto__', '', null, 1, {}, ['circle']]) {
    assert.equal(isCustomShape(value), false)
    assert.equal(isDividerVariant(value), false)
    assert.equal(resolveCustomShape(value), 'circle')
    assert.equal(resolveDividerVariant(value), 'line')
  }
  assert.throws(() => (CUSTOM_SHAPES as string[]).push('svg'))
  assert.throws(() => (DIVIDER_VARIANTS as string[]).push('html'))
})

test('embed blocks preserve code as inert strings and layering uses bounded structured styles', () => {
  const doc = emptyWebsiteDocument()
  doc.sections.custom = [{ id: customId, kind: 'embed', group: 'page', after: 'features' }]
  doc.fields[`${customId}.html`] = '<button id="demo">Show finding</button>'
  doc.fields[`${customId}.css`] = '#demo { color: #32c700; }'
  doc.fields[`${customId}.javascript`] = 'document.querySelector("#demo").addEventListener("click", () => { console.log("Demo"); });'
  doc.styles[customId] = { desktop: { position: 'absolute', zIndex: 20, x: 120, y: -20 }, mobile: { position: 'relative', zIndex: 0 } }
  const validated = assertWebsiteDocument(doc)
  assert.deepEqual(validated, doc)
  assert.equal(typeof validated.fields[`${customId}.javascript`], 'string')
  assert.deepEqual(resolveStyle(setStyleOverride(doc, customId, 'desktop', 'position', 'fixed'), customId, 'desktop'), { position: 'fixed', zIndex: 20, x: 120, y: -20 })
  assert.equal(resolveStyle(setStyleOverride(doc, customId, 'desktop', 'zIndex', 100), customId, 'desktop').zIndex, 100)
  assert.deepEqual(resolveStyle(resetStyleOverride(doc, customId, 'desktop', 'position'), customId, 'desktop'), { zIndex: 20, x: 120, y: -20 })
})

test('layering rejects overflow, fractional levels and arbitrary positioning declarations', () => {
  for (const value of [-1, 101, 1.5, Number.MAX_SAFE_INTEGER, Infinity, '100', 'expression(alert(1))']) {
    assert.equal(validateWebsiteDocument({ ...emptyWebsiteDocument(), styles: { hero: { desktop: { zIndex: value } } } }).ok, false)
  }
  for (const position of ['sticky', 'absolute; color:red', 'url(evil)', 0]) {
    assert.equal(validateWebsiteDocument({ ...emptyWebsiteDocument(), styles: { hero: { desktop: { position } } } }).ok, false)
  }
  assert.throws(() => setStyleOverride(emptyWebsiteDocument(), 'hero', 'desktop', 'zIndex', 101))
  assert.throws(() => setStyleOverride(emptyWebsiteDocument(), 'hero', 'desktop', 'zIndex', 0.5))
})

test('immutable edits do not mutate frozen inputs or keep mutable caller value references', () => {
  const original = frozen(emptyWebsiteDocument())
  const input = structuredClone(richText)
  const edited = setFieldOverride(original, 'hero.title', input)
  input.content.length = 0
  assert.equal((edited.fields['hero.title'] as RichTextDocument).content.length, 2)
  assert.deepEqual(original.fields, {})
  const frozenEdited = frozen(edited)
  assert.deepEqual(setFieldOverride(frozenEdited, 'hero.title', richText, richText).fields, {})
})

test('rich text, safe media, custom blocks and styles round-trip through strict validation', () => {
  const doc = emptyWebsiteDocument()
  doc.sourceVersion = 'abc123'
  doc.fields = {
    'hero.title': richText,
    'hero.image': { type: 'media', source: 'builtin', src: '/home/photo.webp', alt: 'Production twin' },
    [`${customId}.image`]: { type: 'media', source: 'asset', assetId: asset, decorative: true },
  }
  doc.sections.custom.push({ id: customId, kind: 'split', group: 'page', after: 'hero' })
  doc.sections.moves.push({ id: 'footer', after: 'features', group: 'page' })
  doc.styles['hero.title'] = { desktop: { fontSize: 100, fontFamily: `asset:${font}`, lineHeight: 1.1, color: '#1a1a1a' }, mobile: { fontSize: 32 } }
  assert.deepEqual(assertWebsiteDocument(doc), doc)
  assert.deepEqual(referencedAssets(doc), [asset, font].sort())
})

test('a malformed field does not erase unrelated content, collection items, or good style controls', () => {
  const doc = emptyWebsiteDocument() as unknown as Record<string, unknown>
  doc.fields = {
    'hero.title': 'Keep me', 'hero.bad': { type: 'doc', content: [{ type: 'image', attrs: { src: 'javascript:alert(1)' } }] },
    'hero.subtitle': 'Keep this too', 'hero.video': { type: 'media', source: 'asset', assetId: 'not-an-id' },
  }
  doc.styles = { hero: { desktop: { fontSize: 64, color: 'url(javascript:evil)', opacity: 0.9 }, mobile: { fontSize: 30 } } }
  doc.collections = { 'footer.links': { hidden: [], moves: [], custom: [
    { id: 'valid-link', fields: { text: 'Docs', href: '/docs' } },
    { id: 'constructor', fields: { text: 'Bad' } },
    { id: 'another-link', fields: { text: 'About', bad: { nope: true } } },
  ] } }
  const result = normalizeWebsiteDocument(doc)
  assert.ok(result.warnings.length >= 4)
  assert.deepEqual(result.document.fields, { 'hero.title': 'Keep me', 'hero.subtitle': 'Keep this too' })
  assert.deepEqual(result.document.styles.hero, { desktop: { fontSize: 64, opacity: 0.9 }, mobile: { fontSize: 30 } })
  assert.equal(result.document.collections['footer.links']?.custom.length, 2)
  assert.deepEqual(result.document.collections['footer.links']?.custom[1]?.fields, { text: 'About' })
  assert.equal(validateWebsiteDocument(doc).ok, false)
})

test('all supported rich text marks and nested lists remain structured and safe', () => {
  const doc = emptyWebsiteDocument()
  doc.fields['hero.description'] = { type: 'doc', content: [
    { type: 'paragraph', content: [{ type: 'text', text: '<script>literal text, never HTML</script>', marks: [
      { type: 'bold' }, { type: 'italic' }, { type: 'underline' }, { type: 'code' }, { type: 'emphasis' },
      { type: 'link', attrs: { href: 'https://antifailure.dev/docs' } },
    ] }] },
    { type: 'orderedList', content: [{ type: 'listItem', content: [{ type: 'paragraph' }, { type: 'bulletList', content: [{ type: 'listItem', content: [{ type: 'paragraph' }] }] }] }] },
  ] }
  assert.deepEqual(assertWebsiteDocument(doc), doc)
})

test('unsafe rich text links, arbitrary attributes, images and HTML are rejected', () => {
  const inputs = [
    { type: 'doc', content: [{ type: 'paragraph', attrs: { style: 'color:red' } }] },
    { type: 'doc', content: [{ type: 'paragraph', content: [{ type: 'text', text: 'Click', marks: [{ type: 'link', attrs: { href: 'javascript:alert(1)' } }] }] }] },
    { type: 'doc', content: [{ type: 'paragraph', content: [{ type: 'text', text: 'Click', marks: [{ type: 'link', attrs: { href: '/docs', onclick: 'evil' } }] }] }] },
    { type: 'doc', content: [{ type: 'image', attrs: { src: '/home/image.webp' } }] },
    { type: 'doc', content: [{ type: 'html', content: '<b>unsafe</b>' }] },
    { type: 'doc', content: [{ type: 'text', text: 'Text must be in a paragraph.' }] },
  ]
  for (const value of inputs) {
    const doc = { ...emptyWebsiteDocument(), fields: { 'hero.title': value } }
    assert.equal(validateWebsiteDocument(doc).ok, false)
    assert.deepEqual(normalizeWebsiteDocument(doc).document.fields, {})
    assert.throws(() => setFieldOverride(emptyWebsiteDocument(), 'hero.title', value as FieldValue))
  }
})

test('safe links support site, anchor, HTTPS, mail and telephone destinations', () => {
  for (const href of ['/docs', '/docs#setup', '#migrations', 'https://antifailure.dev/docs?q=one%20two', 'mailto:vir@antifailure.dev', 'tel:+17135551212']) assert.equal(safeHref(href), true, href)
  for (const href of ['javascript:alert(1)', 'data:text/html,a', 'vbscript:evil', '//evil.test', '/\\evil.test', 'https:\\evil.test', 'https://user:pass@example.com', '/%2fexample.com', '/%252fexample.com', '/x/../secret', '/x/%2e%2e/secret', '/x/%00y', 'https://example.com/%0d%0aHeader:x', 'https://example.com/"onclick=', ' javascript:evil', '']) assert.equal(safeHref(href), false, href)
})

test('builtin media paths cannot become remote requests or traverse directories', () => {
  for (const src of ['/home/demo.mp4', '/images/brand.svg', '/home/photo.webp?v=2']) assert.equal(safeBuiltinSource(src), true, src)
  for (const src of ['https://example.com/photo.jpg', '//example.com/photo.jpg', '/%2fexample.com/a', '/home/../../a', '/home/%252e%252e/a', '/home/%5ca', '/home/\u0000a', '../a', '/home/%zz']) assert.equal(safeBuiltinSource(src), false, src)
})

test('prototype keys and getter hooks are rejected without execution or pollution', () => {
  const doc = JSON.parse('{"schemaVersion":1,"fields":{"hero.title":"Kept","__proto__":{"polluted":true},"hero.constructor.text":"bad"},"styles":{},"sections":{"hidden":[],"moves":[],"custom":[]},"collections":{}}')
  const result = normalizeWebsiteDocument(doc)
  assert.deepEqual(result.document.fields, { 'hero.title': 'Kept' })
  assert.equal(validateWebsiteDocument(doc).ok, false)
  assert.equal(({} as Record<string, unknown>).polluted, undefined)
  let calls = 0
  const getter = Object.defineProperty(emptyWebsiteDocument(), 'fields', { enumerable: true, get() { calls++; throw new Error('Do not execute') } })
  assert.equal(validateWebsiteDocument(getter).ok, false)
  assert.equal(calls, 0)
  assert.throws(() => setFieldOverride(emptyWebsiteDocument(), '__proto__.title', 'bad'))
})

test('unknown properties, unsafe numbers, rogue CSS and excessive content fail writes', () => {
  const badStyles = [
    { fontSize: Infinity }, { fontSize: 300 }, { opacity: -1 }, { focalX: 101 },
    { float: 'left' }, { color: 'red' }, { backgroundImage: 'url(https://evil.test)' },
    { fontFamily: 'Inter; background: red' }, { x: 99999 },
  ]
  for (const style of badStyles) assert.equal(validateWebsiteDocument({ ...emptyWebsiteDocument(), styles: { hero: { desktop: style } } }).ok, false)
  assert.equal(validateWebsiteDocument({ ...emptyWebsiteDocument(), fields: { a: NaN } }).ok, false)
  assert.equal(validateWebsiteDocument({ ...emptyWebsiteDocument(), unexpected: true }).ok, false)
  assert.equal(validateWebsiteDocument({ ...emptyWebsiteDocument(), schemaVersion: 2 }).ok, false)
  assert.equal(validateWebsiteDocument({ ...emptyWebsiteDocument(), fields: Object.fromEntries(Array.from({ length: 1501 }, (_, i) => [`field-${i}`, 'x'])) }).ok, false)
  assert.equal(validateWebsiteDocument({ ...emptyWebsiteDocument(), fields: Object.fromEntries(Array.from({ length: 20 }, (_, i) => [`field-${i}`, 'x'.repeat(60_000)])) }).ok, false)
  assert.equal(validateWebsiteDocument({ ...emptyWebsiteDocument(), sections: { hidden: Array.from({ length: 81 }, (_, i) => `block-${i}`), moves: [], custom: [] } }).ok, false)
})

test('rich text nesting limits and circular input fail without losing sibling fields', () => {
  const circular: Record<string, unknown> = { type: 'bulletList', content: [] }
  circular.content = [{ type: 'listItem', content: [circular] }]
  let nested: unknown = { type: 'paragraph' }
  for (let index = 0; index < 6; index++) nested = { type: 'bulletList', content: [{ type: 'listItem', content: [nested] }] }
  for (const content of [circular, nested]) {
    const raw = { ...emptyWebsiteDocument(), fields: { bad: { type: 'doc', content: [content] }, good: 'Still present' } }
    assert.equal(validateWebsiteDocument(raw).ok, false)
    assert.deepEqual(normalizeWebsiteDocument(raw).document.fields, { good: 'Still present' })
  }
})

test('reordering preserves new source sections, hidden anchors, and removed-anchor items', () => {
  assert.deepEqual(resolveOrder(['hero', 'proof', 'features', 'footer'], [{ id: 'features', after: 'hero' }]), ['hero', 'features', 'proof', 'footer'])
  assert.deepEqual(resolveOrder(['hero', 'new-release', 'proof', 'features', 'footer'], [{ id: 'features', after: 'hero' }]), ['hero', 'features', 'new-release', 'proof', 'footer'])
  assert.deepEqual(resolveOrder(['hero', 'features', 'footer'], [{ id: 'features', after: 'removed-block' }]), ['hero', 'features', 'footer'])
  assert.deepEqual(resolveOrder(['hero', 'features', 'footer'], [{ id: customId, after: 'hero' }], [customId], ['hero']), [customId, 'features', 'footer'])
  assert.deepEqual(resolveOrder(['hero', 'footer'], [{ id: customId, after: 'removed-block' }], [customId]), ['hero', 'footer', customId])
})

test('ordering resolves dependent moves, front moves and cycles deterministically', () => {
  assert.deepEqual(resolveOrder(['a', 'b', 'c', 'd'], [{ id: 'a', after: 'b' }, { id: 'b', after: 'c' }]), ['c', 'b', 'a', 'd'])
  assert.deepEqual(resolveOrder(['a', 'b', 'c'], [{ id: 'c', after: null }]), ['c', 'a', 'b'])
  const moves = [{ id: 'a', after: 'b' }, { id: 'b', after: 'a' }, { id: 'c', after: 'a' }]
  assert.deepEqual(resolveOrder(['a', 'b', 'c', 'd'], moves), ['a', 'c', 'b', 'd'])
  assert.deepEqual(resolveOrder(['a', 'a', 'b'], [{ id: 'unknown', after: 'a' }, { id: 'a', after: 'a' }]), ['a', 'b'])
})

test('collections merge stable item fields while keeping updated defaults and new items', () => {
  const doc = emptyWebsiteDocument()
  doc.fields['header.links.docs.title'] = 'Read the docs'
  doc.collections['header.links'] = { hidden: ['blog'], moves: [{ id: 'support', after: 'docs' }], custom: [
    { id: 'support', fields: { title: 'Support', href: '/contact' } },
    { id: 'docs', fields: { title: 'Stale collision must not replace source', href: '/evil' } },
  ] }
  const defaults = frozen([
    { id: 'docs', title: 'Documentation', href: '/docs/new', sourceOnly: 'Kept' },
    { id: 'new-product', title: 'New product', href: '/product' },
    { id: 'blog', title: 'Writing', href: '/blog' },
  ])
  const result = resolveCollection(frozen(doc), 'header.links', defaults)
  assert.deepEqual(result, [
    { id: 'docs', title: 'Read the docs', href: '/docs/new', sourceOnly: 'Kept' },
    { id: 'support', title: 'Support', href: '/contact' },
    { id: 'new-product', title: 'New product', href: '/product' },
  ])
  assert.equal(defaults[0]?.title, 'Documentation')
})

test('collections also support manifest-shaped nested fields', () => {
  const doc = emptyWebsiteDocument()
  doc.fields['hero.features.test.title'] = 'Rehearse the change'
  doc.collections['hero.features'] = { hidden: [], moves: [], custom: [{ id: 'extra', fields: { title: 'Extra' } }] }
  assert.deepEqual(resolveCollection(doc, 'hero.features', [{ id: 'test', label: 'Test', fields: { title: 'Test', body: 'New default' } }]), [
    { id: 'test', label: 'Test', fields: { title: 'Rehearse the change', body: 'New default' } },
    { id: 'extra', fields: { title: 'Extra' } },
  ])
})

test('responsive edits never leak desktop values onto tablet or mobile', () => {
  const original = frozen(emptyWebsiteDocument())
  const desktop = setStyleOverride(original, 'hero.title', 'desktop', 'fontSize', 100)
  assert.deepEqual(resolveStyle(desktop, 'hero.title', 'desktop', { fontSize: 80, lineHeight: 1.1 }), { fontSize: 100, lineHeight: 1.1 })
  assert.deepEqual(resolveStyle(desktop, 'hero.title', 'mobile', { fontSize: 32 }), { fontSize: 32 })
  assert.deepEqual(resolveStyle(desktop, 'hero.title', 'tablet', { fontSize: 52 }), { fontSize: 52 })
  const mobile = setStyleOverride(frozen(desktop), 'hero.title', 'mobile', 'fontSize', 36)
  assert.equal(resolveStyle(mobile, 'hero.title', 'desktop').fontSize, 100)
  assert.equal(resolveStyle(mobile, 'hero.title', 'mobile').fontSize, 36)
  assert.throws(() => setStyleOverride(original, 'hero.title', 'desktop', 'fontSize', 1000))
  assert.deepEqual(resetStyleOverride(mobile, 'hero.title', 'mobile').styles, desktop.styles)
  assert.deepEqual(resetStyleOverride(mobile, 'hero.title', 'desktop', 'fontSize').styles, { 'hero.title': { mobile: { fontSize: 36 } } })
  assert.deepEqual(resetStyleOverride(mobile, 'hero.title').styles, {})
  assert.deepEqual(original.styles, {})
})

test('referenced assets include custom items and each font once', () => {
  const doc = emptyWebsiteDocument()
  doc.fields.image = { type: 'media', source: 'asset', assetId: asset }
  doc.fields.builtin = { type: 'media', source: 'builtin', src: '/home/image.webp' }
  doc.collections.features = { hidden: [], moves: [], custom: [{ id: 'extra', fields: { image: { type: 'media', source: 'asset', assetId: asset.toUpperCase() } } }] }
  doc.styles.title = { desktop: { fontFamily: `asset:${font}` }, mobile: { fontFamily: `asset:${font}` } }
  assert.deepEqual(referencedAssets(doc), [asset, font].sort())
})

test('stable serialization sorts keys, retains order in arrays, rejects coercion and getters', () => {
  assert.equal(stableStringify({ z: true, a: { b: 2, a: 1 }, arr: ['z', 'a'] }), '{"a":{"a":1,"b":2},"arr":["z","a"],"z":true}')
  assert.equal(stableStringify({ b: 2, a: 1 }), stableStringify({ a: 1, b: 2 }))
  for (const value of [undefined, NaN, Infinity, new Date(), [undefined], { a: undefined }, JSON.parse('{"__proto__":{}}')]) assert.throws(() => stableStringify(value))
  let calls = 0
  assert.throws(() => stableStringify(Object.defineProperty({}, 'value', { enumerable: true, get() { calls++; return 1 } })))
  assert.equal(calls, 0)
  const circular: Record<string, unknown> = {}
  circular.self = circular
  assert.throws(() => stableStringify(circular))
})

test('normalization itself does not mutate the raw document', () => {
  const raw = frozen({ ...emptyWebsiteDocument(), fields: { title: 'Keep', broken: { unknown: true } } })
  const before = JSON.stringify(raw)
  normalizeWebsiteDocument(raw)
  assert.equal(JSON.stringify(raw), before)
})
