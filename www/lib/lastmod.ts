import { execFileSync } from "node:child_process";
import { existsSync } from "node:fs";
import path from "node:path";
import { getPost, postModified } from "./blog";
import { changelogModified } from "./changelog";

/**
 * When the content behind a route last actually changed.
 *
 * This runs at build time only, from app/sitemap.ts. It exists because
 * `lastModified: new Date()` is the default nearly every sitemap ships with,
 * and it is a lie that costs something: it tells an engine that every page
 * changed on every deploy. Freshness is one of the few signals an answer
 * engine weighs directly, and it stops being a signal the moment it is always
 * true. Perplexity draws roughly half its citations from content under thirteen
 * weeks old, so being able to say truthfully that a page is recent is worth
 * more than saying it about everything.
 *
 * If git cannot answer, this fails the build in CI and warns locally. The
 * earlier version fell back to the build clock on the reasoning that a
 * slightly wrong date beats no sitemap, and that was the wrong trade: the
 * fallback is silent, it is indistinguishable downstream from a real date, and
 * it produces exactly the everything-changed-today lie this module exists to
 * remove. A shallow checkout is a one line fix in the workflow, so the build
 * should say so rather than quietly ship the thing it was written to prevent.
 *
 * Locally it stays a warning, because a file that has never been committed
 * legitimately has no history and stopping `next build` on that would make the
 * site impossible to preview while writing a new page.
 */

const ROOT = process.cwd();

/**
 * Source files that, if changed, mean the route's content changed.
 *
 * Deliberately NOT including lib/routes.ts, even though it holds every title
 * and description. It is one file shared by every route, so listing it
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
  } else if (routePath.startsWith("/blog/")) {
    const slug = routePath.slice("/blog/".length);
    candidates.push(`content/blog/${slug}.tsx`, "app/blog/[slug]/page.tsx");
  } else if (routePath === "/blog") {
    candidates.push("app/blog/page.tsx", "lib/blog.ts");
  } else if (routePath === "/changelog") {
    candidates.push("app/changelog/page.tsx", "lib/changelog.ts");
  } else if (routePath === "/pricing") {
    candidates.push("app/pricing/page.tsx", "components/pages/company/Pricing.tsx");
  } else if (routePath === "/signin" || routePath === "/signup") {
    candidates.push(`app${routePath}/page.tsx`, "components/AuthScreen.tsx");
  } else {
    // The legal pages, all six of which are components/pages/company/Legal.tsx.
    //
    // Three lib/*-content files used to be listed across this function, for
    // the home page, the solutions pages and everything falling through to
    // here. None of them had a single importer once the pages moved to
    // components/pages, so the dates came from files nothing renders and a
    // commit touching only dead content would have moved them. They are
    // deleted rather than merely unlisted; sourcesFor filters by existsSync,
    // so an entry naming a deleted file drops out in silence, which is how
    // this survived being noticed.
    candidates.push(`app${routePath}/page.tsx`, "components/pages/company/Legal.tsx");
  }

  return candidates.filter((c) => existsSync(path.join(ROOT, c)));
}

let warned = false;

/**
 * CI is the place where a wrong date ships. A developer's `next build` is a
 * preview; the workflow's is what the world reads, and it is also the only
 * place a shallow checkout happens, because actions/checkout defaults to
 * depth 1.
 */
const STRICT = process.env.CI === "true" || process.env.CI === "1";

/**
 * A shallow checkout, asked directly rather than inferred.
 *
 * This is the case worth naming, and every guard before it missed. With
 * `actions/checkout`'s default depth of 1 git is present and answers happily:
 * there is exactly one commit, every file looks like it was added in it, and
 * `git log -1` hands back that commit's date for all of them. Nothing throws
 * and nothing is empty. What ships is one date on every route, which is the
 * everything-changed-today lie wearing a real timestamp, and no check
 * downstream can tell it from the truth. check-seo.mjs compares each stamp
 * against the newest commit, and under depth 1 they are equal to it.
 *
 * `--is-shallow-repository` is the question that actually distinguishes them.
 */
function isShallow(): boolean {
  try {
    return (
      execFileSync("git", ["rev-parse", "--is-shallow-repository"], {
        cwd: ROOT,
        encoding: "utf8",
        stdio: ["ignore", "pipe", "ignore"],
      }).trim() === "true"
    );
  } catch {
    // Git could not answer at all, which contentLastModified reports itself
    // with a message naming the route.
    return false;
  }
}

let shallowChecked = false;
let shallow = false;

function unavailable(routePath: string, why: string): Date {
  const guidance =
    `no usable commit history for ${routePath} (${why}).\n` +
    "The sitemap's lastmod would fall back to the build clock, which tells " +
    "every engine that every page changed today. Set fetch-depth: 0 on " +
    "actions/checkout so the build can read real dates.";

  if (STRICT) throw new Error(guidance);

  if (!warned) {
    warned = true;
    console.warn(`[sitemap] ${guidance}`);
  }
  return new Date();
}

export function contentLastModified(routePath: string): Date {
  // A post carries its own date, so this answers without git at all and runs
  // ahead of the shallow check. Asking git when content/blog/<file>.tsx last
  // changed would answer a different question, and the file name is not the
  // slug anyway: what-staging-misses.tsx publishes at
  // /blog/what-staging-misses-about-migrations. Before this branch existed
  // these three routes fell through to the build clock, which is the exact
  // thing this module was written to stop, and check:seo caught it.
  // The changelog's content is the fragments, not the page that renders them,
  // and the newest fragment is the only thing about it that has changed. Asking
  // git when app/changelog/page.tsx last moved would stamp the page with the
  // date of a styling edit and say nothing changed on a day forty entries
  // landed.
  if (routePath === "/changelog") {
    const newest = changelogModified();
    if (newest) return newest;
  }

  if (routePath.startsWith("/blog/")) {
    const post = getPost(routePath.slice("/blog/".length));
    if (post) return new Date(postModified(post));
  }

  if (!shallowChecked) {
    shallowChecked = true;
    shallow = isShallow();
  }
  if (shallow) {
    return unavailable(
      routePath,
      "this is a shallow checkout, so every file looks like it was added in the one commit that exists",
    );
  }

  const sources = sourcesFor(routePath);
  if (sources.length === 0) return unavailable(routePath, "no source file matched this route");

  let iso: string;
  try {
    iso = execFileSync(
      "git",
      ["log", "-1", "--format=%cI", "--", ...sources],
      { cwd: ROOT, encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] },
    ).trim();
  } catch {
    return unavailable(routePath, "git is not available here");
  }

  if (!iso) return unavailable(routePath, `${sources.join(", ")} have no commits`);

  const when = new Date(iso);
  if (Number.isNaN(when.getTime())) return unavailable(routePath, `git returned an unparseable date: ${iso}`);
  return when;
}
