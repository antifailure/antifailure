import type { AuthoredPage, WebsiteDocument } from './types.ts'
import { resolveOrder } from './resolve.ts'
export { isAuthoredPagePath } from './authored-path.ts'

export function authoredPage(document: WebsiteDocument, path: string): AuthoredPage | undefined {
  return document.pages?.find((page) => page.path === path)
}

/** Trim path separators in one pass. A route can come from a request query,
 * so do not use an overlapping start/end regular expression here. */
export function sitePageSlug(path: string): string {
  let start = 0
  let end = path.length
  while (start < end && path.charCodeAt(start) === 47) start++
  while (end > start && path.charCodeAt(end - 1) === 47) end--
  return path.slice(start, end).replace(/[^a-zA-Z0-9_-]+/gu, '-') || 'home'
}

/** Stable, short namespace for blocks on a public page. The canonical path is
 * selected by the editor, so two routes never share authored blocks. */
export function pageBlockPrefix(path: string): string {
  let hash = 2166136261
  for (let index = 0; index < path.length; index += 1) hash = Math.imul(hash ^ path.charCodeAt(index), 16777619)
  return `custom-p${(hash >>> 0).toString(36)}-`
}

export function orderedPageBlockIds(document: WebsiteDocument, path: string): string[] {
  const pageId = `page-${sitePageSlug(path)}`
  const blocks = document.sections.custom.filter((section) => section.group === 'page' && section.id.startsWith(pageBlockPrefix(path)))
  const ids = blocks.map((section) => section.id)
  const initial = blocks.map(({ id, after }) => ({ id, after }))
  const moved = document.sections.moves.filter((move) => ids.includes(move.id))
  return resolveOrder([pageId], [...initial, ...moved], ids, document.sections.hidden).filter((id) => id !== pageId)
}

/** Send each inner page only its own edits and shared chrome. The source model
 * remains one atomic publication, while a docs reader does not download edits
 * for every other article on the site. */
export function projectWebsiteDocument(document: WebsiteDocument, path: string): WebsiteDocument {
  const page = sitePageSlug(path)
  const block = pageBlockPrefix(path)
  const blogIndex = path === '/blog'
  const related = path === '/' ? (key: string) =>
    !key.startsWith('page.') && !key.startsWith('page-') && !key.startsWith('custom-p') &&
    (!key.startsWith('seo.') || key.startsWith('seo.home.')) :
    (key: string) => key === 'global' || key === 'header' || key === 'footer' || key === `page-${page}` ||
      key.startsWith('header.') || key.startsWith('footer.') || key.startsWith(`page.${page}.`) || key.startsWith(`seo.${page}.`) || key.startsWith(block)
      || (blogIndex && key.startsWith('page.blog-'))
  const retain = <T>(items: Record<string, T>): Record<string, T> => Object.fromEntries(Object.entries(items).filter(([key]) => related(key)))
  return {
    ...document,
    fields: retain(document.fields),
    styles: retain(document.styles),
    collections: retain(document.collections),
    sections: {
      hidden: document.sections.hidden.filter(related),
      moves: document.sections.moves.filter((move) => related(move.id)),
      custom: document.sections.custom.filter((section) => path === '/' ? !section.id.startsWith('custom-p') : section.id.startsWith(block)),
    },
    ...(document.pages ? { pages: blogIndex ? document.pages.filter((page) => page.kind === 'post') : document.pages.filter((page) => page.path === path) } : {}),
  }
}
