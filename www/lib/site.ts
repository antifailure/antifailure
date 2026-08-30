/**
 * One description, one name, one origin.
 *
 * Before this file the product described itself four different ways: the site
 * called it "a disposable production twin that proves whether a deployment is
 * safe before it ships", the GitHub About said "sandboxed third-party APIs, AI
 * agents that run real user flows, and load replay", and the README and the
 * documentation each said a third and fourth thing. All four are accurate and
 * that is the problem. An engine trying to resolve "Antifailure" to a single
 * entity sees four products.
 *
 * So the boilerplate below is the only sentence, and everything that needs to
 * describe the product reads it from here: page metadata, OpenGraph, the
 * JSON-LD Organization, llms.txt, and the repository's own About field. If it
 * changes it changes in one place.
 */

/**
 * The origin the site is served from.
 *
 * Every canonical, OpenGraph, sitemap and JSON-LD `@id` URL is built from this,
 * so it has to be absolute and it has to have no trailing slash. The build
 * falls back to production, which means a plain `next build` produces correct
 * absolute URLs without anybody configuring anything.
 */
export const SITE_URL = (
  process.env.NEXT_PUBLIC_SITE_URL ?? "https://antifailure.dev"
).replace(/\/$/, "");

/** The product name, spelled and cased exactly one way, everywhere. */
export const SITE_NAME = "Antifailure";

/** What the site is called in a browser tab when a page has no title of its own. */
export const SITE_TITLE = "Antifailure — Know what happens before you deploy";

/**
 * The canonical one-line description. 152 characters, so it survives intact in
 * a search result and in a link preview instead of being cut mid-clause.
 *
 * This exact string belongs in the GitHub repository About field and in
 * docs/astro.config.mjs too. `npm run check:identity` fails if they drift.
 */
export const SITE_DESCRIPTION =
  "A disposable copy of your production stack for every pull request: masked Postgres, contained third-party APIs, and agents that use your app like people.";

/**
 * The longer form, for places with room to be specific: the JSON-LD
 * Organization description, llms.txt, and the documentation landing page.
 */
export const SITE_DESCRIPTION_LONG =
  "Antifailure builds a disposable copy of your production stack for every pull request. It branches a masked, referentially consistent Postgres from a verified golden, builds and runs your services inside a sandbox that cannot reach the internet except where you say it can, drives real workflows with agents that use the accessibility tree the way a person does, and rehearses pending migrations for locks, plan regressions and rollback feasibility before any of it reaches production.";

/** The category, stated outright rather than implied, so an engine can place it. */
export const SITE_CATEGORY = "Pre-production testing and release safety for teams that deploy to Postgres";

export const REPO_URL = "https://github.com/antifailure/antifailure";
export const REPO_SLUG = "antifailure/antifailure";
export const DOCS_URL = `${SITE_URL}/docs`;

/**
 * Every profile the project owns, in one array.
 *
 * This becomes `sameAs` on the Organization node, which is the single highest
 * leverage entity signal available: it is how a knowledge graph confirms that
 * the antifailure.dev in a search result, the antifailure/antifailure on
 * GitHub, and the Antifailure somebody asked an assistant about are one thing.
 *
 * Only add a URL here once the profile actually exists and resolves. A sameAs
 * pointing at a 404 is worse than a short list.
 */
export const SAME_AS: readonly string[] = [REPO_URL];

/** Absolute URL for a site-relative path. Accepts "/" and "/product/twins". */
export function absoluteUrl(pathname: string): string {
  if (pathname === "/") return SITE_URL;
  return `${SITE_URL}${pathname.startsWith("/") ? pathname : `/${pathname}`}`;
}

/** The social card. A real file at a real path, checked by the build. */
export const OG_IMAGE = {
  url: "/og.png",
  width: 1200,
  height: 630,
  alt: "Antifailure — a disposable copy of your production stack for every pull request",
} as const;
