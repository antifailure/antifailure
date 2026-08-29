/**
 * Encodes the hero art from assets/hero/*.png to public/home/*.{avif,webp}.
 *
 * Why this exists
 * ---------------
 * The site was shipping nine 1536x1024 PNGs totalling 15.4MB out of public/,
 * and /home/hero-aurora.png (1.9MB of it) was in a <link rel="preload"> on the
 * home page. Preload is a mandatory high-priority fetch, so that file was
 * competing with the stylesheet and the fonts for the first bytes on the
 * connection. That is a Largest Contentful Paint failure on its own, before
 * anything else on the page has had a chance to be slow.
 *
 * PNG is lossless, and these are soft gradients and grain. It is the worst
 * available container for them. AVIF reaches a visually identical result at
 * about 2% of the bytes, verified by comparing 1:1 crops of the text-bearing
 * one rather than by trusting the number.
 *
 * Why the sources live outside public/
 * ------------------------------------
 * Everything in public/ is copied verbatim into the export. Leaving the PNGs
 * there would ship 15.4MB that no browser ever requests, because <Picture>
 * only ever references the AVIF and WebP. The sources stay in assets/, which
 * is not served, so they remain in the repository for re-encoding without
 * being deployed.
 *
 * Two widths, because every one of these is decorative background art placed
 * with `fill`, and a 390px phone has no use for 1536px of it.
 *
 * Run: node scripts/optimize-images.mjs   (also runs as part of `npm run build`)
 */
/*
 * sharp is deliberately NOT in package.json, and that needs saying out loud
 * because it looks like an oversight.
 *
 * next declares sharp as an optional dependency and it is what next/image uses
 * to encode, so it is already in the tree and the lock file. Adding it as a
 * direct devDependency looks tidier and breaks `npm ci`: sharp's optional
 * platform packages resolve per architecture, so a lock file regenerated on a
 * macOS laptop omits what a Linux runner needs, and `npm ci` refuses to
 * install when package.json and the lock disagree. That failed CI twice, once
 * for @emnapi/wasi-threads and once for @emnapi/core and @emnapi/runtime.
 * Generating the lock with --os=linux --cpu=x64 fixes one of the three and not
 * the others.
 *
 * So the dependency is taken from next, and the import is guarded below so
 * that if that ever stops being true the build says which line to read rather
 * than printing ERR_MODULE_NOT_FOUND.
 */
import { existsSync, mkdirSync, readdirSync, statSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

let sharp;
try {
  ({ default: sharp } = await import("sharp"));
} catch (cause) {
  console.error(
    "cannot load sharp, so the hero art cannot be encoded and the site would " +
      "deploy with no images at all.\n" +
      "sharp comes from next's optional dependencies. If next has dropped it, " +
      "add it to package.json AND regenerate package-lock.json on Linux, not " +
      "on a laptop: see the comment at the top of this file for why.",
  );
  throw cause;
}

const ROOT = path.join(path.dirname(fileURLToPath(import.meta.url)), "..");
const SRC = path.join(ROOT, "assets", "hero");
const OUT = path.join(ROOT, "public", "home");

const WIDTHS = [768, 1536];

/**
 * Only these are referenced by a component. cta-atmosphere.png and
 * sage-noise.png were in public/ with no reference anywhere in app/,
 * components/, lib/ or the stylesheet: 3.5MB deployed to no purpose. They stay
 * in assets/ in case they are wanted later, and are simply not encoded.
 */
const USED = [
  "firewall-log.png",
  "hero-aurora.png",
  "ide-stage.png",
  "lock-chart.png",
  "safe-state.png",
  "twin-graph.png",
  "twin-stack.png",
];

if (!existsSync(SRC)) {
  console.error(`no source directory at ${SRC}`);
  process.exit(1);
}
mkdirSync(OUT, { recursive: true });

const present = new Set(readdirSync(SRC));
const missing = USED.filter((f) => !present.has(f));
if (missing.length > 0) {
  console.error(`missing source art: ${missing.join(", ")}`);
  process.exit(1);
}

let before = 0;
let after = 0;

for (const file of USED) {
  const src = path.join(SRC, file);
  const stem = file.replace(/\.png$/, "");
  before += statSync(src).size;

  for (const width of WIDTHS) {
    const suffix = width === 1536 ? "" : `-${width}`;
    const base = sharp(src).resize({ width, withoutEnlargement: true });

    // effort 6 is the practical ceiling: past it the encoder spends minutes for
    // fractions of a percent. quality 62 was checked against the one image that
    // contains text (firewall-log) by diffing 1:1 crops; the log lines stay
    // sharp, so it is safe for the smooth ones too.
    await base.clone().avif({ quality: 62, effort: 6 }).toFile(path.join(OUT, `${stem}${suffix}.avif`));
    await base.clone().webp({ quality: 76, effort: 6 }).toFile(path.join(OUT, `${stem}${suffix}.webp`));
  }

  const orig = statSync(src).size;
  const opt = statSync(path.join(OUT, `${stem}.avif`)).size;
  after += opt;
  console.log(
    `${file.padEnd(22)} ${(orig / 1024).toFixed(0).padStart(5)}KB -> ` +
      `${(opt / 1024).toFixed(0).padStart(4)}KB avif  (${(100 - (opt / orig) * 100).toFixed(0)}% smaller)`,
  );
}

console.log(
  `\n${(before / 1024 / 1024).toFixed(1)}MB PNG -> ${(after / 1024 / 1024).toFixed(2)}MB AVIF at 1536w ` +
    `(${(100 - (after / before) * 100).toFixed(1)}% smaller). Two unused sources were not encoded.`,
);
