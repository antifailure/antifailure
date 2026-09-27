import { sitePageSlug } from './page.ts'
import type { AuthoredPage, RichTextDocument, RichTextNode, WebsiteDocument } from './types.ts'

export const emptyPageBody = (): RichTextDocument => ({ type: 'doc', content: [{ type: 'paragraph' }] })
export const pageContentKey = (path: string, field: string): string => `page.${sitePageSlug(path)}.${field}`

export interface AuthoredPageContent {
  title: string
  description: string
  summary: string
  body: RichTextDocument
  tags: string[]
  published: string
  updated: string
}

export function authoredPageContent(document: WebsiteDocument, page: AuthoredPage): AuthoredPageContent {
  const string = (field: string) => {
    const value = document.fields[pageContentKey(page.path, field)]
    return typeof value === 'string' ? value : ''
  }
  const value = document.fields[pageContentKey(page.path, 'body')]
  return {
    title: string('title'), description: string('description'), summary: string('summary'),
    body: value && typeof value === 'object' && value.type === 'doc' ? value : emptyPageBody(),
    tags: string('tags').split(',').map((tag) => tag.trim()).filter(Boolean),
    published: string('published'), updated: string('updated'),
  }
}

export function richTextLength(document: RichTextDocument): number {
  const read = (node: RichTextNode): number => node.type === 'text' ? node.text.trim().length :
    node.type === 'hardBreak' ? 0 : (node.content ?? []).reduce((total, child) => total + read(child), 0)
  return document.content.reduce((total, node) => total + read(node), 0)
}

const validDate = (value: string) => /^\d{4}-\d{2}-\d{2}$/u.test(value) &&
  !Number.isNaN(Date.parse(`${value}T00:00:00Z`)) && new Date(`${value}T00:00:00Z`).toISOString().slice(0, 10) === value

/** Drafts may be incomplete. Publication must not create empty public routes. */
export function unpublishablePages(document: WebsiteDocument): string[] {
  const errors: string[] = []
  for (const page of document.pages ?? []) {
    const content = authoredPageContent(document, page)
    if (!content.title.trim() || content.title.length > 180) errors.push(`${page.path}: add a page title.`)
    if (!content.description.trim() || content.description.length > 300) errors.push(`${page.path}: add a short description.`)
    if (!content.summary.trim() || content.summary.length > 300) errors.push(`${page.path}: add a one-line summary.`)
    if (!richTextLength(content.body)) errors.push(`${page.path}: write the page body before publishing.`)
    if (!validDate(content.published)) errors.push(`${page.path}: set a valid publication date.`)
    if (content.updated && !validDate(content.updated)) errors.push(`${page.path}: set a valid update date.`)
    if (page.kind === 'post') {
      if (!content.tags.length || content.tags.some((tag) => tag.length > 40)) errors.push(`${page.path}: add article tags.`)
    }
  }
  // Existing source articles use the same metadata keys but keep their body
  // and layout in source. An empty headline or malformed date must not turn a
  // previously indexable article into a broken page at publication.
  for (const [key, value] of Object.entries(document.fields)) {
    const match = /^page\.blog-[a-z0-9-]+\.(title|description|summary|tags|published|updated)$/u.exec(key)
    if (!match || typeof value !== 'string') continue
    if (match[1] !== 'updated' && !value.trim()) errors.push(`${key}: use a nonempty article value or reset to its source default.`)
    if ((match[1] === 'published' || (match[1] === 'updated' && Boolean(value))) && !validDate(value)) errors.push(`${key}: use a valid date.`)
  }
  return errors
}
