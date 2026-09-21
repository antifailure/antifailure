#!/usr/bin/env node
// The run page's verdicts, measured in a real browser at every width that
// matters.
//
// THE FAILURE THIS EXISTS FOR. The verdicts table carried the reproduction as
// its seventh column and was given a minimum of 860px to hold it. With the
// 236px rail beside it, a laptop window between 640 and 1279 wide has less
// room than that, so the table scrolled sideways inside its card and the
// Reproduction column was cut off at the card's edge. It was seen in a film
// frame at 1120. At wider windows the column fitted and was a 259px box
// scrolling a 780px sentence, so the reproduction was cut off inside its own
// box instead. Nothing in `npm test` could see either, because both are
// facts about layout and the unit tests never lay anything out.
//
// WHAT IT DOES. Serves the static export in out/ with a fixture control plane
// on the same origin, the way the real control plane serves it, opens
// /runs?run=<fixture> in headless Chrome at each width, and asserts on the
// render: nothing inside the verdicts card scrolls or paints past its box, the
// document is no wider than the window, and every line of the reproduction is
// on screen. The fixture's reproduction is the shape the runner actually
// writes (runner/src/execute.ts), sentences and a long command line, because a
// short tidy fixture is the one that fits.
//
// It REFUSES rather than skipping. No build, no browser, a width the emulation
// did not apply, or a page that never rendered the fixture each fail the run,
// because a layout check that could not look and says nothing reads exactly
// like one that looked and found nothing.
//
// No dependency: Node has a WebSocket client built in, and the DevTools
// protocol is JSON over it, the same approach as tools/preview/shots.mjs.

import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { existsSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const out = path.resolve(here, "..", "out");
const fixture = JSON.parse(readFileSync(path.join(here, "layout-fixture.json"), "utf8"));

// Phone widths stack the table; 640 is the first width it is a table again and
// the narrowest one it has to fit; 1024 is the first with the rail; 1120 is the
// width it was seen clipped at; the rest are the common laptop widths.
const WIDTHS = [360, 390, 430, 640, 768, 1024, 1120, 1280, 1440, 1512, 1600];

function fail(message) {
  console.error(`layout: ${message}`);
  process.exitCode = 1;
}

// ---------------------------------------------------------------------------
// The origin: the export, and a control plane that answers with the fixture.
// ---------------------------------------------------------------------------

const TYPES = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript",
  ".css": "text/css",
  ".json": "application/json",
  ".txt": "text/plain; charset=utf-8",
  ".svg": "image/svg+xml",
  ".woff2": "font/woff2",
  ".png": "image/png",
  ".ico": "image/x-icon",
};

function fileFor(pathname) {
  const clean = path.normalize(decodeURIComponent(pathname)).replace(/^([/\\])+/, "");
  const candidates = [clean, `${clean}.html`, path.join(clean, "index.html")];
  for (const candidate of candidates) {
    const full = path.join(out, candidate);
    if (!full.startsWith(out)) return null;
    if (existsSync(full) && statSync(full).isFile()) return full;
  }
  return null;
}

function serve() {
  const server = createServer((req, res) => {
    const url = new URL(req.url ?? "/", "http://fixture");
    if (url.pathname === "/auth/session") {
      res.setHeader("content-type", "application/json");
      res.end(JSON.stringify(fixture.session));
      return;
    }
    if (url.pathname.startsWith("/trpc/")) {
      const procedure = url.pathname.slice("/trpc/".length);
      res.setHeader("content-type", "application/json");
      const data = procedure in fixture.trpc ? fixture.trpc[procedure] : null;
      res.end(JSON.stringify({ result: { data } }));
      return;
    }
    const file = fileFor(url.pathname);
    if (file === null) {
      res.statusCode = 404;
      res.end("not found");
      return;
    }
    res.setHeader("content-type", TYPES[path.extname(file)] ?? "application/octet-stream");
    res.end(readFileSync(file));
  });
  return new Promise((resolve) => server.listen(0, "127.0.0.1", () => resolve(server)));
}

// ---------------------------------------------------------------------------
// The browser
// ---------------------------------------------------------------------------

/** A Chrome to drive: named, on PATH, or the newest one Playwright cached. */
function findBrowser() {
  if (process.env.AF_CHROME) return process.env.AF_CHROME;
  for (const dir of (process.env.PATH ?? "").split(path.delimiter)) {
    for (const name of ["google-chrome", "google-chrome-stable", "chromium", "chromium-browser"]) {
      const full = path.join(dir, name);
      if (existsSync(full)) return full;
    }
  }
  const caches = [
    path.join(os.homedir(), "Library", "Caches", "ms-playwright"),
    path.join(os.homedir(), ".cache", "ms-playwright"),
  ];
  for (const cache of caches) {
    if (!existsSync(cache)) continue;
    const builds = readdirSync(cache)
      .filter((name) => /^chromium-\d+$/.test(name))
      .sort((a, b) => Number(b.split("-")[1]) - Number(a.split("-")[1]));
    for (const build of builds) {
      for (const tail of [
        ["chrome-mac-arm64", "Google Chrome for Testing.app", "Contents", "MacOS", "Google Chrome for Testing"],
        ["chrome-mac", "Google Chrome for Testing.app", "Contents", "MacOS", "Google Chrome for Testing"],
        ["chrome-linux64", "chrome"],
        ["chrome-linux", "chrome"],
      ]) {
        const full = path.join(cache, build, ...tail);
        if (existsSync(full)) return full;
      }
    }
  }
  return null;
}

async function launch(binary) {
  const profile = mkdtempSync(path.join(os.tmpdir(), "af-console-layout-"));
  const child = spawn(
    binary,
    [
      "--headless=new",
      "--remote-debugging-port=0",
      `--user-data-dir=${profile}`,
      "--no-first-run",
      "--no-default-browser-check",
      "--no-sandbox",
      "--hide-scrollbars",
      "about:blank",
    ],
    { stdio: ["ignore", "ignore", "pipe"] },
  );
  const endpoint = await new Promise((resolve, reject) => {
    let seen = "";
    const timer = setTimeout(() => reject(new Error(`the browser never announced a debugging port: ${seen.slice(-400)}`)), 20_000);
    child.stderr.on("data", (chunk) => {
      seen += chunk.toString();
      const match = seen.match(/DevTools listening on (ws:\/\/\S+)/);
      if (match) {
        clearTimeout(timer);
        resolve(match[1]);
      }
    });
    child.on("exit", (code) => reject(new Error(`the browser exited with ${code}: ${seen.slice(-400)}`)));
  });
  return {
    endpoint,
    // Waits for the process to be gone before removing its profile, because a
    // browser still flushing into the directory makes the removal fail.
    async close() {
      if (child.exitCode === null) {
        const gone = new Promise((resolve) => child.once("exit", resolve));
        child.kill("SIGKILL");
        await gone;
      }
      rmSync(profile, { recursive: true, force: true, maxRetries: 5 });
    },
  };
}

function connect(url) {
  return new Promise((resolve, reject) => {
    const socket = new WebSocket(url);
    const pending = new Map();
    let nextId = 1;
    socket.addEventListener("message", (event) => {
      const message = JSON.parse(event.data);
      if (message.id === undefined) return;
      const waiting = pending.get(message.id);
      if (!waiting) return;
      pending.delete(message.id);
      if (message.error) waiting.reject(new Error(message.error.message));
      else waiting.resolve(message.result);
    });
    socket.addEventListener("error", () => reject(new Error("the browser socket failed")));
    socket.addEventListener("open", () =>
      resolve({
        send(method, params = {}, sessionId) {
          const id = nextId++;
          socket.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
          return new Promise((ok, bad) => pending.set(id, { resolve: ok, reject: bad }));
        },
        close: () => socket.close(),
      }),
    );
  });
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// ---------------------------------------------------------------------------
// The measurement, run inside the page.
// ---------------------------------------------------------------------------

const MEASURE = `(() => {
  const card = [...document.querySelectorAll("section")].find(
    (s) => s.querySelector("h2")?.textContent === "Verdicts",
  );
  if (!card || !card.querySelector("table")) return { ready: false };
  const text = card.innerText;
  const box = card.getBoundingClientRect();
  const offenders = [];
  for (const el of card.querySelectorAll("*")) {
    const style = getComputedStyle(el);
    if (style.display === "none") continue;
    const name = el.tagName.toLowerCase() + (el.textContent || "").trim().slice(0, 40).replace(/\\s+/g, " ");
    // Something that scrolls sideways is hiding part of what it holds.
    if (style.overflowX !== "visible" && el.scrollWidth > el.clientWidth + 1) {
      offenders.push({ what: "scrolls", el: name, scroll: el.scrollWidth, client: el.clientWidth });
    }
    // Something that paints past the card is cut off by its overflow-hidden.
    const r = el.getBoundingClientRect();
    if (r.width > 0 && r.right > box.right + 0.5) {
      offenders.push({ what: "past the card", el: name, right: Math.round(r.right), card: Math.round(box.right) });
    }
  }
  return {
    ready: true,
    innerWidth,
    docWidth: document.documentElement.scrollWidth,
    text,
    offenders: offenders.slice(0, 6),
    count: offenders.length,
  };
})()`;

async function measure(client, sessionId, base, width) {
  await client.send(
    "Emulation.setDeviceMetricsOverride",
    { width, height: 900, deviceScaleFactor: 1, mobile: false },
    sessionId,
  );
  await client.send("Page.navigate", { url: `${base}/runs?run=${fixture.runId}` }, sessionId);
  // Poll for the fixture's own text rather than for a timer: the rows arrive
  // after the first paint, and a skeleton measures as a table with no overflow.
  const marker = fixture.marker;
  for (let attempt = 0; attempt < 100; attempt += 1) {
    await sleep(100);
    const { result } = await client.send("Runtime.evaluate", { expression: MEASURE, returnByValue: true }, sessionId);
    const value = result.value;
    if (value?.ready && value.text.includes(marker)) {
      // One more frame, so a late web font cannot change the widths after this.
      await sleep(150);
      const again = await client.send("Runtime.evaluate", { expression: MEASURE, returnByValue: true }, sessionId);
      return again.result.value;
    }
  }
  return null;
}

async function main() {
  if (!existsSync(path.join(out, "runs.html"))) {
    fail("console/out/runs.html does not exist. Build the console first: npm run build");
    return;
  }
  const binary = findBrowser();
  if (binary === null) {
    fail("no Chrome found. Set AF_CHROME to one, put google-chrome on PATH, or install one with Playwright");
    return;
  }
  const server = await serve();
  const base = `http://127.0.0.1:${server.address().port}`;
  const browser = await launch(binary);
  const client = await connect(browser.endpoint);
  try {
    const { targetId } = await client.send("Target.createTarget", { url: "about:blank" });
    const { sessionId } = await client.send("Target.attachToTarget", { targetId, flatten: true });
    await client.send("Page.enable", {}, sessionId);
    await client.send("Runtime.enable", {}, sessionId);

    const steps = fixture.trpc["runs.verdicts"].flatMap((v) => (Array.isArray(v.reproduction) ? v.reproduction : []));
    for (const width of WIDTHS) {
      const m = await measure(client, sessionId, base, width);
      if (m === null) {
        fail(`${width}: the verdicts never rendered the fixture, so nothing was measured`);
        continue;
      }
      if (m.innerWidth !== width) {
        fail(`${width}: the emulated width did not apply, the page reports ${m.innerWidth}`);
        continue;
      }
      const problems = [];
      if (m.docWidth > m.innerWidth) problems.push(`the document is ${m.docWidth}px wide in a ${m.innerWidth}px window`);
      if (m.count > 0) problems.push(`${m.count} element(s) clipped, first: ${JSON.stringify(m.offenders)}`);
      const missing = steps.filter((line) => !m.text.replace(/\s+/g, " ").includes(line.replace(/\s+/g, " ")));
      if (missing.length > 0) problems.push(`reproduction lines not on screen: ${JSON.stringify(missing)}`);
      if (problems.length > 0) fail(`${width}: ${problems.join("; ")}`);
      else console.log(`layout: ${width} ok, nothing in the verdicts is clipped (document ${m.docWidth}px)`);
    }
  } finally {
    client.close();
    server.close();
    await browser.close();
  }
  if (process.exitCode) console.error("layout: FAILED");
  else console.log(`layout: all ${WIDTHS.length} widths measured, none clipped`);
}

main().catch((error) => {
  fail(error instanceof Error ? error.message : String(error));
});
