import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { emptyWebsiteDocument, validatePreviewMessage, validateWebsiteManifest } from '../src/index.ts'
import type { PreviewBridgeDirection, WebsiteManifest } from '../src/index.ts'

const session = 'e216eb02-cd86-4f18-a49b-0d8b9f6353ae'
const assetId = '7e6d1890-144a-4acf-9a12-579a96ab91ac'
const manifest: WebsiteManifest = {
  schemaVersion: 1,
  sourceVersion: 'homepage-1',
  fields: [
    { key: 'hero.title', label: 'Headline', kind: 'text', sectionId: 'hero', defaultValue: 'Rehearse the change', required: true },
    { key: 'hero.link', label: 'Demo link', kind: 'url', sectionId: 'hero', defaultValue: '/request-demo' },
    { key: 'hero.image', label: 'Background', kind: 'media', sectionId: 'hero', defaultValue: { type: 'media', source: 'builtin', src: '/home/photo.webp' } },
    { key: 'hero.align', label: 'Alignment', kind: 'select', sectionId: 'hero', defaultValue: 'left', options: [{ label: 'Left', value: 'left' }, { label: 'Center', value: 'center' }] },
  ],
  sections: [{ id: 'hero', label: 'Hero', group: 'hero', kind: 'hero', inToc: true }],
  collections: [{ key: 'header.links', label: 'Navigation', sectionId: 'header', items: [{ id: 'docs', label: 'Docs', fields: { title: 'Docs', href: '/docs' } }] }],
  fonts: [{ key: 'inter', label: 'Inter', family: 'Inter, sans-serif' }, { key: 'geist-mono', label: 'Geist Mono', family: '"Geist Mono", monospace' }],
  builtinAssets: [{ id: 'hero-photo', label: 'Hero photograph', src: '/home/photo.webp', kind: 'image' }],
}

function message(type: string, payload: unknown) {
  return { protocol: 'antifailure-cms', version: 1, session, type, payload }
}

test('manifest discovered from source validates and preserves current defaults', () => {
  const result = validateWebsiteManifest(manifest)
  assert.equal(result.ok, true)
  if (result.ok) {
    assert.deepEqual(result.manifest, manifest)
    result.manifest.fields[0]!.defaultValue = 'Changed copy'
    assert.equal(manifest.fields[0]?.defaultValue, 'Rehearse the change')
  }
})

test('code fields declare a supported language and keep defaults as plain strings', () => {
  for (const language of ['html', 'css', 'javascript'] as const) {
    const next: WebsiteManifest = { ...manifest, fields: [{ key: 'custom-demo.code', label: 'Embed source', kind: 'code', language, sectionId: 'hero', defaultValue: '<script>console.log("isolated");</script>' }] }
    const result = validateWebsiteManifest(next)
    assert.equal(result.ok, true)
    if (result.ok) assert.equal(result.manifest.fields[0]?.defaultValue, '<script>console.log("isolated");</script>')
    assert.equal(validatePreviewMessage(message('ready', { manifest: next }), 'child-to-parent').ok, true)
  }
  for (const field of [
    { key: 'embed.source', label: 'Source', kind: 'code', language: 'python', sectionId: 'hero', defaultValue: 'print(1)' },
    { key: 'embed.source', label: 'Source', kind: 'text', language: 'html', sectionId: 'hero', defaultValue: 'Plain' },
    { key: 'embed.source', label: 'Source', kind: 'code', language: 'html', sectionId: 'hero', defaultValue: { type: 'doc', content: [] } },
  ]) assert.equal(validateWebsiteManifest({ ...manifest, fields: [field] }).ok, false)
})

test('preview bridge accepts each permitted message shape in its intended direction', () => {
  const cases: Array<[string, unknown, PreviewBridgeDirection]> = [
    ['init', { document: emptyWebsiteDocument(), assetUrls: {}, mode: 'edit' }, 'parent-to-child'],
    ['update', { document: emptyWebsiteDocument(), assetUrls: { [assetId]: 'https://cdn.antifailure.dev/media/file.webp?token=123' }, mode: 'preview' }, 'parent-to-child'],
    ['select', { key: 'hero.title', sectionId: 'hero' }, 'parent-to-child'],
    ['select', {}, 'parent-to-child'],
    ['ready', { manifest }, 'child-to-parent'],
    ['select', { sectionId: 'hero' }, 'child-to-parent'],
    ['select', {}, 'child-to-parent'],
    ['edit', { key: 'hero.title', value: 'New headline' }, 'child-to-parent'],
    ['edit', { key: 'hero.image', value: { type: 'media', source: 'asset', assetId } }, 'child-to-parent'],
    ['error', { message: 'The preview could not load the image.' }, 'child-to-parent'],
  ]
  for (const [type, payload, direction] of cases) {
    const result = validatePreviewMessage(message(type, payload), direction)
    assert.equal(result.ok, true, JSON.stringify(result))
    if (result.ok) assert.deepEqual(result.message, message(type, payload))
  }
})

test('envelope rejects wrong protocol/version, missing or invalid session, unknown fields and reversed directions', () => {
  const valid = message('init', { document: emptyWebsiteDocument(), assetUrls: {}, mode: 'edit' })
  for (const invalid of [
    { ...valid, protocol: 'different' }, { ...valid, version: 2 }, { ...valid, session: '' }, { ...valid, session: 'no-session' },
    { ...valid, extra: 'untrusted' }, { ...valid, type: 'publish' }, { ...valid, payload: null },
  ]) assert.equal(validatePreviewMessage(invalid, 'parent-to-child').ok, false)
  assert.equal(validatePreviewMessage(valid, 'child-to-parent').ok, false)
  assert.equal(validatePreviewMessage(message('edit', { key: 'hero.title', value: 'x' }), 'parent-to-child').ok, false)
  assert.equal(validatePreviewMessage(message('ready', { manifest }), 'parent-to-child').ok, false)
})

test('preview payload validation blocks script URLs, remote builtin media and unsafe edit values', () => {
  for (const url of ['javascript:alert(1)', 'data:text/html,x', '//evil.test/a', 'blob:https://app.antifailure.dev/123', 'https://example.com/%0aevil']) {
    assert.equal(validatePreviewMessage(message('update', { document: emptyWebsiteDocument(), assetUrls: { [assetId]: url }, mode: 'edit' }), 'parent-to-child').ok, false, url)
  }
  for (const payload of [
    { key: '__proto__.name', value: 'unsafe' },
    { key: 'hero.image', value: { type: 'media', source: 'builtin', src: 'https://evil.test/tracker' } },
    { key: 'hero.title', value: { type: 'doc', content: [{ type: 'paragraph', content: [{ type: 'text', text: 'x', marks: [{ type: 'link', attrs: { href: 'javascript:evil' } }] }] }] } },
    { key: 'hero.title', value: 'valid', run: 'doSomething' },
  ]) assert.equal(validatePreviewMessage(message('edit', payload), 'child-to-parent').ok, false)
  assert.equal(validatePreviewMessage(message('update', { document: { ...emptyWebsiteDocument(), fields: { broken: { nope: true } } }, assetUrls: {}, mode: 'edit' }), 'parent-to-child').ok, false)
})

test('selection identifiers are safe and an empty selection can clear the inspector', () => {
  assert.equal(validatePreviewMessage(message('select', {}), 'child-to-parent').ok, true)
  for (const payload of [{ key: 'hero.__proto__.x' }, { sectionId: 'constructor' }, { key: '<script>' }, { x: 4 }]) assert.equal(validatePreviewMessage(message('select', payload), 'child-to-parent').ok, false)
})

test('invalid manifest items fail independently with useful error paths instead of throwing', () => {
  const cases = [
    { ...manifest, fields: [{ ...manifest.fields[0], key: 'hero.constructor.bad' }] },
    { ...manifest, fields: [{ ...manifest.fields[0], defaultValue: 12 }] },
    { ...manifest, fields: [{ ...manifest.fields[1], defaultValue: 'javascript:evil' }] },
    { ...manifest, fields: [{ ...manifest.fields[3], options: [{ label: 'Missing value' }] }] },
    { ...manifest, fields: [manifest.fields[0], manifest.fields[0]] },
    { ...manifest, sections: [{ id: 'hero', label: 'Hero', group: 'unknown' }] },
    { ...manifest, fonts: [{ key: 'evil', label: 'Evil', family: 'Inter; background:url(evil)' }] },
    { ...manifest, builtinAssets: [{ id: 'image', label: 'Photo', src: '//evil.test/image.jpg', kind: 'image' }] },
  ]
  for (const input of cases) {
    const result = validateWebsiteManifest(input)
    assert.equal(result.ok, false)
    if (!result.ok) assert.ok(result.errors.every((issue) => issue.path.startsWith('$')))
    assert.equal(validatePreviewMessage(message('ready', { manifest: input }), 'child-to-parent').ok, false)
  }
})

test('bridge validation never invokes getters or accepts non-JSON payloads', () => {
  let calls = 0
  const payload = Object.defineProperty({}, 'manifest', { enumerable: true, get() { calls++; return manifest } })
  assert.equal(validatePreviewMessage(message('ready', payload), 'child-to-parent').ok, false)
  assert.equal(calls, 0)
  const circular: Record<string, unknown> = {}
  circular.value = circular
  assert.equal(validatePreviewMessage(message('edit', circular), 'child-to-parent').ok, false)
  assert.equal(validatePreviewMessage(message('edit', { key: 'hero.title', value: () => 'bad' }), 'child-to-parent').ok, false)
})
