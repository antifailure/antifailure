import type {
  CollectionPatch, CustomSection, FieldValue, MediaReference, OrderMove,
  ResponsiveStyle, RichTextDocument, RichTextMark, SectionGroup, SectionMove,
  StyleValues, ValidationIssue, WebsiteDocument, WebsiteNormalizationResult,
  WebsiteValidationResult,
} from './types.ts'

export const WEBSITE_LIMITS = Object.freeze({
  bytes: 1_048_576,
  fields: 1500,
  sections: 80,
  collectionItems: 80,
  collections: 80,
  richTextDepth: 10,
  textLength: 100_000,
})

const FORBIDDEN_KEYS = new Set(['__proto__', 'constructor', 'prototype'])
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i
const IDENTIFIER = /^[A-Za-z0-9][A-Za-z0-9_-]{0,119}$/
const SECTION_KINDS = new Set(['text', 'image', 'video', 'split', 'features', 'cta', 'spacer', 'shape', 'divider', 'embed'])
const SECTION_GROUPS = new Set(['page', 'hero', 'header', 'footer'])
const CONTROLS = /[\u0000-\u001f\u007f-\u009f]/u
const URL_UNSAFE = /[\s\\<>"`]/u
type Dict = Record<string, unknown>

export function isSafeIdentifier(value: unknown): value is string {
  return typeof value === 'string' && IDENTIFIER.test(value) && !FORBIDDEN_KEYS.has(value)
}

export function isSafeFieldKey(value: unknown): value is string {
  return typeof value === 'string' && value.length <= 240 && value.split('.').every(isSafeIdentifier)
}

export function isAssetId(value: unknown): value is string {
  return typeof value === 'string' && UUID.test(value)
}

/** Reject encoded controls, separators and dot segments as well as their plain
 * forms. Builtin images never turn into a remote request or a traversal path. */
export function safeBuiltinSource(value: unknown): value is string {
  if (typeof value !== 'string' || value.length > 2048 || !value.startsWith('/') || value.startsWith('//')) return false
  let decoded = value
  for (let i = 0; i < 4; i++) {
    if (CONTROLS.test(decoded) || URL_UNSAFE.test(decoded) || decoded.startsWith('//')) return false
    if (decoded.split(/[/?#]/u).some((segment) => segment === '.' || segment === '..')) return false
    let next: string
    try { next = decodeURIComponent(decoded) } catch { return false }
    if (next === decoded) return true
    decoded = next
  }
  return false
}

/** URLs are rendered through normal React attributes, never as HTML. This also
 * prevents JavaScript/data schemes and protocol-relative external links. */
export function safeHref(value: unknown): value is string {
  if (typeof value !== 'string' || value.length > 2048 || value.length === 0 || CONTROLS.test(value) || URL_UNSAFE.test(value)) return false
  let decoded = value
  for (let i = 0; i < 4; i++) {
    if (CONTROLS.test(decoded) || /[\\<>"`]/u.test(decoded)) return false
    let next: string
    try { next = decodeURIComponent(decoded) } catch { return false }
    if (next === decoded) break
    decoded = next
    if (i === 3) return false
  }
  if (value.startsWith('/')) return safeBuiltinSource(value)
  if (value.startsWith('#')) return value.length > 1 && !/[\s]/u.test(decoded)
  if (/^https?:\/\//iu.test(value)) {
    try {
      const url = new URL(value)
      return (url.protocol === 'https:' || url.protocol === 'http:') && !!url.hostname && !url.username && !url.password
    } catch { return false }
  }
  if (/^mailto:/iu.test(value)) return /^mailto:[^@\s?]+@[^@\s?]+(?:\?.*)?$/iu.test(value)
  if (/^tel:/iu.test(value)) return /^tel:\+?[0-9().-]{3,32}$/iu.test(value)
  return false
}

export function emptyWebsiteDocument(): WebsiteDocument {
  return { schemaVersion: 1, fields: {}, styles: {}, sections: { hidden: [], moves: [], custom: [] }, collections: {} }
}

function issue(errors: ValidationIssue[], path: string, message: string): void {
  // Keep malformed remote records from producing an unbounded response.
  if (errors.length < 200) errors.push({ path, message })
}

function object(value: unknown, path: string, errors: ValidationIssue[]): Dict | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value) || ![Object.prototype, null].includes(Object.getPrototypeOf(value))) {
    issue(errors, path, 'Expected a plain object.')
    return undefined
  }
  const result: Dict = Object.create(null) as Dict
  for (const key of Reflect.ownKeys(value)) {
    if (typeof key !== 'string' || FORBIDDEN_KEYS.has(key)) {
      issue(errors, path, 'Unsafe object key.')
      continue
    }
    const descriptor = Object.getOwnPropertyDescriptor(value, key)
    if (!descriptor || !('value' in descriptor)) {
      issue(errors, `${path}.${key}`, 'Accessor properties are not allowed.')
      continue
    }
    result[key] = descriptor.value
  }
  return result
}

function onlyKeys(value: Dict, allowed: readonly string[], path: string, errors: ValidationIssue[]): void {
  for (const key of Object.keys(value)) if (!allowed.includes(key)) issue(errors, `${path}.${key}`, 'Unknown property.')
}

function list(value: unknown, path: string, errors: ValidationIssue[], limit: number): unknown[] {
  if (!Array.isArray(value)) { issue(errors, path, 'Expected an array.'); return [] }
  if (value.length > limit) issue(errors, path, `At most ${limit} entries are allowed.`)
  const result: unknown[] = []
  for (let index = 0; index < Math.min(value.length, limit); index++) {
    const descriptor = Object.getOwnPropertyDescriptor(value, String(index))
    if (!descriptor || !('value' in descriptor)) issue(errors, `${path}[${index}]`, 'Sparse arrays and accessors are not allowed.')
    else result.push(descriptor.value)
  }
  return result
}

function identifier(value: unknown, path: string, errors: ValidationIssue[]): string | undefined {
  if (isSafeIdentifier(value)) return value
  issue(errors, path, 'Expected a stable identifier containing letters, numbers, hyphens or underscores.')
  return undefined
}

function parseHidden(value: unknown, path: string, errors: ValidationIssue[], limit: number): string[] {
  const found = new Set<string>()
  for (const [index, raw] of list(value, path, errors, limit).entries()) {
    const id = identifier(raw, `${path}[${index}]`, errors)
    if (id) {
      if (found.has(id)) issue(errors, `${path}[${index}]`, 'Duplicate identifier.')
      found.add(id)
    }
  }
  return [...found]
}

function parseMoves(value: unknown, path: string, errors: ValidationIssue[], section: true): SectionMove[]
function parseMoves(value: unknown, path: string, errors: ValidationIssue[], section?: false): OrderMove[]
function parseMoves(value: unknown, path: string, errors: ValidationIssue[], section = false): SectionMove[] {
  const moves: SectionMove[] = []
  const seen = new Set<string>()
  for (const [index, raw] of list(value, path, errors, WEBSITE_LIMITS.sections).entries()) {
    const location = `${path}[${index}]`
    const row = object(raw, location, errors)
    if (!row) continue
    onlyKeys(row, section ? ['id', 'after', 'group'] : ['id', 'after'], location, errors)
    const id = identifier(row.id, `${location}.id`, errors)
    const after = row.after === null ? null : identifier(row.after, `${location}.after`, errors)
    if (id === undefined || after === undefined) continue
    if (id === after) { issue(errors, location, 'An item cannot follow itself.'); continue }
    if (seen.has(id)) { issue(errors, `${location}.id`, 'Only one move per identifier is allowed.'); continue }
    const move: SectionMove = { id, after }
    if (section && row.group !== undefined) {
      if (typeof row.group !== 'string' || !SECTION_GROUPS.has(row.group)) { issue(errors, `${location}.group`, 'Unknown section group.'); continue }
      move.group = row.group as SectionGroup
    }
    seen.add(id)
    moves.push(move)
  }
  return moves
}

function parseMedia(raw: Dict, path: string, errors: ValidationIssue[]): MediaReference | undefined {
  const before = errors.length
  onlyKeys(raw, ['type', 'source', 'assetId', 'src', 'kind', 'alt', 'decorative'], path, errors)
  if (raw.kind !== undefined && raw.kind !== 'image' && raw.kind !== 'video') issue(errors, `${path}.kind`, 'Expected image or video.')
  if (raw.alt !== undefined && (typeof raw.alt !== 'string' || raw.alt.length > 2000)) issue(errors, `${path}.alt`, 'Alt text must be a string of at most 2000 characters.')
  if (raw.decorative !== undefined && typeof raw.decorative !== 'boolean') issue(errors, `${path}.decorative`, 'Expected a boolean.')
  let reference: MediaReference | undefined
  if (raw.source === 'asset') {
    if (!isAssetId(raw.assetId)) issue(errors, `${path}.assetId`, 'Expected an asset UUID.')
    else reference = { type: 'media', source: 'asset', assetId: raw.assetId.toLowerCase() }
    if (raw.src !== undefined) issue(errors, `${path}.src`, 'Asset references cannot include a source URL.')
  } else if (raw.source === 'builtin') {
    if (!safeBuiltinSource(raw.src)) issue(errors, `${path}.src`, 'Builtin media must use a safe root-relative path.')
    else reference = { type: 'media', source: 'builtin', src: raw.src }
    if (raw.assetId !== undefined) issue(errors, `${path}.assetId`, 'Builtin references cannot include an asset ID.')
  } else issue(errors, `${path}.source`, 'Unknown media source.')
  if (!reference || errors.length !== before) return undefined
  if (raw.kind === 'image' || raw.kind === 'video') reference.kind = raw.kind
  if (typeof raw.alt === 'string') reference.alt = raw.alt
  if (typeof raw.decorative === 'boolean') reference.decorative = raw.decorative
  return reference
}

function parseMark(value: unknown, path: string, errors: ValidationIssue[]): RichTextMark | undefined {
  const raw = object(value, path, errors)
  if (!raw) return undefined
  if (raw.type === 'link') {
    onlyKeys(raw, ['type', 'attrs'], path, errors)
    const attrs = object(raw.attrs, `${path}.attrs`, errors)
    if (!attrs) return undefined
    onlyKeys(attrs, ['href'], `${path}.attrs`, errors)
    if (!safeHref(attrs.href)) { issue(errors, `${path}.attrs.href`, 'Unsafe link URL.'); return undefined }
    return { type: 'link', attrs: { href: attrs.href } }
  }
  onlyKeys(raw, ['type'], path, errors)
  if (typeof raw.type === 'string' && ['bold', 'italic', 'underline', 'code', 'emphasis'].includes(raw.type)) return { type: raw.type as 'bold' | 'italic' | 'underline' | 'code' | 'emphasis' }
  issue(errors, `${path}.type`, 'Unsupported text mark.')
  return undefined
}

function parseRichText(raw: Dict, path: string, errors: ValidationIssue[]): RichTextDocument | undefined {
  const before = errors.length
  const ancestors = new Set<unknown>()
  let nodes = 0
  function node(value: unknown, at: string, depth: number, allowed: readonly string[]): Dict | undefined {
    if (++nodes > 5000 || depth > WEBSITE_LIMITS.richTextDepth) { issue(errors, at, 'Rich text exceeds its size or nesting limit.'); return undefined }
    if (ancestors.has(value)) { issue(errors, at, 'Circular content is not allowed.'); return undefined }
    const current = object(value, at, errors)
    if (!current) return undefined
    if (typeof current.type !== 'string' || !allowed.includes(current.type)) { issue(errors, `${at}.type`, 'Unsupported rich text node in this position.'); return undefined }
    ancestors.add(value)
    const result: Dict = { type: current.type }
    if (current.type === 'text') {
      onlyKeys(current, ['type', 'text', 'marks'], at, errors)
      if (typeof current.text !== 'string' || current.text.length > WEBSITE_LIMITS.textLength) issue(errors, `${at}.text`, 'Expected bounded text.')
      else result.text = current.text
      if (current.marks !== undefined) {
        const marks: RichTextMark[] = []
        const markTypes = new Set<string>()
        for (const [index, rawMark] of list(current.marks, `${at}.marks`, errors, 8).entries()) {
          const mark = parseMark(rawMark, `${at}.marks[${index}]`, errors)
          if (mark) {
            if (markTypes.has(mark.type)) issue(errors, `${at}.marks[${index}]`, 'Duplicate text mark.')
            else { markTypes.add(mark.type); marks.push(mark) }
          }
        }
        result.marks = marks
      }
    } else if (current.type === 'hardBreak') {
      onlyKeys(current, ['type'], at, errors)
    } else {
      onlyKeys(current, ['type', 'content'], at, errors)
      const childTypes = current.type === 'paragraph' ? ['text', 'hardBreak']
        : current.type === 'bulletList' || current.type === 'orderedList' ? ['listItem']
        : ['paragraph', 'bulletList', 'orderedList']
      if (current.content !== undefined || current.type !== 'paragraph') {
        result.content = list(current.content, `${at}.content`, errors, 5000)
          .map((child, index) => node(child, `${at}.content[${index}]`, depth + 1, childTypes)).filter(Boolean)
      }
    }
    ancestors.delete(value)
    return result
  }
  const result = node(raw, path, 0, ['doc'])
  return result && errors.length === before ? result as unknown as RichTextDocument : undefined
}

export function parseFieldValue(value: unknown, path: string, errors: ValidationIssue[]): FieldValue | undefined {
  if (value === null || typeof value === 'boolean') return value
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string' && value.length <= WEBSITE_LIMITS.textLength) return value
  if (!value || typeof value !== 'object') { issue(errors, path, 'Expected text, a finite number, a boolean, null, rich text or media.'); return undefined }
  const raw = object(value, path, errors)
  if (!raw) return undefined
  if (raw.type === 'media') return parseMedia(raw, path, errors)
  if (raw.type === 'doc') return parseRichText(raw, path, errors)
  issue(errors, path, 'Unknown field value shape.')
  return undefined
}

function parseFields(value: unknown, path: string, errors: ValidationIssue[], budget: { fields: number }): Record<string, FieldValue> {
  const result: Record<string, FieldValue> = {}
  const raw = object(value, path, errors)
  if (!raw) return result
  for (const [key, entry] of Object.entries(raw)) {
    if (++budget.fields > WEBSITE_LIMITS.fields) { issue(errors, path, `At most ${WEBSITE_LIMITS.fields} field overrides are allowed.`); break }
    if (!isSafeFieldKey(key)) { issue(errors, `${path}.${key}`, 'Unsafe field key.'); continue }
    const before = errors.length
    const parsed = parseFieldValue(entry, `${path}.${key}`, errors)
    if (parsed !== undefined && errors.length === before) result[key] = parsed
  }
  return result
}

export const STYLE_NUMBER_BOUNDS: Readonly<Record<string, readonly [number, number]>> = Object.freeze({
  fontSize: [8, 240], fontWeight: [100, 900], lineHeight: [0.8, 3], letterSpacing: [-10, 30],
  paddingTop: [0, 480], paddingRight: [0, 480], paddingBottom: [0, 480], paddingLeft: [0, 480],
  marginTop: [-240, 480], marginBottom: [-240, 480], gap: [0, 240], maxWidth: [0, 3840],
  minHeight: [0, 2160], width: [0, 3840], borderRadius: [0, 240], opacity: [0, 1],
  x: [-1200, 1200], y: [-1200, 1200], focalX: [0, 100], focalY: [0, 100],
  zIndex: [0, 100],
})
const STYLE_ENUMS: Readonly<Record<string, readonly string[]>> = Object.freeze({
  textAlign: ['left', 'center', 'right', 'justify'],
  layout: ['default', 'stack', 'media-left', 'media-right', 'center'], imageFit: ['cover', 'contain'],
  position: ['relative', 'absolute', 'fixed'],
})

function parseStyleValues(value: unknown, path: string, errors: ValidationIssue[]): StyleValues | undefined {
  const raw = object(value, path, errors)
  if (!raw) return undefined
  const result: Dict = {}
  for (const [key, entry] of Object.entries(raw)) {
    const bounds = Object.hasOwn(STYLE_NUMBER_BOUNDS, key) ? STYLE_NUMBER_BOUNDS[key] : undefined
    const values = Object.hasOwn(STYLE_ENUMS, key) ? STYLE_ENUMS[key] : undefined
    if (bounds) {
      if (typeof entry !== 'number' || !Number.isFinite(entry) || entry < bounds[0] || entry > bounds[1] || (key === 'zIndex' && !Number.isSafeInteger(entry))) issue(errors, `${path}.${key}`, `Expected ${key === 'zIndex' ? 'an integer' : 'a number'} from ${bounds[0]} to ${bounds[1]}.`)
      else result[key] = entry
    } else if (values) {
      if (typeof entry !== 'string' || !values.includes(entry)) issue(errors, `${path}.${key}`, 'Unknown style option.')
      else result[key] = entry
    } else if (key === 'color' || key === 'backgroundColor') {
      if (typeof entry !== 'string' || !/^#(?:[\da-f]{3}|[\da-f]{4}|[\da-f]{6}|[\da-f]{8})$/iu.test(entry)) issue(errors, `${path}.${key}`, 'Expected a hexadecimal color.')
      else result[key] = entry
    } else if (key === 'fontFamily') {
      if (typeof entry !== 'string' || !(isSafeIdentifier(entry as unknown) || (entry.startsWith('asset:') && isAssetId(entry.slice(6))))) issue(errors, `${path}.${key}`, 'Expected a font key or asset UUID.')
      else result[key] = entry.startsWith('asset:') ? entry.toLowerCase() : entry
    } else issue(errors, `${path}.${key}`, 'Unsupported style property.')
  }
  return result as StyleValues
}

function parseStyles(value: unknown, path: string, errors: ValidationIssue[]): Record<string, ResponsiveStyle> {
  const raw = object(value, path, errors)
  const result: Record<string, ResponsiveStyle> = {}
  if (!raw) return result
  const entries = Object.entries(raw)
  if (entries.length > WEBSITE_LIMITS.fields) issue(errors, path, 'Too many style targets.')
  for (const [key, style] of entries.slice(0, WEBSITE_LIMITS.fields)) {
    if (!isSafeFieldKey(key)) { issue(errors, `${path}.${key}`, 'Unsafe style key.'); continue }
    const responsive = object(style, `${path}.${key}`, errors)
    if (!responsive) continue
    onlyKeys(responsive, ['desktop', 'tablet', 'mobile'], `${path}.${key}`, errors)
    const parsed: ResponsiveStyle = {}
    for (const breakpoint of ['desktop', 'tablet', 'mobile'] as const) {
      if (responsive[breakpoint] === undefined) continue
      const values = parseStyleValues(responsive[breakpoint], `${path}.${key}.${breakpoint}`, errors)
      if (values && Object.keys(values).length) parsed[breakpoint] = values
    }
    if (Object.keys(parsed).length) result[key] = parsed
  }
  return result
}

function parseCustomSections(value: unknown, path: string, errors: ValidationIssue[]): CustomSection[] {
  const sections: CustomSection[] = []
  const seen = new Set<string>()
  for (const [index, entry] of list(value, path, errors, WEBSITE_LIMITS.sections).entries()) {
    const at = `${path}[${index}]`
    const raw = object(entry, at, errors)
    if (!raw) continue
    onlyKeys(raw, ['id', 'kind', 'group', 'after'], at, errors)
    if (typeof raw.id !== 'string' || !raw.id.startsWith('custom-') || !isAssetId(raw.id.slice(7))) { issue(errors, `${at}.id`, 'Custom sections require a custom-UUID identifier.'); continue }
    const id = raw.id.toLowerCase()
    if (seen.has(id)) { issue(errors, `${at}.id`, 'Duplicate custom section.'); continue }
    if (typeof raw.kind !== 'string' || !SECTION_KINDS.has(raw.kind)) { issue(errors, `${at}.kind`, 'Unknown custom section kind.'); continue }
    if (raw.group !== 'page' && raw.group !== 'hero') { issue(errors, `${at}.group`, 'Custom sections belong to page or hero.'); continue }
    const after = raw.after === null ? null : identifier(raw.after, `${at}.after`, errors)
    if (after === undefined) continue
    if (id === after) { issue(errors, `${at}.after`, 'A section cannot follow itself.'); continue }
    seen.add(id)
    sections.push({ id, kind: raw.kind as CustomSection['kind'], group: raw.group, after })
  }
  return sections
}

function parseCollections(value: unknown, path: string, errors: ValidationIssue[], budget: { fields: number }): Record<string, CollectionPatch> {
  const raw = object(value, path, errors)
  const result: Record<string, CollectionPatch> = {}
  if (!raw) return result
  const entries = Object.entries(raw)
  if (entries.length > WEBSITE_LIMITS.collections) issue(errors, path, 'Too many collections.')
  for (const [key, entry] of entries.slice(0, WEBSITE_LIMITS.collections)) {
    if (!isSafeFieldKey(key)) { issue(errors, `${path}.${key}`, 'Unsafe collection key.'); continue }
    const at = `${path}.${key}`
    const patch = object(entry, at, errors)
    if (!patch) continue
    onlyKeys(patch, ['hidden', 'moves', 'custom'], at, errors)
    const custom: CollectionPatch['custom'] = []
    const seen = new Set<string>()
    for (const [index, item] of list(patch.custom, `${at}.custom`, errors, WEBSITE_LIMITS.collectionItems).entries()) {
      const itemPath = `${at}.custom[${index}]`
      const row = object(item, itemPath, errors)
      if (!row) continue
      onlyKeys(row, ['id', 'fields'], itemPath, errors)
      const id = identifier(row.id, `${itemPath}.id`, errors)
      if (!id) continue
      if (seen.has(id)) { issue(errors, `${itemPath}.id`, 'Duplicate custom item.'); continue }
      seen.add(id)
      custom.push({ id, fields: parseFields(row.fields, `${itemPath}.fields`, errors, budget) })
    }
    result[key] = {
      hidden: parseHidden(patch.hidden, `${at}.hidden`, errors, WEBSITE_LIMITS.collectionItems),
      moves: parseMoves(patch.moves, `${at}.moves`, errors), custom,
    }
  }
  return result
}

/** Count JSON size without executing toJSON/accessor hooks. Shared references
 * are counted at each occurrence; circular references are rejected locally by
 * value parsing, without losing unrelated overrides on tolerant reads. */
function exceedsSize(value: unknown): boolean {
  let bytes = 0
  const ancestors = new Set<unknown>()
  const encoder = new TextEncoder()
  function visit(current: unknown, depth: number): void {
    if (bytes > WEBSITE_LIMITS.bytes || depth > 32 || ancestors.has(current)) return
    if (typeof current === 'string') { bytes += encoder.encode(JSON.stringify(current)).length; return }
    if (!current || typeof current !== 'object') { bytes += 16; return }
    ancestors.add(current)
    bytes += 2
    for (const key of Object.keys(current)) {
      bytes += encoder.encode(key).length + 4
      const descriptor = Object.getOwnPropertyDescriptor(current, key)
      if (descriptor && 'value' in descriptor) visit(descriptor.value, depth + 1)
      if (bytes > WEBSITE_LIMITS.bytes) break
    }
    ancestors.delete(current)
  }
  visit(value, 0)
  return bytes > WEBSITE_LIMITS.bytes
}

export function normalizeWebsiteDocument(input: unknown): WebsiteNormalizationResult {
  const document = emptyWebsiteDocument()
  const warnings: ValidationIssue[] = []
  const raw = object(input, '$', warnings)
  if (!raw) return { document, warnings }
  if (exceedsSize(raw)) {
    issue(warnings, '$', 'Document exceeds the 1 MiB limit.')
    // A global resource limit is the exception to per-entry read tolerance.
    return { document, warnings }
  }
  onlyKeys(raw, ['schemaVersion', 'sourceVersion', 'fields', 'styles', 'sections', 'collections'], '$', warnings)
  if (raw.schemaVersion !== 1) issue(warnings, '$.schemaVersion', 'Expected schema version 1.')
  if (raw.sourceVersion !== undefined) {
    if (typeof raw.sourceVersion !== 'string' || raw.sourceVersion.length > 200 || CONTROLS.test(raw.sourceVersion)) issue(warnings, '$.sourceVersion', 'Expected a source version of at most 200 characters.')
    else document.sourceVersion = raw.sourceVersion
  }
  const budget = { fields: 0 }
  document.fields = parseFields(raw.fields, '$.fields', warnings, budget)
  document.styles = parseStyles(raw.styles, '$.styles', warnings)
  const sections = object(raw.sections, '$.sections', warnings)
  if (sections) {
    onlyKeys(sections, ['hidden', 'moves', 'custom'], '$.sections', warnings)
    document.sections = {
      hidden: parseHidden(sections.hidden, '$.sections.hidden', warnings, WEBSITE_LIMITS.sections),
      moves: parseMoves(sections.moves, '$.sections.moves', warnings, true),
      custom: parseCustomSections(sections.custom, '$.sections.custom', warnings),
    }
  }
  document.collections = parseCollections(raw.collections, '$.collections', warnings, budget)
  return { document, warnings }
}

export function validateWebsiteDocument(input: unknown): WebsiteValidationResult {
  const { document, warnings } = normalizeWebsiteDocument(input)
  return warnings.length ? { ok: false, errors: warnings } : { ok: true, document }
}

export function assertWebsiteDocument(input: unknown): WebsiteDocument {
  const result = validateWebsiteDocument(input)
  if (result.ok) return result.document
  throw new Error(`Invalid website document: ${result.errors.map(({ path, message }) => `${path}: ${message}`).join('; ')}`)
}
