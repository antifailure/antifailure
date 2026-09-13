/**
 * The HTML entities this site's rendered pages carry, decoded for text that is
 * written into a markdown twin or compared as plain text.
 *
 * One function, imported by twin.mjs, which writes the twins, and by
 * twin-cells.mjs, which is the check that compares a page with its twin. Two
 * copies of this chain were two chances to decode differently, and the check
 * would then have measured the difference between two decoders rather than the
 * difference between a page and its twin.
 */
export function decodeEntities(s) {
  return s
    .replace(/&nbsp;/g, " ")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&quot;/g, '"')
    .replace(/&#(\d+);/g, (_, d) => String.fromCharCode(+d))
    .replace(/&#x([0-9a-f]+);/gi, (_, h) => String.fromCharCode(parseInt(h, 16)))
    // &amp; LAST. Decoding it first turns the text a page shows as "&lt;b&gt;",
    // which its HTML carries as &amp;lt;b&amp;gt;, into "<b>": a different
    // sentence, and the twins published exactly that.
    .replace(/&amp;/g, "&");
}
