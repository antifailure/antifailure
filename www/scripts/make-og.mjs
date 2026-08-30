/**
 * Generates public/og.png, the social card.
 *
 * This has to exist for a reason beyond tidiness: docs/astro.config.mjs has
 * been pointing og:image at https://antifailure.dev/og.png since the
 * documentation site was built, and that URL has been a 404 the whole time. So
 * every documentation page shared anywhere has been rendering a broken image,
 * and the marketing site had no card at all.
 *
 * The design is the logo argued at size. The mark is four corner brackets, a
 * containment boundary, which is the entire product in one glyph: a sealed
 * environment that nothing leaves except where you say it can. So the card is
 * that boundary drawn around the message rather than a gradient with the name
 * on it. Nothing here is decoration that could be swapped onto another
 * company's card.
 *
 * Run: node scripts/make-og.mjs
 */
import sharp from "sharp";
import { mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const DIR = path.dirname(fileURLToPath(import.meta.url));

/**
 * Two sizes, because the two consumers disagree.
 *
 * 1200x630 is what OpenGraph and Twitter want and is what the site serves at
 * /og.png. GitHub's repository social preview is 1280x640, a different aspect
 * ratio, and it centre-crops anything else. There is no REST endpoint for
 * uploading it, so the second file exists to be dragged into
 * Settings > General > Social preview by hand. Without it GitHub generates a
 * grey card from the repo name, which is what antifailure/antifailure has been
 * showing on every link anybody has ever shared.
 */
const TARGETS = [
  { file: path.join(DIR, "..", "public", "og.png"), w: 1200, h: 630 },
  { file: path.join(DIR, "..", "..", "assets", "github-social-preview.png"), w: 1280, h: 640 },
];

function card(W, H) {
const INK = "#101014";
const NEON = "#33bf00";
const GROUND = "#f7f7f5";
const DIM = "#61646b";

// Corner brackets, inset from the edge. Same geometry as app/icon.svg: an open
// square whose sides stop short of the corners, drawn here at poster scale.
const M = 44; // inset from the canvas edge
const L = 116; // arm length
const SW = 5; // stroke width

function bracket(x, y, sx, sy) {
  // One corner: a horizontal arm and a vertical arm meeting at (x, y).
  // sx/sy are +1 or -1 to point the arms inward from that corner.
  return `M ${x + sx * L} ${y} L ${x} ${y} L ${x} ${y + sy * L}`;
}

const corners = [
  bracket(M, M, 1, 1),
  bracket(W - M, M, -1, 1),
  bracket(W - M, H - M, -1, -1),
  bracket(M, H - M, 1, -1),
].join(" ");

const FONT = "Helvetica Neue, Helvetica, Arial, sans-serif";
const MONO = "SF Mono, Menlo, Consolas, monospace";

const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${W}" height="${H}" viewBox="0 0 ${W} ${H}">
  <rect width="${W}" height="${H}" fill="${GROUND}"/>

  <!-- The containment boundary. Green, because in the product green is the
       colour of a decision that held. -->
  <path d="${corners}" fill="none" stroke="${NEON}" stroke-width="${SW}" stroke-linecap="square"/>

  <!-- Wordmark, with the mark itself at text size beside it. -->
  <g transform="translate(104 108)">
    <path d="M1.8 6.4V1.8H6.4M11.6 1.8H16.2V6.4M16.2 11.6V16.2H11.6M6.4 16.2H1.8V11.6"
          transform="scale(1.55)" fill="none" stroke="${NEON}" stroke-width="2.1" stroke-linecap="square"/>
    <text x="46" y="22" font-family="${FONT}" font-size="25" font-weight="600"
          letter-spacing="-0.4" fill="${INK}">Antifailure</text>
  </g>

  <!-- The claim. Two lines, set tight, because a headline set at default
       leading at this size collides with itself. -->
  <text x="104" y="290" font-family="${FONT}" font-size="76" font-weight="700"
        letter-spacing="-3.2" fill="${INK}">Know what happens</text>
  <text x="104" y="368" font-family="${FONT}" font-size="76" font-weight="700"
        letter-spacing="-3.2" fill="${INK}">before you deploy.</text>

  <text x="104" y="428" font-family="${FONT}" font-size="25" font-weight="400"
        letter-spacing="-0.5" fill="${DIM}">A disposable copy of your production stack for every pull request.</text>

  <!-- The commands, because the audience is developers and this is the part
       they actually want to see.

       Written as one flat line with no newlines inside the element. An earlier
       version broke the tspans across source lines for readability and the SVG
       renderer collapsed the surrounding whitespace, so it shipped as
       "af up ·af test·af down" with the separators jammed against the text.
       xml:space="preserve" keeps the padding around each separator. -->
  <text x="104" y="516" font-family="${MONO}" font-size="21" letter-spacing="-0.2" fill="${INK}" xml:space="preserve">af up<tspan fill="${DIM}">   ·   </tspan>af test<tspan fill="${DIM}">   ·   </tspan>af down</text>

  <text x="104" y="556" font-family="${MONO}" font-size="19" letter-spacing="-0.1" fill="${NEON}">antifailure.dev</text>
</svg>`;

return svg;
}

for (const { file, w, h } of TARGETS) {
  const png = await sharp(Buffer.from(card(w, h))).png({ compressionLevel: 9 }).toBuffer();
  mkdirSync(path.dirname(file), { recursive: true });
  writeFileSync(file, png);
  const meta = await sharp(png).metadata();
  if (meta.width !== w || meta.height !== h) {
    console.error(`${file} came out ${meta.width}x${meta.height}, expected ${w}x${h}`);
    process.exit(1);
  }
  console.log(`wrote ${path.relative(process.cwd(), file)} ${w}x${h} ${(png.length / 1024).toFixed(1)}KB`);
}
