import { parseMarker, parseSnapshot } from './cms-snapshot.mjs'

const NAVIGATION_COLLECTION = /^(?:header\.(?:actions|menus(?:\.[^.]+\.(?:featured|sections(?:\.[^.]+\.items)?))?)|footer\.(?:legal|columns(?:\.[^.]+\.items)?))$/
const NAVIGATION_HREF = /^(?:header\.(?:actions\.[^.]+|menus\.[^.]+(?:\.(?:featured\.[^.]+|sections\.[^.]+\.items\.[^.]+))?|github|logo)|footer\.(?:legal\.[^.]+|columns\.[^.]+\.items\.[^.]+|logo))\.href$/

/** Only explicit changes capable of removing a source navigation destination
 * relax the source navigation contract. Copy, colors, fonts, ordering, added
 * links, and homepage content cannot hide a broken source navigation build. */
export function cmsNavigationWasEdited(snapshotValue, markerValue) {
  const snapshot = parseSnapshot(snapshotValue)
  const marker = parseMarker(markerValue)
  if (snapshot.revision !== marker.revision || snapshot.contentHash !== marker.contentHash) {
    throw new Error('The SEO check snapshot does not match the built CMS revision.')
  }
  if (snapshot.revision === 0) return false
  const { document } = snapshot
  if (document.sections.hidden.some((id) => id === 'header' || id === 'footer')) return true
  if (Object.entries(document.collections).some(([key, patch]) => NAVIGATION_COLLECTION.test(key) && patch.hidden.length > 0)) return true
  return Object.entries(document.fields).some(([key, value]) => NAVIGATION_HREF.test(key)
    || (/^header\.menus\.[^.]+\.text$/.test(key) && typeof value === 'string' && !value.trim()))
}

/** Navigation discoverability is an editorial choice once its source links
 * were deliberately removed. Broken destinations remain failures in either
 * mode, as do the caller's independent metadata and rendered-content checks.
 * @param {{customized: boolean, missingHomeRoutes: string[], unreachableRoutes: string[], brokenDestinations?: string[]}} input
 */
export function assessNavigation({ customized, missingHomeRoutes, unreachableRoutes, brokenDestinations = [] }) {
  return {
    missingHomeRoutes: customized ? [] : missingHomeRoutes,
    unreachableRoutes: customized ? [] : unreachableRoutes,
    brokenDestinations,
    notices: customized ? { missingHomeRoutes, unreachableRoutes } : { missingHomeRoutes: [], unreachableRoutes: [] },
  }
}
