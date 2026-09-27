import type { WebsiteDocument } from './types.ts'

/** Stable, short namespace for blocks on a public page. The canonical path is
 * selected by the editor, so two routes never share authored blocks. */
export function pageBlockPrefix(path: string): string {
  let hash = 2166136261
  for (let index = 0; index < path.length; index += 1) hash = Math.imul(hash ^ path.charCodeAt(index), 16777619)
  return `custom-p${(hash >>> 0).toString(36)}-`
}

/** Send each inner page only its own edits and shared chrome. The source model
 * remains one atomic publication, while a docs reader does not download edits
 * for every other article on the site. */
export function projectWebsiteDocument(document: WebsiteDocument, path: string): WebsiteDocument {
  if (path === '/') return document
  const page = path.replace(/^\/+|\/+$/gu, '').replace(/[^a-zA-Z0-9_-]+/gu, '-')
  const block = pageBlockPrefix(path)
  const related = (key: string) => key === 'global' || key === 'header' || key === 'footer' || key === `page-${page}` ||
    key.startsWith('header.') || key.startsWith('footer.') || key.startsWith(`page.${page}.`) || key.startsWith(`seo.${page}.`) || key.startsWith(block)
  const retain = <T>(items: Record<string, T>): Record<string, T> => Object.fromEntries(Object.entries(items).filter(([key]) => related(key)))
  return {
    ...document,
    fields: retain(document.fields),
    styles: retain(document.styles),
    collections: retain(document.collections),
    sections: {
      hidden: document.sections.hidden.filter(related),
      moves: document.sections.moves.filter((move) => related(move.id)),
      custom: document.sections.custom.filter((section) => section.id.startsWith(block)),
    },
  }
}
