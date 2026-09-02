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

/**
 * The one origin every absolute URL the site emits has to resolve to.
 *
 * This is a literal rather than an import of SITE_URL because SITE_URL is
 * `process.env.NEXT_PUBLIC_SITE_URL ?? "https://antifailure.dev"`, and a check
 * that reads its subject's own configuration cannot disagree with it. Every
 * canonical, og:url, sitemap <loc>, robots Host and JSON-LD @id on the site is
 * built from that variable, so one wrong value in one build environment ships
 * the whole site canonicalised to the wrong scheme or the wrong host, and
 * nothing about the pages themselves looks wrong.
 *
 * That is not hypothetical. Google's top result for this site is the http://
 * spelling of the home page, because the site shipped with no canonical at all
 * for long enough to be indexed that way. The scheme costs nothing to a
 * visitor, since .dev is an HSTS-preloaded TLD and no browser will emit a
 * plaintext request to one, but it is the wrong URL in the index and it splits
 * every signal that points at the site between two spellings of it.
 *
 * Both wrong spellings are asserted against, not just the scheme: www is a
 * second live origin serving this same build on its own certificate, so
 * https://www.antifailure.dev is reachable, returns 200, and is exactly as
 * wrong to canonicalise to as the http:// one.
 */
const ORIGIN = "https://antifailure.dev";
const WRONG_ORIGINS = [
  "http://antifailure.dev",
  "http://www.antifailure.dev",
  "https://www.antifailure.dev",
];

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

function jsonLdNodes(html) {
  const nodes = [];
  for (const match of html.matchAll(/<script type="application\/ld\+json">([\s\S]*?)<\/script>/gi)) {
    const parsed = JSON.parse(match[1]);
    nodes.push(...(Array.isArray(parsed?.["@graph"]) ? parsed["@graph"] : [parsed]));
  }
  return nodes;
}

console.log("\nCrawl surfaces");
assert(has("robots.txt"), "robots.txt exists");
assert(has("sitemap.xml"), "sitemap.xml exists");
assert(has("llms.txt"), "llms.txt exists");
assert(has("llms-full.txt"), "llms-full.txt exists");
assert(has("404.md"), "the visual 404 has a machine-readable recovery page");
assert(has("errors.v1.json"), "the machine-readable error catalog exists");
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
  const groups = robots.split(/\n\s*\n/).filter((block) => /User-Agent:/i.test(block));
  for (const bot of ["GPTBot", "OAI-SearchBot", "ClaudeBot", "Claude-SearchBot", "PerplexityBot"]) {
    const group = groups.find((block) =>
      new RegExp(`^User-Agent:\\s*${bot.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*$`, "im").test(block),
    );
    assert(
      Boolean(group) && /^Allow:\s*\/$/im.test(group) && !/^Disallow:\s*\/$/im.test(group),
      `robots.txt permits ${bot} to crawl public pages`,
    );
  }

  // Advertising a sitemap and advertising it at the right URL are different
  // claims, and the check above only made the first one. `includes("Sitemap:")`
  // is true of `Sitemap: http://www.antifailure.dev/sitemap.xml`, which names a
  // different origin from the one every page canonicalises to and hands a
  // crawler a second spelling of the site to reconcile.
  const advertised = [...robots.matchAll(/^Sitemap:\s*(\S+)$/gim)].map((m) => m[1]);
  const offOrigin = advertised.filter((url) => !url.startsWith(`${ORIGIN}/`));
  assert(
    advertised.length > 0 && offOrigin.length === 0,
    `robots.txt advertises every sitemap on ${ORIGIN}`,
    offOrigin.length > 0 ? offOrigin.join(", ") : "no Sitemap: line at all",
  );
  const host = robots.match(/^Host:\s*(\S+)$/im)?.[1];
  assert(
    host === ORIGIN,
    `robots.txt names ${ORIGIN} as the host`,
    host ? `it names ${host}` : "no Host: line",
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
  // The count above is a canary against a truncated sitemap and says nothing
  // about what the URLs in it are. A sitemap listing 32 URLs on a host the
  // pages do not canonicalise to passes it and is worse than no sitemap,
  // because it actively submits the wrong spelling for indexing.
  const locs = [...sitemap.matchAll(/<loc>([^<]*)<\/loc>/g)].map((m) => m[1]);
  const strays = locs.filter((url) => url !== ORIGIN && !url.startsWith(`${ORIGIN}/`));
  assert(
    locs.length > 0 && strays.length === 0,
    `every sitemap URL is on ${ORIGIN}`,
    strays.length > 0 ? `${strays.length} are not: ${strays.slice(0, 3).join(", ")}` : "",
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

if (has("404.html") && has("404.md")) {
  const notFound = read("404.html");
  const recovery = read("404.md");
  const alternate = (notFound.match(/<link\b[^>]*>/gi) ?? []).find(
    (tag) =>
      /rel="alternate"/i.test(tag) &&
      /type="text\/markdown"/i.test(tag) &&
      /href="https:\/\/antifailure\.dev\/404\.md"/i.test(tag),
  );
  assert(
    Boolean(alternate),
    "404 HTML advertises its Markdown recovery body",
  );
  for (const address of ["/sitemap.xml", "/llms.txt", "/docs", "/openapi.json"]) {
    assert(recovery.includes(address), `404 Markdown points at ${address}`);
  }
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

/**
 * The path a built file is the page for, in the spelling the host serves.
 *
 * staticwebapp.config.json sets "trailingSlash": "never", so /about is the one
 * address /about answers on and /about/ is a 301 to it. The canonical has to
 * be the address that answers, and the home page is the origin itself with no
 * trailing slash, which is what absoluteUrl() in lib/site.ts produces.
 */
const routeOf = (rel) =>
  rel === "index.html" ? "" : `/${rel.replace(/\.html$/, "").replace(/\/index$/, "")}`;

const pages = htmlFiles(OUT);
console.log(`\nPer-page tags (${pages.length} HTML files)`);

const missing = {
  canonical: [],
  canonicalTarget: [],
  ogTitle: [],
  ogImage: [],
  ogUrl: [],
  twitter: [],
  description: [],
  jsonld: [],
  jsonldPage: [],
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
  // What the canonical POINTS AT, which the presence check above never looked
  // at. A page carrying `<link rel="canonical" href="http://www.antifailure.dev/pricing">`
  // satisfies that check completely. Downgrading every URL in the built output
  // to that spelling moved this gate by zero checks before this assertion
  // existed, so a site-wide scheme or host regression, which is the only way
  // this failure ever actually happens, shipped green.
  //
  // Asserted as the exact expected URL rather than as a prefix, because that
  // costs nothing more and additionally catches a page canonicalising to a
  // different page, which is the cannibalisation bug that silently drops a
  // page out of the index entirely.
  const want = ORIGIN + routeOf(rel);
  const canonicalValue = html.match(/<link rel="canonical" href="([^"]*)"/i)?.[1] ?? "";
  if (canonicalValue && canonicalValue !== want) {
    missing.canonicalTarget.push(`${rel} (want ${JSON.stringify(want)}, got ${JSON.stringify(canonicalValue)})`);
  }
  // og:url is a second, independent claim about the page's address, and a
  // crawler that disagrees with the canonical has two candidates rather than
  // one. They are built from the same absoluteUrl() call today; this is what
  // notices if they ever stop being.
  // Checking og:url against the canonical rather than against the expected
  // address would pass whenever both are wrong the same way, which is the only
  // shape this failure has ever taken: one SITE_URL feeds both, so a bad value
  // moves them together and a relative check sees two values that agree.
  const ogUrl = html.match(/<meta property="og:url" content="([^"]*)"/i)?.[1];
  if (ogUrl !== want) {
    missing.ogUrl.push(`${rel} (want ${JSON.stringify(want)}, og:url ${JSON.stringify(ogUrl ?? null)})`);
  }
  if (!/property="og:title"/i.test(html)) missing.ogTitle.push(rel);
  if (!/property="og:image"/i.test(html)) missing.ogImage.push(rel);
  if (!/name="twitter:card"/i.test(html)) missing.twitter.push(rel);
  if (!/<meta name="description"/i.test(html)) missing.description.push(rel);
  if (!/application\/ld\+json/i.test(html)) missing.jsonld.push(rel);
  if (rel !== "index.html") {
    let nodes = [];
    try {
      nodes = jsonLdNodes(html);
    } catch {
      missing.jsonldPage.push(`${rel} (JSON-LD does not parse)`);
    }
    const page = nodes.find((node) => node?.["@id"] === `${canonicalValue}#webpage`);
    if (!page || page.url !== canonicalValue) {
      missing.jsonldPage.push(`${rel} (WebPage identity does not match ${canonicalValue})`);
    }
  }
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

// Every markdown twin a page ADVERTISES has to be a file that exists.
//
// The per-page loop above skips noindex pages, so it only ever asked whether
// an indexable page had a twin. It could not see the opposite failure, which
// is the one that shipped: /signin and /signup are deliberately noindex, the
// twin generator deliberately refuses to write a twin for a noindex page, and
// lib/seo.ts advertised one anyway. Two <link rel="alternate"> tags pointed at
// https://antifailure.dev/signin.md and /signup.md, the host answered 404 for
// both, and the only thing that noticed was the external link checker, after
// the build was green and merged.
//
// So this walks every built page, indexable or not, reads the address it
// claims its markdown lives at, and asserts the build actually wrote that
// file. It is deliberately driven by the advertised href rather than by the
// route registry: the tag is what a crawler follows, so the tag is what has to
// be true.
console.log("\nAdvertised markdown twins");
{
  const dangling = [];
  let advertised = 0;
  for (const file of pages) {
    const html = readFileSync(file, "utf8");
    const rel = path.relative(OUT, file);
    const tag = (html.match(/<link\b[^>]*>/gi) ?? []).find(
      (t) => /rel="alternate"/i.test(t) && /type="text\/markdown"/i.test(t),
    );
    if (!tag) continue;
    const href = tag.match(/href="([^"]*)"/i)?.[1] ?? "";
    advertised++;
    // Same-origin only. An absolute address on another host is somebody
    // else's file and this check has no opinion about it.
    if (!href.startsWith(ORIGIN + "/")) {
      dangling.push(`${rel} advertises ${JSON.stringify(href)}, which is not on ${ORIGIN}`);
      continue;
    }
    const target = href.slice(ORIGIN.length + 1);
    if (!has(target)) dangling.push(`${rel} advertises /${target}, which the build did not write`);
  }
  assert(
    advertised > 0 && dangling.length === 0,
    `every advertised markdown twin exists (${advertised} pages advertise one)`,
    advertised === 0
      ? "no page advertises a markdown twin at all, which is itself the regression"
      : dangling.join("\n         "),
  );
}

// The skip link is in the root layout, so it is on every page, but the element
// it points at is not: three pages do not use SiteLayout and had no id="main",
// and one of them had no <main> at all. That made the first thing a keyboard
// user reaches a link that goes nowhere. The assembled-site link check caught
// it, which is the right outcome and the late one, so it is asserted here too
// against every page rather than only the ones that end up in the site bundle.
// The 404, which every mistyped URL on the live site reaches.
//
// It serves two robots tags: one from Next's own handling of this route and
// one from not-found.tsx. They read `noindex` and `noindex, follow`, which
// look like a disagreement about `follow` and are not one, because bare
// `noindex` already implies it. Neither can be removed. Dropping the explicit
// one does not leave the framework's behind on its own, it lets the root
// layout's site-wide `index: true` through and ships `index, follow` on the
// 404, which this check caught the moment it was tried.
//
// So the assertion is the property that matters rather than a tag count: no
// robots tag on this page may permit indexing. That holds whether Next keeps
// emitting its tag or stops, and it fails loudly if the layout's default ever
// reaches this page again.
//
// The title is asserted here too. It carried the site name twice for as long
// as the page has existed, which is invisible from source because the root
// layout's template adds the duplication rather than anything written down.
console.log("\nNot found");
if (has("404.html")) {
  const html = read("404.html");
  const robots = [...html.matchAll(/<meta name="robots" content="([^"]*)"/gi)].map((m) => m[1]);
  const permits = robots.filter((r) => !/noindex/i.test(r));
  assert(
    robots.length > 0 && permits.length === 0,
    "no robots tag on the 404 permits indexing",
    robots.length === 0
      ? "the page carries no robots tag at all"
      : `these permit it: ${permits.map((r) => JSON.stringify(r)).join(", ")}`,
  );
  // The strongest of the three claims this page was making. The root layout
  // canonicalises the whole site to SITE_URL, so the 404 inherited it and told
  // crawlers it WAS the home page. Google generally honours a rel=canonical
  // over a noindex when the two conflict, and its canonicalization guidance
  // separately says to check that a canonical target does not carry a noindex,
  // so the pair invited consolidating the 404 into the home page and taking
  // the noindex along.
  //
  // Asserted as a property rather than as an absence, so a later
  // self-referential canonical still passes: the 404 may not point somewhere
  // that is not itself.
  const canonical = html.match(/<link rel="canonical" href="([^"]*)"/i)?.[1];
  assert(
    !canonical || /\/404(\.html)?$/.test(canonical),
    "the 404 does not canonicalise to another page",
    `it claims to be ${canonical}`,
  );

  const title = html.match(/<title>([^<]*)<\/title>/i)?.[1] ?? "";
  const times = title.split("Antifailure").length - 1;
  assert(times === 1, "the 404 title names the site once", `"${title}" names it ${times} times`);
} else {
  fail("the 404 page was built", "no 404.html in out/");
}

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
  canonicalTarget: "every canonical is the page's own https://antifailure.dev address",
  ogUrl: "every og:url agrees with the canonical",
  ogTitle: "every indexable page has og:title",
  ogImage: "every indexable page has og:image",
  twitter: "every indexable page has a twitter card",
  description: "every indexable page has a meta description",
  jsonld: "every indexable page has JSON-LD",
  jsonldPage: "every page below the root has a matching WebPage JSON-LD node",
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

console.log("\nMarkdown twins carry the page's tables and definition lists");
// The twin is what an answer engine reads instead of 300KB of markup, and the
// extractor matched headings, paragraphs and list items only. So
// /product/safe-state's masking table and /product/overview's manifest
// definition list, which are the most specific claims either page makes,
// were in the HTML and absent from the twin. Nothing said so: the twin was
// present, well formed, and the right length.
//
// Derived from the built pages rather than from a fixture, so a cell added to a
// page is covered the day it ships and a hardcoded list cannot go stale.
{
  const cellText = (html) =>
    html
      .replace(/<[^>]+>/g, " ")
      .replace(/&nbsp;/g, " ")
      .replace(/&amp;/g, "&")
      .replace(/&lt;/g, "<")
      .replace(/&gt;/g, ">")
      .replace(/&quot;/g, '"')
      .replace(/&#(\d+);/g, (_, d) => String.fromCharCode(+d))
      .replace(/&#x([0-9a-f]+);/gi, (_, h) => String.fromCharCode(parseInt(h, 16)))
      .replace(/\s+/g, " ")
      .trim();

  let cellsChecked = 0;
  const absent = [];
  for (const file of pages) {
    const twinPath = file.replace(/\.html$/, ".md");
    if (!existsSync(twinPath)) continue;
    const html = readFileSync(file, "utf8");
    const main = html.match(/<main\b[^>]*>([\s\S]*)<\/main>/i)?.[1];
    if (!main) continue;
    // The extractor drops these wholesale, so a string inside one is not
    // content and its absence from the twin is correct.
    const body = main.replace(
      /<(script|style|svg|noscript|template|nav)\b[^>]*>[\s\S]*?<\/\1>/gi,
      " ",
    );
    const twin = readFileSync(twinPath, "utf8");
    for (const match of body.matchAll(/<(th|td|dt|dd)\b[^>]*>([\s\S]*?)<\/\1>/gi)) {
      const value = cellText(match[2]);
      if (value.length < 3) continue;
      cellsChecked++;
      if (!twin.includes(value)) {
        absent.push(`${path.relative(OUT, file)} <${match[1].toLowerCase()}> ${JSON.stringify(value)}`);
      }
    }
  }

  // Standard 24. Every assertion here is an absence, and a page set with no
  // cells in it produces exactly the same green as an extractor that works.
  assert(
    cellsChecked >= 20,
    `the twin check examined ${cellsChecked} table and definition cells`,
    "under twenty means the scan found nothing to check, which is what a broken scan looks like",
  );
  assert(
    absent.length === 0,
    "every table cell and definition on a page is in its markdown twin",
    absent.slice(0, 8).join("; "),
  );
}

console.log("\nMachine-readable corpus");
if (has("llms.txt") && has("llms-full.txt") && has("sitemap.xml")) {
  const index = read("llms.txt");
  const full = read("llms-full.txt");
  const sitemapUrls = [...read("sitemap.xml").matchAll(/<loc>([^<]+)<\/loc>/g)].map((m) => m[1]);
  assert(index.includes("## When to use Antifailure"), "llms.txt says when an agent should use Antifailure");
  for (const address of ["/openapi.json", "/errors.v1.json", "/docs/reference/api", "/docs/reference/cli"]) {
    assert(index.includes(address), `llms.txt discovers ${address}`);
  }
  assert(full.length >= 50_000, `llms-full.txt contains substantive rendered text (${full.length} bytes)`);
  const absent = sitemapUrls.filter((url) => !full.includes(`> Canonical: ${url}`));
  assert(
    absent.length === 0,
    "llms-full.txt contains every marketing sitemap page",
    absent.length ? `missing ${absent.join(", ")}` : "",
  );
}

console.log("\nCanonical inventory");
if (has("sitemap.xml")) {
  const sitemapUrls = new Set([...read("sitemap.xml").matchAll(/<loc>([^<]+)<\/loc>/g)].map((m) => m[1]));
  const pageUrls = new Set();
  for (const file of pages) {
    const html = readFileSync(file, "utf8");
    if (/<meta name="robots" content="[^"]*noindex/i.test(html)) continue;
    const canonical = html.match(/<link rel="canonical" href="([^"]+)"/i)?.[1];
    if (canonical) pageUrls.add(canonical);
  }
  const missingFromMap = [...pageUrls].filter((url) => !sitemapUrls.has(url));
  const missingPage = [...sitemapUrls].filter((url) => !pageUrls.has(url));
  assert(
    missingFromMap.length === 0 && missingPage.length === 0,
    "sitemap URLs and indexable page canonicals are the same set",
    `not in sitemap: ${missingFromMap.join(", ")}; no page: ${missingPage.join(", ")}`,
  );
}

console.log("\nTrust pages");
const trustPages = [
  {
    route: "/about",
    file: "about.html",
    schemaType: "AboutPage",
    links: ["/docs", "https://github.com/antifailure/antifailure", "/privacy", "/contact"],
  },
  {
    route: "/contact",
    file: "contact.html",
    schemaType: "ContactPage",
    links: [
      "https://github.com/antifailure/antifailure/security/advisories/new",
      "https://github.com/antifailure/antifailure/issues/new/choose",
      "https://github.com/antifailure/antifailure/discussions",
      "/signup",
    ],
  },
];
const home = read("index.html");
for (const page of trustPages) {
  assert(has(page.file), `${page.route} was built`);
  if (!has(page.file)) continue;
  const html = read(page.file);
  const canonical = `https://antifailure.dev${page.route}`;
  const node = jsonLdNodes(html).find((item) => item?.["@id"] === `${canonical}#webpage`);
  assert(
    node?.["@type"] === page.schemaType &&
      node?.url === canonical &&
      node?.about?.["@id"] === "https://antifailure.dev/#organization",
    `${page.route} carries linked ${page.schemaType} JSON-LD`,
  );
  assert(home.includes(`href="${page.route}"`), `home navigation links to ${page.route}`);
  for (const href of page.links) {
    assert(html.includes(`href="${href}"`), `${page.route} visibly links to ${href}`);
  }
  const markdown = read(page.file.replace(/\.html$/, ".md"));
  assert(
    markdown.replace(/\s+/g, " ").trim().length > 500,
    `${page.route} has more than 500 characters of raw content`,
  );
}
if (has("contact.html")) {
  const html = read("contact.html");
  const organization = jsonLdNodes(html).find(
    (node) => node?.["@id"] === "https://antifailure.dev/#organization",
  );
  const contacts = new Set((organization?.contactPoint ?? []).map((point) => point.url));
  for (const url of [
    "https://github.com/antifailure/antifailure/security/advisories/new",
    "https://github.com/antifailure/antifailure/issues/new/choose",
    "https://github.com/antifailure/antifailure/discussions",
    "https://antifailure.dev/signup",
  ]) {
    assert(contacts.has(url), `Organization JSON-LD publishes ${url}`);
  }
  assert(!html.includes("mailto:"), "/contact does not advertise dead email channels");
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

// One origin, everywhere, in every file the host will serve.
//
// The tag checks above reach canonicals and og:urls. They do not reach a
// JSON-LD @id, a link written by hand in MDX, a URL baked into a JS chunk, or
// the markdown twins, and every one of those is a place the site states its own
// address. This is the assertion that covers all of them at once, and it is
// deliberately a search for the wrong answer rather than a survey of the right
// one: it needs no list of the files that are allowed to mention the site, so
// a file added later cannot fall outside it.
console.log("\nOne origin");
const SERVED = /\.(html|md|txt|xml|json|js|webmanifest)$/i;
function servedFiles(dir) {
  const found = [];
  for (const entry of readdirSync(dir)) {
    const full = path.join(dir, entry);
    if (statSync(full).isDirectory()) found.push(...servedFiles(full));
    else if (SERVED.test(entry)) found.push(full);
  }
  return found;
}
const offOrigin = [];
for (const file of servedFiles(OUT)) {
  const body = readFileSync(file, "utf8");
  for (const wrong of WRONG_ORIGINS) {
    if (body.includes(wrong)) offOrigin.push(`${path.relative(OUT, file)} (${wrong})`);
  }
}
assert(
  offOrigin.length === 0,
  `no built file spells the site any way but ${ORIGIN}`,
  offOrigin.length > 0
    ? `${offOrigin.length} occurrences: ${offOrigin.slice(0, 5).join(", ")}${offOrigin.length > 5 ? " ..." : ""}`
    : "",
);

console.log(`\n${checks - failures}/${checks} passed`);
if (failures > 0) {
  console.error(`\n${failures} SEO check(s) failed.`);
  process.exit(1);
}
