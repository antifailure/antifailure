import type { MetadataRoute } from "next";
import { INDEXABLE_ROUTES } from "@/lib/routes";
import { absoluteUrl } from "@/lib/site";
import { contentLastModified } from "@/lib/lastmod";

/**
 * The sitemap.
 *
 * antifailure.dev/sitemap.xml returned 404 before this file existed, which
 * means every page on the site was found only by whatever links an engine
 * happened to follow. Generating it from the route registry rather than
 * writing the URLs out by hand is the point: a route that is added to the app
 * and not to the registry fails the SEO check, so it cannot quietly go
 * missing from here.
 *
 * `lastModified` comes from the git commit that last touched the files backing
 * each route, not from the build clock. A sitemap that stamps every URL with
 * "now" on every deploy is telling engines that all 31 pages changed, every
 * time, which teaches them to stop believing the field. Freshness only works
 * as a signal while it is true.
 */
export const dynamic = "force-static";

export default function sitemap(): MetadataRoute.Sitemap {
  return INDEXABLE_ROUTES.map((route) => ({
    url: absoluteUrl(route.path),
    lastModified: contentLastModified(route.path),
    changeFrequency:
      route.section === "legal"
        ? ("yearly" as const)
        : route.path === "/"
          ? ("weekly" as const)
          : ("monthly" as const),
    priority: route.priority,
  }));
}
