import type {
  FieldValue, MediaReference, OrderMove, ResponsiveStyle, StyleValues, WebsiteDocument,
} from './types.ts'
import { assertWebsiteDocument, emptyWebsiteDocument, isAssetId, isSafeFieldKey, isSafeIdentifier, parseFieldValue } from './validation.ts'

/** Stable JSON for equality and caller-owned hashing. Reject values that JSON
 * would otherwise silently drop or coerce. Never invokes toJSON or getters. */
export function stableStringify(value: unknown): string {
  const ancestors = new Set<unknown>()
  function encode(current: unknown): string {
    if (current === null) return 'null'
    if (typeof current === 'string' || typeof current === 'boolean') return JSON.stringify(current)
    if (typeof current === 'number' && Number.isFinite(current)) return JSON.stringify(current)
    if (!current || typeof current !== 'object') throw new TypeError('Only JSON values can be serialized.')
    if (ancestors.has(current)) throw new TypeError('Circular values cannot be serialized.')
    if (!Array.isArray(current) && ![Object.prototype, null].includes(Object.getPrototypeOf(current))) throw new TypeError('Only plain JSON objects can be serialized.')
    ancestors.add(current)
    let encoded: string
    if (Array.isArray(current)) {
      const entries: string[] = []
      for (let index = 0; index < current.length; index++) {
        const descriptor = Object.getOwnPropertyDescriptor(current, String(index))
        if (!descriptor || !('value' in descriptor)) throw new TypeError('Sparse arrays and accessors cannot be serialized.')
        entries.push(encode(descriptor.value))
      }
      encoded = `[${entries.join(',')}]`
    } else {
      const keys = Reflect.ownKeys(current)
      if (keys.some((key) => typeof key !== 'string')) throw new TypeError('Symbol keys cannot be serialized.')
      encoded = `{${(keys as string[]).sort().map((key) => {
        if (['__proto__', 'constructor', 'prototype'].includes(key)) throw new TypeError('Unsafe object key.')
        const descriptor = Object.getOwnPropertyDescriptor(current, key)
        if (!descriptor || !('value' in descriptor)) throw new TypeError('Accessors cannot be serialized.')
        return `${JSON.stringify(key)}:${encode(descriptor.value)}`
      }).join(',')}}`
    }
    ancestors.delete(current)
    return encoded
  }
  return encode(value)
}

function compatible(value: FieldValue, fallback: FieldValue): boolean {
  // A null source default is an optional field with no content yet.
  if (fallback === null) return true
  if (value === null) return typeof fallback === 'object' && fallback.type === 'media'
  if (typeof value !== typeof fallback) return false
  if (typeof fallback !== 'object' || typeof value !== 'object') return true
  return value.type === fallback.type
}

/** An override disappears from the rendered result if a component changes its
 * field type. Stale data remains available in the editor for explicit reset. */
export function resolveField(document: WebsiteDocument, key: string, defaultValue: MediaReference): MediaReference | null
export function resolveField<T extends FieldValue>(document: WebsiteDocument, key: string, defaultValue: T): T
export function resolveField(document: WebsiteDocument, key: string, defaultValue: FieldValue): FieldValue {
  if (!isSafeFieldKey(key) || !Object.hasOwn(document.fields, key)) return defaultValue
  const errors: Array<{ path: string; message: string }> = []
  const value = parseFieldValue(document.fields[key], key, errors)
  return value !== undefined && errors.length === 0 && compatible(value, defaultValue) ? value : defaultValue
}

/** Updating a field does not snapshot unrelated source defaults. Passing
 * undefined resets the field; passing its current source default also resets. */
export function setFieldOverride(
  document: WebsiteDocument, key: string, value: FieldValue | undefined, defaultValue?: FieldValue,
): WebsiteDocument {
  if (!isSafeFieldKey(key)) throw new TypeError('Unsafe field key.')
  const fields = { ...document.fields }
  if (value === undefined) delete fields[key]
  else {
    const errors: Array<{ path: string; message: string }> = []
    const parsed = parseFieldValue(value, key, errors)
    if (parsed === undefined || errors.length) throw new TypeError(`Invalid field value: ${errors.map((error) => error.message).join(' ')}`)
    if (defaultValue !== undefined && stableStringify(parsed) === stableStringify(defaultValue)) delete fields[key]
    else fields[key] = parsed
  }
  return { ...document, fields }
}

/** Each breakpoint is independent. No desktop styles are inherited on mobile;
 * a missing breakpoint falls back to that component's own responsive design. */
export function resolveStyle(
  document: WebsiteDocument, key: string, breakpoint: keyof ResponsiveStyle, defaults: StyleValues = {},
): StyleValues {
  const overrides = Object.hasOwn(document.styles, key) ? document.styles[key]?.[breakpoint] : undefined
  return { ...defaults, ...overrides }
}

/** Pass a property to reset just that control, a breakpoint to reset that
 * device, or neither to reset all explicit styles for the selected element. */
export function resetStyleOverride(
  document: WebsiteDocument, key: string, breakpoint?: keyof ResponsiveStyle, property?: keyof StyleValues,
): WebsiteDocument {
  if (!isSafeFieldKey(key)) throw new TypeError('Unsafe style key.')
  const styles = { ...document.styles }
  if (!breakpoint) delete styles[key]
  else if (Object.hasOwn(styles, key)) {
    const responsive = { ...styles[key] }
    if (!property) delete responsive[breakpoint]
    else {
      const values = { ...responsive[breakpoint] }
      delete values[property]
      if (Object.keys(values).length) responsive[breakpoint] = values
      else delete responsive[breakpoint]
    }
    if (Object.keys(responsive).length) styles[key] = responsive
    else delete styles[key]
  }
  return { ...document, styles }
}

export function setStyleOverride(
  document: WebsiteDocument, key: string, breakpoint: keyof ResponsiveStyle, property: keyof StyleValues,
  value: StyleValues[keyof StyleValues] | undefined,
): WebsiteDocument {
  if (!isSafeFieldKey(key)) throw new TypeError('Unsafe style key.')
  if (value === undefined) return resetStyleOverride(document, key, breakpoint, property)
  // Validate a small isolated document so each edit shares the server's bounds.
  const candidate = emptyWebsiteDocument()
  candidate.styles[key] = { [breakpoint]: { [property]: value } }
  const validated = assertWebsiteDocument(candidate)
  return {
    ...document,
    styles: {
      ...document.styles,
      [key]: {
        ...document.styles[key],
        [breakpoint]: { ...document.styles[key]?.[breakpoint], ...validated.styles[key]?.[breakpoint] },
      },
    },
  }
}

/** Moves describe adjacency rather than a saved array of source positions, so
 * newly shipped sections survive. Missing anchors retain source position (or
 * append custom items); cycles are ignored as a unit, then dependents resolve. */
export function resolveOrder(
  defaultIds: readonly string[], moves: readonly OrderMove[], customIds: readonly string[] = [], hidden: readonly string[] = [],
): string[] {
  const base = [...new Set([...defaultIds, ...customIds].filter(isSafeIdentifier))]
  const exists = new Set(base)
  const anchors = new Map<string, string | null>()
  const movePriority = new Map<string, number>()
  for (const [index, move] of moves.entries()) {
    if (!exists.has(move.id) || move.after === move.id || (move.after !== null && !exists.has(move.after))) continue
    anchors.set(move.id, move.after)
    movePriority.set(move.id, index)
  }
  const finished = new Set<string>()
  for (const id of base) {
    const path: string[] = []
    const position = new Map<string, number>()
    let current: string | null | undefined = id
    while (current != null && anchors.has(current) && !finished.has(current)) {
      const start = position.get(current)
      if (start !== undefined) {
        for (const cyclic of path.slice(start)) anchors.delete(cyclic)
        break
      }
      position.set(current, path.length)
      path.push(current)
      current = anchors.get(current)
    }
    for (const visited of path) finished.add(visited)
  }
  const children = new Map<string | null, string[]>()
  for (const id of base) {
    if (!anchors.has(id)) continue
    const anchor = anchors.get(id)!
    const siblings = children.get(anchor) ?? []
    siblings.push(id)
    children.set(anchor, siblings)
  }
  for (const siblings of children.values()) siblings.sort((a, b) => (movePriority.get(a) ?? 0) - (movePriority.get(b) ?? 0))
  const ordered: string[] = []
  const emitted = new Set<string>()
  const hiddenIds = new Set(hidden)
  function emit(id: string): void {
    if (emitted.has(id)) return
    emitted.add(id)
    if (!hiddenIds.has(id)) ordered.push(id)
    for (const child of children.get(id) ?? []) emit(child)
  }
  for (const id of children.get(null) ?? []) emit(id)
  for (const id of base) if (!anchors.has(id)) emit(id)
  // Defensive completeness for callers passing data that was not normalized.
  for (const id of base) emit(id)
  return ordered
}

/** Default items keep all source-owned properties. Existing fields resolve via
 * `${collectionKey}.${itemId}.${fieldName}`. A custom entry with a source ID is
 * a stale collision and cannot replace the source item. */
export function resolveCollection<T extends { id: string }>(
  document: WebsiteDocument, key: string, defaults: readonly T[],
): T[] {
  if (!isSafeFieldKey(key)) return defaults.map((item) => ({ ...item }))
  const patch = Object.hasOwn(document.collections, key) ? document.collections[key] : undefined
  const items = new Map<string, T>()
  const nestedFields = defaults.some((item) => Object.hasOwn(item, 'fields'))
  for (const item of defaults) {
    if (!isSafeIdentifier(item.id) || items.has(item.id)) continue
    const resolved: Record<string, unknown> = { ...item }
    const itemHasNestedFields = Object.hasOwn(item, 'fields') && typeof resolved.fields === 'object' && resolved.fields !== null
    const fields: Record<string, unknown> = itemHasNestedFields
      ? { ...resolved.fields as Record<string, unknown> }
      : resolved
    for (const [name, fallback] of Object.entries(fields)) {
      if (name === 'id' || !isSafeFieldKey(name)) continue
      const errors: Array<{ path: string; message: string }> = []
      const parsed = parseFieldValue(fallback, name, errors)
      if (parsed !== undefined && errors.length === 0) fields[name] = resolveField(document, `${key}.${item.id}.${name}`, parsed)
    }
    if (itemHasNestedFields) resolved.fields = fields
    items.set(item.id, resolved as T)
  }
  const customIds: string[] = []
  for (const custom of patch?.custom ?? []) {
    if (!isSafeIdentifier(custom.id) || items.has(custom.id)) continue
    const fields: Record<string, FieldValue> = {}
    for (const [name, fallback] of Object.entries(custom.fields)) {
      if (name === 'id' || !isSafeFieldKey(name)) continue
      fields[name] = resolveField(document, `${key}.${custom.id}.${name}`, fallback)
    }
    const item = nestedFields ? { id: custom.id, fields } : { ...fields, id: custom.id }
    items.set(custom.id, item as unknown as T)
    customIds.push(custom.id)
  }
  return resolveOrder(defaults.map((item) => item.id), patch?.moves ?? [], customIds, patch?.hidden ?? [])
    .flatMap((id) => items.has(id) ? [items.get(id)!] : [])
}

export function referencedAssets(document: WebsiteDocument): string[] {
  const assets = new Set<string>()
  function inspect(value: FieldValue): void {
    if (value && typeof value === 'object' && value.type === 'media' && value.source === 'asset' && isAssetId(value.assetId)) assets.add(value.assetId.toLowerCase())
  }
  for (const value of Object.values(document.fields)) inspect(value)
  for (const collection of Object.values(document.collections)) for (const item of collection.custom) for (const value of Object.values(item.fields)) inspect(value)
  for (const style of Object.values(document.styles)) {
    for (const breakpoint of ['desktop', 'tablet', 'mobile'] as const) {
      const family = style[breakpoint]?.fontFamily
      if (family?.startsWith('asset:') && isAssetId(family.slice(6))) assets.add(family.slice(6).toLowerCase())
    }
  }
  return [...assets].sort()
}
