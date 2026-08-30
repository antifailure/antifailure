import { execFileSync } from "node:child_process";
import { existsSync } from "node:fs";
import path from "node:path";
import { getPost, postModified } from "./blog";

/**
 * When the content behind a route last actually changed.
 *
 * This runs at build time only, from app/sitemap.ts. It exists because
 * `lastModified: new Date()` is the default nearly every sitemap ships with,
 * and it is a lie that costs something: it tells an engine that all thirty-one
 * pages changed on every deploy. Freshness is one of the few signals an answer
 * engine weighs directly, and it stops being a signal the moment it is always
 * true. Perplexity draws roughly half its citations from content under thirteen
 * weeks old, so being able to say truthfully that a page is recent is worth
 * more than saying it about everything.
 *
 * If git is unavailable or the checkout is shallow, this falls back to the
 * build time rather than failing the build. A slightly wrong date is better
 * than no sitemap.
 */

const ROOT = process.cwd();

/**
 * Source files that, if changed, mean the route's content changed.
 *
 * Deliberately NOT including lib/routes.ts, even though it holds every title
 * and description. It is one file shared by all thirty routes, so listing it
 * here would collapse every lastmod to the date that file was last touched and
 * reproduce exactly the "everything changed at once" lie this module exists to
 * avoid. A title edit not moving the date is the lesser error.
 */
function sourcesFor(routePath: string): string[] {
  const candidates: string[] = [];

  if (routePath === "/") {
    candidates.push("app/page.tsx", "components/home");
  } else if (routePath.startsWith("/product/")) {
    const slug = routePath.slice("/product/".length);
    // Twins -> Twins.tsx, safe-state -> SafeState.tsx, and so on.
    const component = slug
      .split("-")
      .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
      .join("");
    candidates.push(
      `components/pages/product/${component}.tsx`,
      "app/product/[slug]/page.tsx",
    );
  } else if (routePath === "/product") {
    candidates.push("app/product/page.tsx", "components/pages/product/Overview.tsx");
  } else if (routePath.startsWith("/solutions/")) {
    candidates.push(
      `components/pages/solutions/${routePath.slice("/solutions/".length)}.tsx`,
      "components/pages/solutions/Vertical.tsx",
      "app/solutions/[slug]/page.tsx",
    );
  } else if (routePath === "/solutions") {
    candidates.push("app/solutions/page.tsx", "components/pages/solutions/Hub.tsx");
  } else if (routePath === "/pricing") {
    candidates.push("app/pricing/page.tsx", "components/pages/company/Pricing.tsx");
  } else {
    candidates.push(`app${routePath}/page.tsx`);
  }

  return candidates.filter((c) => existsSync(path.join(ROOT, c)));
}

let warned = false;

export function contentLastModified(routePath: string): Date {
  // A post carries its own date. Asking git when content/blog/<file>.tsx last
  // changed would answer a different question, and the file name is not the
  // slug anyway: what-staging-misses.tsx publishes at
  // /blog/what-staging-misses-about-migrations. Before this branch existed
  // these three routes fell through to the build clock, which is the exact
  // thing this module was written to stop, and check:seo caught it.
  if (routePath.startsWith("/blog/")) {
    const post = getPost(routePath.slice("/blog/".length));
    if (post) return new Date(postModified(post));
  }

  const sources = sourcesFor(routePath);
  if (sources.length === 0) return new Date();

  try {
    const iso = execFileSync(
      "git",
      ["log", "-1", "--format=%cI", "--", ...sources],
      { cwd: ROOT, encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] },
    ).trim();

    if (!iso) return new Date();
    const when = new Date(iso);
    return Number.isNaN(when.getTime()) ? new Date() : when;
  } catch {
    if (!warned) {
      warned = true;
      // One line, not one per route. A shallow checkout is the usual cause:
      // actions/checkout fetches depth 1 by default, which leaves every file
      // looking like it changed in the same commit.
      console.warn(
        "[sitemap] git history unavailable; falling back to build time for lastModified. " +
          "Set fetch-depth: 0 on actions/checkout to get real dates.",
      );
    }
    return new Date();
  }
}
