/** Stable namespace for a public path. Creation rejects a second path that
 * would map to the same slug, so existing sitewide editor keys stay intact. */
export function sitePageSlug(path: string): string {
  let start = 0
  let end = path.length
  while (start < end && path.charCodeAt(start) === 47) start++
  while (end > start && path.charCodeAt(end - 1) === 47) end--
  return path.slice(start, end).replace(/[^a-zA-Z0-9_-]+/gu, '-') || 'home'
}
