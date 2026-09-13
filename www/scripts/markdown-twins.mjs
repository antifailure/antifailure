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
import { renderTwin } from "./twin.mjs";

const OUT = path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "out");

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
  const twin = renderTwin(html, path.relative(OUT, file));
  if (twin === null) continue;

  const target = file.replace(/\.html$/, ".md");
  writeFileSync(target, twin.markdown);
  twins.push(twin);
  written++;
  bytesHtml += html.length;
  bytesMd += twin.markdown.length;
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
