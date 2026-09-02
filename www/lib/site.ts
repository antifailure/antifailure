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

/**
 * The origin the hosted control plane is served from.
 *
 * This is where "Continue with GitHub" sends a visitor, so it is the primary
 * conversion path off this site. It was a bare constant in AuthScreen.tsx
 * reading `https://app.dev.antifailure.dev`, the staging deployment, which
 * carries a different OAuth application and a different database: every
 * invited person who clicked sign in on the marketing site landed on staging.
 *
 * It follows SITE_URL rather than being a bare constant, and for the same
 * reason: the fallback is production, so a plain `next build` with nothing
 * configured produces the correct link and cannot emit "undefined/auth/github".
 * Only a build that deliberately sets the variable points anywhere else.
 */
export const CONTROL_PLANE_URL = (
  process.env.NEXT_PUBLIC_CONTROL_PLANE_URL ?? "https://app.antifailure.dev"
).replace(/\/$/, "");

/** The product name, spelled and cased exactly one way, everywhere. */
export const SITE_NAME = "Antifailure";

/**
 * What joins a page's own name to the site's name in a <title>.
 *
 * It was an em dash, in every one of the twenty-three route titles, the
 * layout's title template, the OpenGraph card, and five separate regular
 * expressions that stripped it back off again. That is the character this project bans in prose everywhere else,
 * and a title is not an exception to prose: it is the line a reader sees in the
 * tab, in the search result and in a pasted link, before any sentence on the
 * page itself. A middle dot separates without pretending to be punctuation.
 *
 * Everything that writes or reads a title goes through the two functions below,
 * so this is the only place the character appears at all.
 */
export const TITLE_SEPARATOR = " · ";

/** A page's <title>: its own name, then the site's. */
export function pageTitle(name: string): string {
  return `${name}${TITLE_SEPARATOR}${SITE_NAME}`;
}

/**
 * A page's own name, with the site name taken back off.
 *
 * For a breadcrumb, a link list, or the heading of a page's markdown twin,
 * where repeating the site name on every line reads like a stack of browser
 * tabs. A title that does not end in the suffix, the home page's among them, is
 * returned whole.
 */
export function titleName(title: string): string {
  const suffix = `${TITLE_SEPARATOR}${SITE_NAME}`;
  return title.endsWith(suffix) ? title.slice(0, -suffix.length) : title;
}

/**
 * What the site is called in a browser tab when a page has no title of its own.
 *
 * The one title written the other way round, name first, because on the home
 * page the name is the thing being introduced rather than the thing being told
 * apart from other things with the same name.
 */
export const SITE_TITLE = "Antifailure: know what happens before you deploy";

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
 *
 * "builds your services and then runs them inside a sandbox" is worded that
 * way on purpose and the word order is the claim. It used to say "builds and
 * runs your services inside a sandbox that cannot reach the internet", which
 * put the build inside the sandbox and was false: `ImageBuildOptions` in
 * engine/internal/build/docker.go sets no NetworkMode, and the buildpack path
 * runs `npm ci` and `pip install`, so a build necessarily has a route out. The
 * containment is real and adversarially tested, and it applies to the running
 * services. This string is read by search engines and by models through
 * JSON-LD and llms.txt, so it is the one place a loose word travels furthest.
 */
export const SITE_DESCRIPTION_LONG =
  "Antifailure builds a disposable copy of your production stack for every pull request. It branches a masked, referentially consistent Postgres from a verified golden, builds your services and then runs them inside a sandbox that cannot reach the internet except where you say it can, drives real workflows with agents that use the accessibility tree the way a person does, and rehearses pending migrations for exclusive locks, table rewrites and query plan regressions before any of it reaches production.";

/** The category, stated outright rather than implied, so an engine can place it. */
export const SITE_CATEGORY = "Pre-production testing and release safety for teams that deploy to Postgres";

export const REPO_URL = "https://github.com/antifailure/antifailure";
export const REPO_SLUG = "antifailure/antifailure";
export const DOCS_URL = `${SITE_URL}/docs`;

/** Public contact routes checked against the live repository and site. The
 * domain currently has no MX records, so no email address is presented as a
 * working channel. */
export const CONTACT_POINTS = [
  {
    id: "security",
    label: "Private security reports",
    url: `${REPO_URL}/security/advisories/new`,
    contactType: "security",
  },
  {
    id: "issues",
    label: "Bugs and feature requests",
    url: `${REPO_URL}/issues/new/choose`,
    contactType: "technical support",
  },
  {
    id: "discussions",
    label: "Questions and ideas",
    url: `${REPO_URL}/discussions`,
    contactType: "community support",
  },
  {
    id: "waitlist",
    label: "Hosted product interest",
    url: "/signup",
    contactType: "early access",
  },
] as const;

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
  alt: "Antifailure: a disposable copy of your production stack for every pull request",
} as const;
