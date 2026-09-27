const PAGE_PATH = /^\/[a-z0-9]+(?:-[a-z0-9]+)*(?:\/[a-z0-9]+(?:-[a-z0-9]+)*){0,5}$/u
const BUILTIN = new Set(['/about', '/acceptable-use', '/blog', '/careers', '/changelog', '/contact', '/developer-policy', '/docs', '/pages', '/pricing', '/privacy', '/product', '/request-demo', '/signin', '/signup', '/solutions', '/status', '/terms'])
const REDIRECTED = new Set(['/product/twins', '/product/safe-state', '/product/firewall', '/product/load', '/product/migrations', '/product/crowdi', '/product/workload', '/product/exploratory-users', '/product/oracle', '/product/fidelity', '/product/report', '/product/architecture', '/product/change-intelligence'])

/** A new route cannot shadow the editor, the API, or generated assets. */
export function isAuthoredPagePath(path: unknown): path is string {
  return typeof path === 'string' && path.length <= 180 && PAGE_PATH.test(path) &&
    !BUILTIN.has(path) && !REDIRECTED.has(path) && !/^\/(?:admin|api|_next|cms-preview|cms-page-preview|cms-page-resolver)(?:\/|$)/u.test(path) && path !== '/blog/rss'
}
