import { createRequire } from "node:module";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const require = createRequire(import.meta.url);
const puppeteer = require("/tmp/ui-verify/node_modules/puppeteer-core/lib/puppeteer/puppeteer-core.js");

const CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
const BASE = "http://127.0.0.1:3000";
const ROOT = "/Users/maksymrajszewski/UIFORBACK/.verify-shots/product-polish";

const PAGES = [
  { path: "/product", dir: "overview" },
  { path: "/product/twins", dir: "twins" },
];

const VIEWPORTS = [
  { name: "1440", width: 1440, height: 900 },
  { name: "390", width: 390, height: 844 },
];

function rectsIntersect(a, b, pad = 0.5) {
  return !(
    a.x + a.w - pad <= b.x ||
    b.x + b.w - pad <= a.x ||
    a.y + a.h - pad <= b.y ||
    b.y + b.h - pad <= a.y
  );
}

async function measure(page) {
  return page.evaluate(() => {
    const vw = document.documentElement.clientWidth;
    const overflowers = [];
    for (const el of document.querySelectorAll("body *")) {
      const r = el.getBoundingClientRect();
      if (r.width < 1 || r.height < 1) continue;
      if (r.right > vw + 1) {
        overflowers.push({
          tag: el.tagName,
          cls: (el.className || "").toString().slice(0, 80),
          right: Math.round(r.right),
          vw,
        });
        if (overflowers.length > 12) break;
      }
    }

    const texts = [];
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    let node;
    while ((node = walker.nextNode())) {
      const t = node.textContent?.trim();
      if (!t) continue;
      const parent = node.parentElement;
      if (!parent) continue;
      const style = getComputedStyle(parent);
      if (style.visibility === "hidden" || style.display === "none") continue;
      const range = document.createRange();
      range.selectNodeContents(node);
      const rects = [...range.getClientRects()]
        .filter((r) => r.width > 2 && r.height > 2)
        .map((r) => ({ x: r.x, y: r.y, w: r.width, h: r.height, t: t.slice(0, 40) }));
      texts.push(...rects);
    }
    const hits = [];
    for (let i = 0; i < texts.length; i++) {
      for (let j = i + 1; j < texts.length; j++) {
        const a = texts[i];
        const b = texts[j];
        const overlapX = Math.min(a.x + a.w, b.x + b.w) - Math.max(a.x, b.x);
        const overlapY = Math.min(a.y + a.h, b.y + b.h) - Math.max(a.y, b.y);
        if (overlapX > 1.5 && overlapY > 1.5) {
          hits.push({ a: a.t, b: b.t, ox: Math.round(overlapX), oy: Math.round(overlapY) });
          if (hits.length > 16) break;
        }
      }
      if (hits.length > 16) break;
    }

    const wells = [...document.querySelectorAll('[class*="rounded-[32px]"]')].map((el, i) => {
      const wr = el.getBoundingClientRect();
      const win = el.querySelector('[class*="rounded-[16px]"]');
      const wir = win ? win.getBoundingClientRect() : null;
      const topGap = wir ? wir.top - wr.top : null;
      const botGap = wir ? wr.bottom - wir.bottom : null;
      return {
        i,
        wellH: Math.round(wr.height),
        winH: wir ? Math.round(wir.height) : null,
        topGap: topGap != null ? Math.round(topGap) : null,
        botGap: botGap != null ? Math.round(botGap) : null,
        emptyBoth: topGap != null && botGap != null && topGap >= 80 && botGap >= 80,
      };
    });

    return {
      scrollWidth: document.documentElement.scrollWidth,
      clientWidth: document.documentElement.clientWidth,
      overflow: document.documentElement.scrollWidth > document.documentElement.clientWidth + 1,
      overflowers,
      textHits: hits,
      wells,
    };
  });
}

const browser = await puppeteer.launch({
  executablePath: CHROME,
  headless: "new",
  args: ["--no-sandbox", "--disable-gpu", "--hide-scrollbars"],
});

const report = [];

try {
  for (const pageSpec of PAGES) {
    for (const vp of VIEWPORTS) {
      const page = await browser.newPage();
      await page.setViewport({ width: vp.width, height: vp.height, deviceScaleFactor: 1 });
      await page.goto(`${BASE}${pageSpec.path}`, { waitUntil: "networkidle0", timeout: 60000 });
      await page.waitForSelector('[class*="rounded-[32px]"]', { timeout: 15000 });
      await new Promise((r) => setTimeout(r, 400));

      const dir = join(ROOT, pageSpec.dir);
      mkdirSync(dir, { recursive: true });
      const fullPath = join(dir, `full-${vp.name}.png`);
      await page.screenshot({ path: fullPath, fullPage: true });

      const wells = await page.$$('[class*="rounded-[32px]"]');
      for (let i = 0; i < wells.length; i++) {
        const shot = join(dir, `well-${i + 1}-${vp.name}.png`);
        await wells[i].screenshot({ path: shot });
      }

      const metrics = await measure(page);
      report.push({ page: pageSpec.path, vp: vp.name, wellsShot: wells.length, ...metrics });
      await page.close();
    }
  }
} finally {
  await browser.close();
}

writeFileSync(join(ROOT, "metrics.json"), JSON.stringify(report, null, 2));
console.log(JSON.stringify(report, null, 2));
