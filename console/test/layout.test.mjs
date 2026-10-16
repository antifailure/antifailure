// The layout check must END when the browser does not come up.
//
// On 2026-10-05 the www job on #618 printed "the browser never announced a
// debugging port" and then sat silent for eleven minutes until the job's
// timeout cancelled it. The launch had failed and been reported, but the
// browser it spawned and the server it started were never released, so Node
// stayed up. A cancelled job reads as a slow one, not as a refusal, and it
// masks everything after it in the job.
//
// These run the real script against a fake browser, so they need no Chrome
// and no console build.

import { spawn } from "node:child_process";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";

const script = path.join(path.dirname(fileURLToPath(import.meta.url)), "layout.mjs");

/** Runs layout.mjs against a fake browser whose body is given, and returns how it ended. */
async function runAgainst(fakeBody) {
  const dir = mkdtempSync(path.join(os.tmpdir(), "af-layout-test-"));
  const out = path.join(dir, "out");
  const pidFile = path.join(dir, "browser.pid");
  // The script refuses before launching when there is no build, so give it one.
  mkdirSync(out);
  writeFileSync(path.join(out, "runs.html"), "<!doctype html><title>runs</title>");
  const fake = path.join(dir, "fake-chrome");
  writeFileSync(
    fake,
    `#!${process.execPath}\nrequire("node:fs").writeFileSync(${JSON.stringify(pidFile)}, String(process.pid));\n${fakeBody}\n`,
  );
  chmodSync(fake, 0o755);

  const started = Date.now();
  const child = spawn(process.execPath, [script], {
    env: { ...process.env, AF_CHROME: fake, AF_LAYOUT_OUT: out, AF_LAYOUT_LAUNCH_MS: "500" },
    stdio: ["ignore", "pipe", "pipe"],
  });
  let stderr = "";
  child.stderr.on("data", (chunk) => (stderr += chunk));
  const code = await new Promise((resolve) => {
    const guard = setTimeout(() => {
      child.kill("SIGKILL");
      resolve("hung");
    }, 15_000);
    child.on("exit", (c) => {
      clearTimeout(guard);
      resolve(c);
    });
  });
  const pid = existsSync(pidFile) ? Number(readFileSync(pidFile, "utf8")) : null;
  let browserAlive = false;
  if (pid !== null) {
    try {
      process.kill(pid, 0);
      browserAlive = true;
      process.kill(pid, "SIGKILL");
    } catch {
      browserAlive = false;
    }
  }
  rmSync(dir, { recursive: true, force: true });
  return { code, stderr, seconds: (Date.now() - started) / 1000, pid, browserAlive };
}

test("a browser that never announces a port fails the run promptly", async () => {
  const r = await runAgainst("setInterval(() => {}, 1 << 30);");
  assert.notEqual(r.code, "hung", `the run hung after: ${r.stderr}`);
  assert.equal(r.code, 1, "a check that could not look fails");
  assert.match(r.stderr, /never announced a debugging port/);
});

test("a browser that never announces a port is not left running", async () => {
  const r = await runAgainst("setInterval(() => {}, 1 << 30);");
  assert.notEqual(r.pid, null, "the fake browser started, so this measured something");
  assert.equal(r.browserAlive, false, "the browser the run spawned is gone when the run is");
});

test("a browser that exits at once fails the run promptly", async () => {
  const r = await runAgainst("process.exit(3);");
  assert.notEqual(r.code, "hung", `the run hung after: ${r.stderr}`);
  assert.equal(r.code, 1);
  assert.match(r.stderr, /the browser exited with 3/);
});
