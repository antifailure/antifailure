/**
 * Writes a markdown twin of every page next to it in out/.
 *
 * /product/twins.html gets /product/twins.md, and the page's metadata points at
 * it with <link rel="alternate" type="text/markdown">.
 *
 * Why bother. A page of this site is roughly 300KB of HTML, of which the actual
 * prose is under 1% of the bytes. Anything reading it to answer a question
 * spends nearly all of its context on layout markup. Most AI crawlers also do
 * not execute JavaScript, so a markdown twin generated at build time is both
 * smaller and more reliable than asking them to render.
 *
 * This reads the built HTML rather than the React source, which means it can
 * only ever describe what actually shipped. A twin cannot drift from its page,
 * because it is derived from it.
 *
 * Run after `next build`. Wired into the build script.
 */
import { readFileSync, writeFileSync, readdirSync, statSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const OUT = path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "out");

/**
 * Never content, wherever they appear.
 *
 * `nav` is here rather than in the fallback list because the one nav inside
 * <main> is the breadcrumb trail, and a twin that opens with "Home / Writing /
 * <the title again>" wastes the first thing a reader of it sees.
 */
const DROP = ["script", "style", "svg", "noscript", "template", "nav"];

/**
 * Site chrome, dropped only when there is no <main> to scope to.
 *
 * `header` and `footer` are structural, not positional: inside <main> they are
 * an article's own header and footer, which is content. Dropping them
 * unconditionally cost the three blog posts their <h1>, their date, their tags
 * and their standfirst, because those live in <article><header>. The twins
 * still looked right, because the missing heading was silently replaced by the
 * <title> further down, suffix and all. That is the failure this whole file is
 * supposed to prevent, so check-seo.mjs now asserts every twin's first heading
 * is the page's real h1.
 */
const DROP_WITHOUT_MAIN = [...DROP, "footer", "header"];

/**
 * Removes an element and everything inside it, counting depth.
 *
 * A lazy regex like /<nav\b[^>]*>[\s\S]*?<\/nav>/ is the obvious way to do
 * this and it is wrong the moment the markup nests, which this site's header
 * does: it contains three navs inside two headers. The lazy match stops at the
 * first </nav> it meets, so the outer nav's remaining content survives and the
 * whole navigation menu ends up in the twin. It did, and the first generated
 * file opened with the contents of the product dropdown.
 */
function stripElements(html, tags) {
  let out = html;
  for (const tag of tags) {
    const token = new RegExp(`<${tag}\\b[^>]*>|</${tag}\\s*>`, "gi");
    let result = "";
    let depth = 0;
    let cursor = 0;
    let m;
    while ((m = token.exec(out)) !== null) {
      const isOpen = !m[0].startsWith("</");
      if (isOpen) {
        if (depth === 0) result += out.slice(cursor, m.index);
        // A void/self-closing form of these tags does not occur, but guard it.
        if (!m[0].endsWith("/>")) depth++;
      } else {
        depth = Math.max(0, depth - 1);
        if (depth === 0) cursor = m.index + m[0].length;
      }
    }
    if (depth === 0) result += out.slice(cursor);
    out = result;
  }
  return out;
}

function decode(s) {
  return s
    .replace(/&nbsp;/g, " ")
    .replace(/&amp;/g, "&")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&quot;/g, '"')
    .replace(/&#(\d+);/g, (_, d) => String.fromCharCode(+d))
    .replace(/&#x([0-9a-f]+);/gi, (_, h) => String.fromCharCode(parseInt(h, 16)));
}

function text(html) {
  return decode(html.replace(/<[^>]+>/g, " "))
    .replace(/\s+/g, " ")
    .trim();
}

/**
 * Pull headings, paragraphs and list items out in document order.
 *
 * Deliberately structural rather than clever: an answer engine selects content
 * at the level of a passage, so preserving which heading a paragraph sits under
 * is the part that matters. Prettier prose with the hierarchy flattened would
 * be worse.
 */
function extract(html) {
  // Scope to <main> rather than excluding the chrome.
  //
  // Excluding was tried first and produced twins that opened with the contents
  // of the product dropdown, because the site's mega-menu renders in plain
  // divs with no <nav> or <header> ancestor to exclude. A blacklist can only
  // remove the chrome somebody remembered to mark up. A whitelist keeps
  // exactly the content region and is not affected by how the chrome around it
  // is built.
  const main = html.match(/<main\b[^>]*>([\s\S]*)<\/main>/i)?.[1];
  const body =
    main !== undefined
      ? stripElements(main, DROP)
      : stripElements(
          html.match(/<body[^>]*>([\s\S]*)<\/body>/i)?.[1] ?? html,
          DROP_WITHOUT_MAIN,
        );

  const out = [];
  const seen = new Set();
  // tr, dt and dd are here because the pages that carry the most specific
  // claims on this site carry them in exactly those elements, and the twin
  // dropped every one.
  //
  // /product/safe-state's masking table is the whole point of the page, and its
  // twin said "Table Row Parent Reason" and then stopped: the header row is
  // <th>, every value is <td>, and neither was matched. /product/overview's
  // manifest walkthrough is a <dl>, so its twin carried the prose around a
  // definition list and none of the definitions. An answer engine reading the
  // twin could see that the page discusses masking and could not see a single
  // rule.
  //
  // tr rather than th and td, so a row stays one line. The alternation is
  // matched left to right and exec does not overlap, so the tr match consumes
  // its own cells and a cell is never emitted twice.
  const re =
    /<(h1|h2|h3|h4|p|li|blockquote|code|figcaption|tr|dt|dd)\b[^>]*>([\s\S]*?)<\/\1>/gi;
  const cell = /<(th|td)\b[^>]*>([\s\S]*?)<\/\1>/gi;

  let m;
  while ((m = re.exec(body)) !== null) {
    const tag = m[1].toLowerCase();

    if (tag === "tr") {
      const cells = [...m[2].matchAll(cell)].map((c) => text(c[2]));
      // A row of nothing but decoration, which this site's tables use for
      // spacing and for a status pill with no text.
      if (cells.every((value) => value.length < 2)) continue;
      const row = `| ${cells.join(" | ")} |`;
      if (seen.has(row)) continue;
      seen.add(row);
      out.push(row);
      continue;
    }

    const value = text(m[2]);
    // Skip empties, single glyphs from decorative spans, and repeated boilerplate.
    if (value.length < 2) continue;
    const key = `${tag}:${value}`;
    if (seen.has(key)) continue;
    seen.add(key);

    if (tag === "h1") out.push(`# ${value}`);
    else if (tag === "h2") out.push(`## ${value}`);
    else if (tag === "h3") out.push(`### ${value}`);
    else if (tag === "h4") out.push(`#### ${value}`);
    else if (tag === "li") out.push(`- ${value}`);
    else if (tag === "blockquote") out.push(`> ${value}`);
    // A definition list is a term and its meaning, and losing which is which
    // makes the pair unreadable. `**term**` and the value beneath it is what
    // the same content would look like written as markdown by hand.
    else if (tag === "dt") out.push(`**${value}**`);
    else if (tag === "dd") out.push(value);
    else out.push(value);
  }
  return out;
}

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

let written = 0;
let bytesHtml = 0;
let bytesMd = 0;
const twins = [];

for (const file of htmlFiles(OUT)) {
  const html = readFileSync(file, "utf8");

  // Do not publish a twin of a page that is not itself indexable.
  if (/<meta name="robots" content="[^"]*noindex/i.test(html)) continue;

  const canonical = html.match(/<link rel="canonical" href="([^"]+)"/i)?.[1] ?? "";
  const title = decode(html.match(/<title>([^<]*)<\/title>/i)?.[1] ?? "");
  const description = decode(
    html.match(/<meta name="description" content="([^"]*)"/i)?.[1] ?? "",
  );

  const blocks = extract(html);
  if (blocks.length === 0) continue;

  const md = [
    `<!-- Generated from ${path.relative(OUT, file)} at build time. Do not edit. -->`,
    "",
    ...(canonical ? [`> Canonical: ${canonical}`, ""] : []),
    ...(description ? [`> ${description}`, ""] : []),
    // A fallback, and it should be rare: a page whose content region has no
    // h1 at all. The site name comes off, because the twin already states its
    // canonical URL and a heading that ends in the site's name reads as a
    // browser tab rather than as the title of the thing.
    //
    // The separator is spelled out here rather than imported, because this is a
    // build script reading built HTML and there is no TypeScript in scope. It
    // has to match lib/site.ts TITLE_SEPARATOR, and check-seo.mjs asserts the
    // twin's first heading is the page's real h1, which is what catches it if
    // the two ever drift apart.
    ...(blocks[0]?.startsWith("# ")
      ? []
      : [`# ${title.replace(/\s+\u00b7\s+Antifailure$/, "")}`, ""]),
    blocks.join("\n\n"),
    "",
  ].join("\n");

  const target = file.replace(/\.html$/, ".md");
  writeFileSync(target, md);
  twins.push({ canonical, markdown: md });
  written++;
  bytesHtml += html.length;
  bytesMd += md.length;
}

console.log(
  `wrote ${written} markdown twins  ` +
    `(${(bytesHtml / 1024 / 1024).toFixed(1)}MB HTML -> ${(bytesMd / 1024).toFixed(0)}KB markdown)`,
);
if (written === 0) {
  console.error("no twins written; did next build run and produce out/?");
  process.exit(1);
}

// The route-generated llms-full.txt used to contain only each page's summary,
// despite calling itself the full text. Build it from the twins above, which
// are themselves extracted from the rendered pages, so a section visible to a
// reader cannot disappear from the one-fetch corpus.
const corpus = [
  "# Antifailure: full text of the public site",
  "",
  "> Generated from the rendered, indexable pages at build time.",
  "",
  ...twins
    .sort((a, b) => a.canonical.localeCompare(b.canonical))
    .flatMap((twin) => [twin.markdown.trim(), "", "---", ""]),
].join("\n");
writeFileSync(path.join(OUT, "llms-full.txt"), corpus);
console.log(
  `wrote llms-full.txt from ${twins.length} rendered pages (${Math.round(corpus.length / 1024)}KB)`,
);
