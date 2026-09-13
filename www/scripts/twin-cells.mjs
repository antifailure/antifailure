/**
 * The check that every table cell and definition on a page is in its markdown
 * twin, for one page at a time.
 *
 * Moved out of check-seo.mjs, which runs every check when it is loaded, so that
 * a test can hand it a page and a twin. check-seo.mjs still walks out/, counts
 * the cells and fails the build.
 */
import { decodeEntities } from "./entities.mjs";

const cellText = (html) =>
  decodeEntities(html.replace(/<[^>]+>/g, " "))
    .replace(/\s+/g, " ")
    .trim();

/**
 * How many cells of `html` were compared with `twin`, and which were not in it.
 */
export function twinCells(html, twin) {
  const main = html.match(/<main\b[^>]*>([\s\S]*)<\/main>/i)?.[1];
  if (!main) return { checked: 0, absent: [] };
  // The extractor drops these wholesale, so a string inside one is not
  // content and its absence from the twin is correct.
  const body = main.replace(
    /<(script|style|svg|noscript|template|nav)\b[^>]*>[\s\S]*?<\/\1>/gi,
    " ",
  );
  let checked = 0;
  const absent = [];
  for (const match of body.matchAll(/<(th|td|dt|dd)\b[^>]*>([\s\S]*?)<\/\1>/gi)) {
    const value = cellText(match[2]);
    if (value.length < 3) continue;
    checked++;
    if (!twin.includes(value)) absent.push({ tag: match[1].toLowerCase(), value });
  }
  return { checked, absent };
}
