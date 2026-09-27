import type {
  FieldDefinition, PreviewBridgeDirection, PreviewChildMessage, PreviewParentMessage,
  PreviewValidationResult, ValidationIssue, WebsiteManifest, WebsiteManifestValidationResult,
} from './types.ts'
import { isAssetId, isSafeFieldKey, isSafeIdentifier, parseFieldValue, safeBuiltinSource, safeHref, validateWebsiteDocument, WEBSITE_LIMITS } from './validation.ts'
import { stableStringify } from './resolve.ts'

export const PREVIEW_PROTOCOL = 'antifailure-cms' as const
export const PREVIEW_PROTOCOL_VERSION = 1 as const
type Dict = Record<string, unknown>
type Check = (condition: unknown, path: string, message: string) => boolean

function inspect(input: unknown): { value?: Dict; errors: ValidationIssue[]; check: Check } {
  const errors: ValidationIssue[] = []
  const check: Check = (condition, path, message) => {
    if (!condition && errors.length < 200) errors.push({ path, message })
    return !!condition
  }
  try {
    const serialized = stableStringify(input)
    if (!check(new TextEncoder().encode(serialized).length <= WEBSITE_LIMITS.bytes * 2, '$', 'Preview payload exceeds 2 MiB.')) return { errors, check }
    if (!check(!!input && typeof input === 'object' && !Array.isArray(input), '$', 'Expected a plain object.')) return { errors, check }
    // Stable serialization has already rejected accessors, non-JSON values,
    // unsafe object keys, custom prototypes, and circular references.
    return { value: JSON.parse(serialized) as Dict, errors, check }
  } catch {
    check(false, '$', 'Expected safe JSON without circular references, accessors or unsafe keys.')
    return { errors, check }
  }
}

function record(value: unknown, path: string, check: Check): Dict | undefined {
  return check(value !== null && typeof value === 'object' && !Array.isArray(value), path, 'Expected an object.') ? value as Dict : undefined
}

function keys(value: Dict, allowed: readonly string[], path: string, check: Check): void {
  for (const key of Object.keys(value)) check(allowed.includes(key), `${path}.${key}`, 'Unknown property.')
}

function text(value: unknown, path: string, check: Check, max = 240, optional = false): value is string {
  if (optional && value === undefined) return false
  return check(typeof value === 'string' && value.length <= max && !/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/u.test(value), path, `Expected text of at most ${max} characters.`)
}

function entries(value: unknown, path: string, check: Check, max: number): unknown[] {
  if (!Array.isArray(value)) { check(false, path, 'Expected an array.'); return [] }
  check(value.length <= max, path, `At most ${max} entries are allowed.`)
  return value.slice(0, max)
}

function unique(seen: Set<string>, value: unknown, path: string, check: Check, isKey = false): void {
  if (!check(isKey ? isSafeFieldKey(value) : isSafeIdentifier(value), path, 'Expected a safe stable identifier.')) return
  const id = value as string
  check(!seen.has(id), path, 'Duplicate identifier.')
  seen.add(id)
}

function checkField(raw: Dict, path: string, errors: ValidationIssue[], check: Check): void {
  keys(raw, ['key', 'label', 'kind', 'language', 'sectionId', 'group', 'defaultValue', 'required', 'options', 'help'], path, check)
  check(isSafeFieldKey(raw.key), `${path}.key`, 'Unsafe field key.')
  check(isSafeIdentifier(raw.sectionId), `${path}.sectionId`, 'Unsafe section identifier.')
  text(raw.label, `${path}.label`, check)
  text(raw.group, `${path}.group`, check, 240, true)
  text(raw.help, `${path}.help`, check, 2000, true)
  if (raw.required !== undefined) check(typeof raw.required === 'boolean', `${path}.required`, 'Expected a boolean.')
  const kinds = ['text', 'richtext', 'url', 'media', 'number', 'boolean', 'select', 'code']
  check(typeof raw.kind === 'string' && kinds.includes(raw.kind), `${path}.kind`, 'Unknown field kind.')
  if (raw.language !== undefined) {
    check(raw.kind === 'code', `${path}.language`, 'Only code fields have a language.')
    check(raw.language === 'html' || raw.language === 'css' || raw.language === 'javascript', `${path}.language`, 'Expected html, css or javascript.')
  }
  const parsed = parseFieldValue(raw.defaultValue, `${path}.defaultValue`, errors)
  if (parsed !== undefined) {
    const kind = raw.kind as FieldDefinition['kind']
    if (kind === 'text') check(typeof parsed === 'string', `${path}.defaultValue`, 'Text fields require a text default.')
    if (kind === 'code') check(typeof parsed === 'string', `${path}.defaultValue`, 'Code fields require a plain text default.')
    if (kind === 'url') check(typeof parsed === 'string' && (parsed === '' || safeHref(parsed)), `${path}.defaultValue`, 'URL fields require a safe link or an empty default.')
    if (kind === 'richtext') check(typeof parsed === 'string' || (parsed !== null && typeof parsed === 'object' && parsed.type === 'doc'), `${path}.defaultValue`, 'Rich text fields require text or a rich text document.')
    if (kind === 'media') check(parsed === null || (typeof parsed === 'object' && parsed.type === 'media'), `${path}.defaultValue`, 'Media fields require a media reference or null.')
    if (kind === 'number' || kind === 'boolean') check(typeof parsed === kind, `${path}.defaultValue`, `Expected a ${kind} default.`)
    if (kind === 'select') check(['string', 'number', 'boolean'].includes(typeof parsed), `${path}.defaultValue`, 'Select fields require a scalar default.')
  }
  if (raw.options !== undefined) {
    const seen = new Set<string>()
    for (const [index, option] of entries(raw.options, `${path}.options`, check, 200).entries()) {
      const at = `${path}.options[${index}]`
      const item = record(option, at, check)
      if (!item) continue
      keys(item, ['label', 'value'], at, check)
      text(item.label, `${at}.label`, check)
      if (check(['string', 'number', 'boolean'].includes(typeof item.value), `${at}.value`, 'Expected a scalar option value.')) {
        const serialized = stableStringify(item.value)
        check(!seen.has(serialized), `${at}.value`, 'Duplicate option value.')
        seen.add(serialized)
      }
    }
  }
}

export function validateWebsiteManifest(input: unknown): WebsiteManifestValidationResult {
  const { value: raw, errors, check } = inspect(input)
  if (!raw) return { ok: false, errors }
  keys(raw, ['schemaVersion', 'sourceVersion', 'fields', 'sections', 'collections', 'fonts', 'builtinAssets'], '$', check)
  check(raw.schemaVersion === 1, '$.schemaVersion', 'Expected manifest schema version 1.')
  text(raw.sourceVersion, '$.sourceVersion', check, 200)
  const fieldKeys = new Set<string>()
  for (const [index, value] of entries(raw.fields, '$.fields', check, WEBSITE_LIMITS.fields).entries()) {
    const path = `$.fields[${index}]`
    const field = record(value, path, check)
    if (!field) continue
    unique(fieldKeys, field.key, `${path}.key`, check, true)
    checkField(field, path, errors, check)
  }
  const sectionIds = new Set<string>()
  for (const [index, value] of entries(raw.sections, '$.sections', check, WEBSITE_LIMITS.sections).entries()) {
    const path = `$.sections[${index}]`
    const section = record(value, path, check)
    if (!section) continue
    keys(section, ['id', 'label', 'group', 'kind', 'inToc'], path, check)
    unique(sectionIds, section.id, `${path}.id`, check)
    text(section.label, `${path}.label`, check)
    text(section.kind, `${path}.kind`, check, 120, true)
    check(['page', 'hero', 'header', 'footer'].includes(String(section.group)), `${path}.group`, 'Unknown section group.')
    if (section.inToc !== undefined) check(typeof section.inToc === 'boolean', `${path}.inToc`, 'Expected a boolean.')
  }
  const collectionKeys = new Set<string>()
  for (const [index, value] of entries(raw.collections, '$.collections', check, WEBSITE_LIMITS.collections).entries()) {
    const path = `$.collections[${index}]`
    const collection = record(value, path, check)
    if (!collection) continue
    keys(collection, ['key', 'label', 'sectionId', 'items'], path, check)
    unique(collectionKeys, collection.key, `${path}.key`, check, true)
    text(collection.label, `${path}.label`, check)
    check(isSafeIdentifier(collection.sectionId), `${path}.sectionId`, 'Unsafe section identifier.')
    const itemIds = new Set<string>()
    for (const [itemIndex, value] of entries(collection.items, `${path}.items`, check, WEBSITE_LIMITS.collectionItems).entries()) {
      const at = `${path}.items[${itemIndex}]`
      const item = record(value, at, check)
      if (!item) continue
      keys(item, ['id', 'label', 'fields'], at, check)
      unique(itemIds, item.id, `${at}.id`, check)
      text(item.label, `${at}.label`, check)
      if (item.fields !== undefined) {
        const fields = record(item.fields, `${at}.fields`, check)
        if (fields) for (const [key, field] of Object.entries(fields)) {
          check(isSafeFieldKey(key), `${at}.fields.${key}`, 'Unsafe field key.')
          parseFieldValue(field, `${at}.fields.${key}`, errors)
        }
      }
    }
  }
  const fontKeys = new Set<string>()
  for (const [index, value] of entries(raw.fonts, '$.fonts', check, 100).entries()) {
    const path = `$.fonts[${index}]`
    const font = record(value, path, check)
    if (!font) continue
    keys(font, ['key', 'label', 'family'], path, check)
    // Uploaded fonts are represented by asset keys in style overrides. Manifest
    // fonts are the predefined source-owned choices only.
    unique(fontKeys, font.key, `${path}.key`, check)
    text(font.label, `${path}.label`, check)
    check(typeof font.family === 'string' && font.family.length <= 240 && /^[A-Za-z0-9_'", -]+$/u.test(font.family), `${path}.family`, 'Expected a safe CSS font family list.')
  }
  const builtinIds = new Set<string>()
  for (const [index, value] of entries(raw.builtinAssets, '$.builtinAssets', check, 500).entries()) {
    const path = `$.builtinAssets[${index}]`
    const asset = record(value, path, check)
    if (!asset) continue
    keys(asset, ['id', 'label', 'src', 'kind'], path, check)
    unique(builtinIds, asset.id, `${path}.id`, check)
    text(asset.label, `${path}.label`, check)
    check(safeBuiltinSource(asset.src), `${path}.src`, 'Expected a safe root-relative builtin media path.')
    check(asset.kind === 'image' || asset.kind === 'video', `${path}.kind`, 'Expected image or video.')
  }
  return errors.length ? { ok: false, errors } : { ok: true, manifest: raw as unknown as WebsiteManifest }
}

export function validatePreviewMessage(input: unknown, direction: 'parent-to-child'): PreviewValidationResult<PreviewParentMessage>
export function validatePreviewMessage(input: unknown, direction: 'child-to-parent'): PreviewValidationResult<PreviewChildMessage>
export function validatePreviewMessage(input: unknown, direction: PreviewBridgeDirection): PreviewValidationResult
export function validatePreviewMessage(input: unknown, direction: PreviewBridgeDirection): PreviewValidationResult {
  const { value: raw, errors, check } = inspect(input)
  if (!raw) return { ok: false, errors }
  keys(raw, ['protocol', 'version', 'session', 'type', 'payload'], '$', check)
  check(raw.protocol === PREVIEW_PROTOCOL, '$.protocol', 'Unknown preview protocol.')
  check(raw.version === PREVIEW_PROTOCOL_VERSION, '$.version', 'Unsupported preview protocol version.')
  check(isAssetId(raw.session), '$.session', 'Expected a session UUID.')
  check(direction === 'parent-to-child' || direction === 'child-to-parent', '$', 'Unknown message direction.')
  const allowed = direction === 'parent-to-child' ? ['init', 'update', 'select'] : ['ready', 'select', 'edit', 'error']
  check(typeof raw.type === 'string' && allowed.includes(raw.type), '$.type', 'Message type is not permitted in this direction.')
  const payload = record(raw.payload, '$.payload', check)
  if (payload) {
    if (raw.type === 'init' || raw.type === 'update') {
      keys(payload, ['document', 'assetUrls', 'mode'], '$.payload', check)
      const document = validateWebsiteDocument(payload.document)
      if (!document.ok) errors.push(...document.errors.map((error) => ({ ...error, path: `$.payload.document${error.path.slice(1)}` })))
      else payload.document = document.document
      check(payload.mode === 'edit' || payload.mode === 'preview', '$.payload.mode', 'Expected edit or preview mode.')
      const assets = record(payload.assetUrls, '$.payload.assetUrls', check)
      if (assets) {
        check(Object.keys(assets).length <= 500, '$.payload.assetUrls', 'Too many asset URLs.')
        for (const [id, url] of Object.entries(assets)) {
          check(isAssetId(id), `$.payload.assetUrls.${id}`, 'Expected an asset UUID.')
          check(typeof url === 'string' && (safeBuiltinSource(url) || (/^https?:\/\//iu.test(url) && safeHref(url))), `$.payload.assetUrls.${id}`, 'Expected a safe HTTP(S) or root-relative asset URL.')
        }
      }
    } else if (raw.type === 'select') {
      keys(payload, ['key', 'sectionId'], '$.payload', check)
      if (payload.key !== undefined) check(isSafeFieldKey(payload.key), '$.payload.key', 'Unsafe field key.')
      if (payload.sectionId !== undefined) check(isSafeIdentifier(payload.sectionId), '$.payload.sectionId', 'Unsafe section identifier.')
      // An empty selection explicitly clears the inspector selection.
    } else if (raw.type === 'ready') {
      keys(payload, ['manifest'], '$.payload', check)
      const manifest = validateWebsiteManifest(payload.manifest)
      if (!manifest.ok) errors.push(...manifest.errors.map((error) => ({ ...error, path: `$.payload.manifest${error.path.slice(1)}` })))
      else payload.manifest = manifest.manifest
    } else if (raw.type === 'edit') {
      keys(payload, ['key', 'value'], '$.payload', check)
      check(isSafeFieldKey(payload.key), '$.payload.key', 'Unsafe field key.')
      const value = parseFieldValue(payload.value, '$.payload.value', errors)
      if (value !== undefined) payload.value = value
    } else if (raw.type === 'error') {
      keys(payload, ['message'], '$.payload', check)
      text(payload.message, '$.payload.message', check, 2000)
    }
  }
  return errors.length ? { ok: false, errors } : { ok: true, message: raw as unknown as PreviewParentMessage | PreviewChildMessage }
}
