/**
 * Asserts, against the built output, that the SEO and GEO surfaces are real.
 *
 * The reason this exists rather than a checklist: every single thing it checks
 * was absent from production at the time it was written, and nothing anywhere
 * said so. There was no sitemap, no robots.txt, no canonical, no OpenGraph, no
 * structured data, and an og:image that had 404'd since the day it was
 * referenced. All of it built green. All of it deployed. None of it worked.
 *
 * A structural test that a file contains the right words guards against a
 * regression of something already known. That is exactly what is wanted here:
 * these are load-bearing tags that nothing else will notice the loss of,
 * because a missing meta tag breaks no page and fails no type check.
 *
 * Run: npm run check:seo   (after npm run build)
 */
import { readFileSync, existsSync, readdirSync, statSync } from "node:fs";
import { execFileSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = path.join(path.dirname(fileURLToPath(import.meta.url)), "..");
const OUT = path.join(ROOT, "out");

let failures = 0;
let checks = 0;

function ok(label) {
  checks++;
  console.log(`  ok   ${label}`);
}

function fail(label, detail) {
  checks++;
  failures++;
  console.error(`  FAIL ${label}${detail ? `\n         ${detail}` : ""}`);
}

function assert(cond, label, detail) {
  if (cond) ok(label);
  else fail(label, detail);
}

if (!existsSync(OUT)) {
  console.error("no out/ directory. Run `npm run build` first.");
  process.exit(1);
}

const read = (rel) => readFileSync(path.join(OUT, rel), "utf8");
const has = (rel) => existsSync(path.join(OUT, rel));

console.log("\nCrawl surfaces");
assert(has("robots.txt"), "robots.txt exists");
assert(has("sitemap.xml"), "sitemap.xml exists");
assert(has("llms.txt"), "llms.txt exists");
assert(has("llms-full.txt"), "llms-full.txt exists");
assert(has("og.png"), "og.png exists (docs/astro.config.mjs references it)");
assert(has("manifest.webmanifest"), "web app manifest exists");
assert(has("staticwebapp.config.json"), "host config exists (headers, 301s, 404 status)");

if (has("robots.txt")) {
  const robots = read("robots.txt");
  console.log("\nAI crawler access");
  for (const bot of [
    "GPTBot",
    "OAI-SearchBot",
    "ChatGPT-User",
    "ClaudeBot",
    "Claude-SearchBot",
    "PerplexityBot",
    "Google-Extended",
  ]) {
    assert(robots.includes(bot), `robots.txt names ${bot}`);
  }
  assert(robots.includes("Sitemap:"), "robots.txt advertises a sitemap");
  assert(
    robots.includes("/docs/sitemap-index.xml"),
    "robots.txt advertises the documentation sitemap too",
    "the docs are 41 of the site's 72 pages; leaving them out halves what a crawler finds",
  );
}

if (has("sitemap.xml")) {
  const sitemap = read("sitemap.xml");
  const urls = (sitemap.match(/<loc>/g) ?? []).length;
  console.log("\nSitemap");
  // Twenty, not the twenty-five this was written with. Four product pages
  // described subsystems that do not exist and were removed, and two more
  // collapsed into one. The number is a canary against a truncated or empty
  // sitemap, so it tracks the real page count rather than holding a page
  // hostage to a threshold.
  assert(urls >= 20, `sitemap lists ${urls} URLs`, urls < 20 ? "expected at least 20" : "");
  assert(
    !sitemap.includes("/signin") && !sitemap.includes("/signup"),
    "sitemap excludes the noindex waitlist routes",
  );
  // The property that matters is that lastmod came from content history, not
  // from the clock. Requiring several distinct values was the first version of
  // this check and it was wrong: a repository where every page legitimately
  // changed in one commit would fail it, which is exactly what happened the
  // first time these files were committed together.
  //
  // The second version compared each stamp against the wall clock and called
  // anything inside ten minutes a build-clock fallback. That was wrong in the
  // other direction, and it would have failed almost every pull request: the
  // commit under test is usually a few minutes old when CI runs it, so the very
  // pages a change touches look freshly stamped. It failed on this one.
  //
  // What separates the two cases exactly: contentLastModified falls back to
  // `new Date()`, and no commit date can be later than the newest commit in the
  // repository. A stamp after that came from the clock. A minute of slack
  // covers clock skew between the machine that committed and the one building.
  const stamps = [...sitemap.matchAll(/<lastmod>([^<]+)<\/lastmod>/g)].map((m) => new Date(m[1]));
  let newest = null;
  try {
    newest = new Date(execFileSync("git", ["log", "-1", "--format=%cI"], { cwd: ROOT, encoding: "utf8" }).trim());
  } catch {
    newest = null;
  }
  const ceiling = newest ? newest.getTime() + 60 * 1000 : Date.now();
  const fromTheClock = stamps.filter((d) => d.getTime() > ceiling);
  assert(
    stamps.length > 0 && fromTheClock.length === 0,
    `sitemap lastmod comes from git history (${new Set(stamps.map(String)).size} distinct across ${stamps.length} URLs)`,
    fromTheClock.length > 0
      ? `${fromTheClock.length} URLs are stamped later than the newest commit, which is the build clock rather than a commit date. ` +
        "Usually a shallow checkout: set fetch-depth: 0 on actions/checkout."
      : stamps.length === 0
        ? "no lastmod at all"
        : "",
  );
}

// Every indexable page, checked individually. A site-wide tag that is present
// on the home page and missing on eleven product pages is the normal failure.
function htmlFiles(dir) {
  const found = [];
  for (const entry of readdirSync(dir)) {
    if (entry === "_next") continue;
    const full = path.join(dir, entry);
    if (statSync(full).isDirectory()) found.push(...htmlFiles(full));
    else if (entry.endsWith(".html")) found.push(full);
  }
  return found;
}

const pages = htmlFiles(OUT);
console.log(`\nPer-page tags (${pages.length} HTML files)`);

const missing = {
  canonical: [],
  ogTitle: [],
  ogImage: [],
  twitter: [],
  description: [],
  jsonld: [],
  breadcrumb: [],
  h1: [],
  oneMain: [],
  markdownTwin: [],
  twinHeading: [],
};

for (const file of pages) {
  const html = readFileSync(file, "utf8");
  const rel = path.relative(OUT, file);
  const noindex = /<meta name="robots" content="[^"]*noindex/i.test(html);
  if (noindex) continue;

  if (!/<link rel="canonical"/i.test(html)) missing.canonical.push(rel);
  if (!/property="og:title"/i.test(html)) missing.ogTitle.push(rel);
  if (!/property="og:image"/i.test(html)) missing.ogImage.push(rel);
  if (!/name="twitter:card"/i.test(html)) missing.twitter.push(rel);
  if (!/<meta name="description"/i.test(html)) missing.description.push(rel);
  if (!/application\/ld\+json/i.test(html)) missing.jsonld.push(rel);
  // A trail a reader can see and no BreadcrumbList describing it is the failure
  // this catches: the blog posts rendered the visible trail and shipped no
  // markup for it, because they use PostJsonLd rather than the PageHero that
  // emits both. The home page is exempt, since a trail of one is the page
  // pointing at itself.
  if (rel !== "index.html" && !html.includes('"BreadcrumbList"')) missing.breadcrumb.push(rel);
  // Exactly one, because the markdown twins are cut from it and because a
  // second one is a second content landmark. The home page had two: a
  // decorative drawing of an application used a real <main> as a layout box,
  // so anybody navigating by landmark met the page and then met a picture.
  const mains = (html.match(/<main\b/gi) ?? []).length;
  if (mains !== 1) missing.oneMain.push(`${rel} (${mains})`);
  if (!/<h1[\s>]/i.test(html)) missing.h1.push(rel);
  const twin = file.replace(/\.html$/, ".md");
  if (!existsSync(twin)) missing.markdownTwin.push(rel);
  else {
    // The twin's first heading has to be the page's own h1. It is the check
    // that catches content being dropped rather than merely absent: the three
    // blog posts lost their whole <article><header> to a chrome filter, and
    // the twin still opened with a plausible heading because the generator
    // silently substituted the <title>. A twin missing a section is worse than
    // no twin, because nothing about it looks wrong.
    const pageH1 = html.match(/<h1[^>]*>([\s\S]*?)<\/h1>/i)?.[1];
    const twinH1 = readFileSync(twin, "utf8").match(/^# (.+)$/m)?.[1];
    // The entity set here has to match decode() in markdown-twins.mjs, which is
    // what wrote the twin. Decoding only &amp; was enough for as long as no h1
    // held an apostrophe: React serializes one as &#x27; in the HTML while the
    // twin carries the character itself, so the two sides of a comparison that
    // are the same string stopped comparing equal the day a title said
    // "production's". &amp; is decoded last so an escaped entity does not get
    // unescaped twice.
    const flatten = (v) =>
      v
        ? v
            .replace(/<[^>]*>/g, "")
            .replace(/&nbsp;/g, " ")
            .replace(/&lt;/g, "<")
            .replace(/&gt;/g, ">")
            .replace(/&quot;/g, '"')
            .replace(/&#(\d+);/g, (_, d) => String.fromCharCode(+d))
            .replace(/&#x([0-9a-f]+);/gi, (_, h) => String.fromCharCode(parseInt(h, 16)))
            .replace(/&amp;/g, "&")
            .replace(/\s+/g, " ")
            .trim()
        : "";
    if (pageH1 && flatten(pageH1) !== flatten(twinH1)) {
      missing.twinHeading.push(
        `${rel} (page "${flatten(pageH1).slice(0, 40)}" vs twin "${flatten(twinH1).slice(0, 40)}")`,
      );
    }
  }
}

// The skip link is in the root layout, so it is on every page, but the element
// it points at is not: three pages do not use SiteLayout and had no id="main",
// and one of them had no <main> at all. That made the first thing a keyboard
// user reaches a link that goes nowhere. The assembled-site link check caught
// it, which is the right outcome and the late one, so it is asserted here too
// against every page rather than only the ones that end up in the site bundle.
console.log("\nSkip link");
const deadSkipLink = [];
for (const file of pages) {
  const html = readFileSync(file, "utf8");
  const target = html.match(/<a[^>]+class="skip-to-content"[^>]*href="#([^"]+)"/i)?.[1];
  if (!target) continue;
  if (!new RegExp(`id="${target}"`).test(html)) deadSkipLink.push(path.relative(OUT, file));
}
assert(
  deadSkipLink.length === 0,
  "the skip-to-content link resolves on every page",
  deadSkipLink.length > 0
    ? `no matching id on ${deadSkipLink.length}: ${deadSkipLink.join(", ")}`
    : "",
);

const LABELS = {
  canonical: "every indexable page has a canonical",
  ogTitle: "every indexable page has og:title",
  ogImage: "every indexable page has og:image",
  twitter: "every indexable page has a twitter card",
  description: "every indexable page has a meta description",
  jsonld: "every indexable page has JSON-LD",
  breadcrumb: "every indexable page below the root has a BreadcrumbList",
  h1: "every indexable page has an h1",
  oneMain: "every indexable page has exactly one <main>",
  markdownTwin: "every indexable page has its markdown twin",
  twinHeading: "every twin opens with the page's own h1",
};

for (const [key, list] of Object.entries(missing)) {
  assert(
    list.length === 0,
    LABELS[key],
    list.length > 0 ? `missing on ${list.length}: ${list.slice(0, 5).join(", ")}${list.length > 5 ? " ..." : ""}` : "",
  );
}

// Reachability, which is a different question from "does this link resolve".
//
// Every link on the site pointed somewhere real and the blog was still an
// island: /blog was linked only from its own three posts, and each post only
// from /blog. Nothing anywhere else on the site pointed into the cluster, so
// the only route in was the sitemap. A page a crawler reaches only through a
// sitemap is a page it treats as unimportant, and a reader cannot reach it at
// all.
//
// So: walk out from the home page the way a crawler does, and require that
// every indexable route is found. Depth is reported rather than asserted,
// because "three clicks" is a guideline and this is a small site.
console.log("\nReachability");
{
  const hrefs = (html) =>
    [...html.matchAll(/href="(\/[^"#?][^"]*)"/g)].map((m) => m[1].replace(/\/$/, "") || "/");
  const fileFor = (route) =>
    [route === "/" ? "index.html" : `${route.slice(1)}.html`, `${route.slice(1)}/index.html`]
      .map((r) => path.join(OUT, r))
      .find(existsSync);

  const seen = new Set(["/"]);
  const depth = new Map([["/", 0]]);
  const queue = ["/"];
  while (queue.length > 0) {
    const here = queue.shift();
    const file = fileFor(here);
    if (!file) continue;
    for (const href of hrefs(readFileSync(file, "utf8"))) {
      if (seen.has(href) || href.startsWith("/docs") || href.startsWith("/_next")) continue;
      seen.add(href);
      depth.set(href, depth.get(here) + 1);
      queue.push(href);
    }
  }

  const sitemapRoutes = [...readFileSync(path.join(OUT, "sitemap.xml"), "utf8")
    .matchAll(/<loc>[^<]*?(\/[^<]*)?<\/loc>/g)]
    .map((m) => new URL(m[0].replace(/<\/?loc>/g, "")).pathname.replace(/\/$/, "") || "/");
  const unreachable = sitemapRoutes.filter((r) => !seen.has(r));
  const deepest = Math.max(...[...depth.values()]);
  assert(
    unreachable.length === 0,
    `every route in the sitemap is reachable from the home page (deepest is ${deepest} clicks)`,
    unreachable.length > 0
      ? `${unreachable.length} reachable only through the sitemap: ${unreachable.join(", ")}`
      : "",
  );
}

// Navigation that belongs to no landmark.
//
// The product flyout is a sibling of the header bar rather than a child, so
// its nineteen links sat outside header, main and footer alike. Somebody
// navigating by landmark met the banner and then the page, and never the
// navigation. Every other nav on the site was already inside one, which is
// what made this invisible: the markup looked consistent everywhere you
// checked.
//
// Counting <a> elements rather than <nav>, because the defect was a block of
// links with no nav around it either. An anchor that is inside no landmark at
// all is the thing to catch, whatever element it sits in.
console.log("\nLandmarks");
{
  const stray = [];
  for (const file of pages) {
    const html = readFileSync(file, "utf8");
    if (/<meta name="robots" content="[^"]*noindex/i.test(html)) continue;

    // Walk the tags, tracking landmark depth. A link seen at depth zero is in
    // no landmark. Regex rather than a parser because the built output is
    // machine generated and well formed, and this file has no dependencies.
    let depth = 0;
    let loose = 0;
    for (const m of html.matchAll(/<(\/?)(header|main|footer|nav|aside|a)\b[^>]*>/gi)) {
      const [tagText, closing, tag] = m;
      const name = tag.toLowerCase();
      if (name === "a") {
        // Opening tags only. Counting </a> too reported every link twice, which
        // is how this check first "found" two loose links on every page.
        if (closing) continue;
        // The skip link is the one anchor that has to be outside everything: it
        // is the first thing in the body precisely so it comes before the
        // banner a keyboard user is trying to skip.
        if (/class="[^"]*skip-to-content/.test(tagText)) continue;
        if (depth === 0) loose++;
        continue;
      }
      if (closing) depth = Math.max(0, depth - 1);
      else depth++;
    }
    if (loose > 0) stray.push(`${path.relative(OUT, file)} (${loose})`);
  }
  assert(
    stray.length === 0,
    "every link on every page is inside a landmark",
    stray.length > 0 ? `links outside header, main, footer, nav and aside: ${stray.slice(0, 5).join(", ")}` : "",
  );
}

// A single duplicated title across pages is a cannibalisation bug that no
// other check catches.
const titles = new Map();
for (const file of pages) {
  const html = readFileSync(file, "utf8");
  if (/<meta name="robots" content="[^"]*noindex/i.test(html)) continue;
  const t = html.match(/<title>([^<]*)<\/title>/i)?.[1];
  if (!t) continue;
  titles.set(t, (titles.get(t) ?? 0) + 1);
}
const dupes = [...titles.entries()].filter(([, n]) => n > 1);
console.log("\nUniqueness");
assert(
  dupes.length === 0,
  "every indexable page has a unique title",
  dupes.length > 0 ? dupes.map(([t, n]) => `"${t}" x${n}`).join("; ") : "",
);

console.log(`\n${checks - failures}/${checks} passed`);
if (failures > 0) {
  console.error(`\n${failures} SEO check(s) failed.`);
  process.exit(1);
}
