/** Only explicit edits are stored. Values supplied by source components remain
 * the default, including when a later release changes those defaults. */
export interface WebsiteDocument {
  schemaVersion: 1
  sourceVersion?: string
  fields: Record<string, FieldValue>
  styles: Record<string, ResponsiveStyle>
  sections: {
    hidden: string[]
    moves: SectionMove[]
    custom: CustomSection[]
  }
  collections: Record<string, CollectionPatch>
}

export type FieldValue = string | number | boolean | null | RichTextDocument | MediaReference

export type RichTextMark =
  | { type: 'bold' | 'italic' | 'underline' | 'code' | 'emphasis' }
  | { type: 'link'; attrs: { href: string } }

export type RichTextInline =
  | { type: 'text'; text: string; marks?: RichTextMark[] }
  | { type: 'hardBreak' }

export interface RichTextParagraph {
  type: 'paragraph'
  content?: RichTextInline[]
}

export interface RichTextListItem {
  type: 'listItem'
  content: Array<RichTextParagraph | RichTextList>
}

export interface RichTextList {
  type: 'bulletList' | 'orderedList'
  content: RichTextListItem[]
}

export type RichTextNode = RichTextParagraph | RichTextList | RichTextListItem | RichTextInline

export interface RichTextDocument {
  type: 'doc'
  content: Array<RichTextParagraph | RichTextList>
}

export type MediaReference = {
  type: 'media'
  /** Uploaded URLs are UUID routes without file extensions. Preserve the
   * verified asset kind so renderers choose the correct media element. */
  kind?: 'image' | 'video'
  alt?: string
  decorative?: boolean
} & (
  | { source: 'asset'; assetId: string }
  | { source: 'builtin'; src: string }
)

export type CustomSectionKind = 'text' | 'image' | 'video' | 'split' | 'features' | 'cta' | 'spacer' | 'shape' | 'divider' | 'embed'
export type CustomShape = 'circle' | 'rectangle' | 'arch' | 'triangle' | 'ring'
export type DividerVariant = 'line' | 'dashed' | 'dots' | 'wave'
export type CustomSectionGroup = 'page' | 'hero'
export type SectionGroup = CustomSectionGroup | 'header' | 'footer'

export interface OrderMove {
  id: string
  after: string | null
}

export interface SectionMove extends OrderMove {
  group?: SectionGroup
}

export interface CustomSection extends OrderMove {
  kind: CustomSectionKind
  group: CustomSectionGroup
}

export interface CollectionPatch {
  hidden: string[]
  moves: OrderMove[]
  custom: Array<{ id: string; fields: Record<string, FieldValue> }>
}

/** Numbers are CSS pixels, except unitless lineHeight and opacity; fontWeight
 * is the standard 100..900 axis and focalX/focalY are percentages. */
export interface StyleValues {
  fontFamily?: string
  fontSize?: number
  fontWeight?: number
  lineHeight?: number
  letterSpacing?: number
  textAlign?: 'left' | 'center' | 'right' | 'justify'
  color?: string
  backgroundColor?: string
  paddingTop?: number
  paddingRight?: number
  paddingBottom?: number
  paddingLeft?: number
  marginTop?: number
  marginBottom?: number
  gap?: number
  maxWidth?: number
  minHeight?: number
  width?: number
  borderRadius?: number
  opacity?: number
  x?: number
  y?: number
  position?: 'relative' | 'absolute' | 'fixed'
  zIndex?: number
  layout?: 'default' | 'stack' | 'media-left' | 'media-right' | 'center'
  imageFit?: 'cover' | 'contain'
  focalX?: number
  focalY?: number
}

export interface ResponsiveStyle {
  desktop?: StyleValues
  tablet?: StyleValues
  mobile?: StyleValues
}

export interface FieldOption {
  label: string
  value: string | number | boolean
}

export interface FieldDefinition {
  key: string
  label: string
  kind: 'text' | 'richtext' | 'url' | 'media' | 'number' | 'boolean' | 'select' | 'code'
  /** Code is plain text for an isolated embed, never rich text or raw site HTML. */
  language?: 'html' | 'css' | 'javascript'
  sectionId: string
  group?: string
  defaultValue: FieldValue
  required?: boolean
  options?: FieldOption[]
  help?: string
}

export interface SectionDefinition {
  id: string
  label: string
  group: SectionGroup
  kind?: string
  inToc?: boolean
}

export interface CollectionDefinition {
  key: string
  label: string
  sectionId: string
  items: Array<{ id: string; label: string; fields?: Record<string, FieldValue> }>
}

export interface FontChoice {
  key: string
  label: string
  family: string
}

export interface WebsiteManifest {
  schemaVersion: 1
  sourceVersion: string
  fields: FieldDefinition[]
  sections: SectionDefinition[]
  collections: CollectionDefinition[]
  fonts: FontChoice[]
  builtinAssets: Array<{ id: string; label: string; src: string; kind: 'image' | 'video' }>
}

export interface ValidationIssue {
  path: string
  message: string
}

export type WebsiteValidationResult =
  | { ok: true; document: WebsiteDocument }
  | { ok: false; errors: ValidationIssue[] }

export interface WebsiteNormalizationResult {
  document: WebsiteDocument
  warnings: ValidationIssue[]
}

export interface PreviewSelection {
  key?: string
  sectionId?: string
}

export interface PreviewDocumentPayload {
  document: WebsiteDocument
  assetUrls: Record<string, string>
  mode: 'edit' | 'preview'
}

export interface PreviewEnvelope<T extends string, P> {
  protocol: 'antifailure-cms'
  version: 1
  session: string
  type: T
  payload: P
}

export type PreviewParentMessage =
  | PreviewEnvelope<'init' | 'update', PreviewDocumentPayload>
  | PreviewEnvelope<'select', PreviewSelection>

export type PreviewChildMessage =
  | PreviewEnvelope<'ready', { manifest: WebsiteManifest }>
  | PreviewEnvelope<'select', PreviewSelection>
  | PreviewEnvelope<'edit', { key: string; value: FieldValue }>
  | PreviewEnvelope<'error', { message: string }>

export type PreviewBridgeMessage = PreviewParentMessage | PreviewChildMessage
export type PreviewBridgeDirection = 'parent-to-child' | 'child-to-parent'

export type PreviewValidationResult<T extends PreviewBridgeMessage = PreviewBridgeMessage> =
  | { ok: true; message: T }
  | { ok: false; errors: ValidationIssue[] }

export type WebsiteManifestValidationResult =
  | { ok: true; manifest: WebsiteManifest }
  | { ok: false; errors: ValidationIssue[] }
