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
  assert(urls >= 25, `sitemap lists ${urls} URLs`, urls < 25 ? "expected at least 25" : "");
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
  h1: [],
  markdownTwin: [],
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
  if (!/<h1[\s>]/i.test(html)) missing.h1.push(rel);
  if (!existsSync(file.replace(/\.html$/, ".md"))) missing.markdownTwin.push(rel);
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
  h1: "every indexable page has an h1",
  markdownTwin: "every indexable page has its markdown twin",
};

for (const [key, list] of Object.entries(missing)) {
  assert(
    list.length === 0,
    LABELS[key],
    list.length > 0 ? `missing on ${list.length}: ${list.slice(0, 5).join(", ")}${list.length > 5 ? " ..." : ""}` : "",
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
