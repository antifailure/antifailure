import type { CustomShape, DividerVariant } from './types.ts'

/** Shapes are source-owned geometry selected by name. A field value never
 * becomes an SVG path, tag name, CSS expression, or raw HTML. */
export const CUSTOM_SHAPES: readonly CustomShape[] = Object.freeze(['circle', 'rectangle', 'arch', 'triangle', 'ring'])
export const DIVIDER_VARIANTS: readonly DividerVariant[] = Object.freeze(['line', 'dashed', 'dots', 'wave'])

export function isCustomShape(value: unknown): value is CustomShape {
  return typeof value === 'string' && CUSTOM_SHAPES.includes(value as CustomShape)
}

export function isDividerVariant(value: unknown): value is DividerVariant {
  return typeof value === 'string' && DIVIDER_VARIANTS.includes(value as DividerVariant)
}

export function resolveCustomShape(value: unknown): CustomShape {
  return isCustomShape(value) ? value : 'circle'
}

export function resolveDividerVariant(value: unknown): DividerVariant {
  return isDividerVariant(value) ? value : 'line'
}
