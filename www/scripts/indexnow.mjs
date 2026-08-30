/**
 * Tells Bing what changed, immediately, instead of waiting to be crawled.
 *
 * This matters more than its reputation suggests. Bing's index is the
 * retrieval layer behind ChatGPT search and Copilot, so a page Bing has not
 * indexed cannot be cited there no matter how it ranks on Google. IndexNow is
 * the only mechanism that pushes rather than waits, and it is free.
 *
 * Google does not participate. Yandex, Seznam and Naver do, and all
 * participants share submissions, so one POST reaches all of them.
 *
 * The key is not a secret. IndexNow proves domain ownership by fetching the
 * key back from the site root, which is the whole point of publishing it at
 * public/<key>.txt, so it is committed like any other file.
 *
 * Only recently changed URLs are submitted. Re-submitting the whole sitemap on
 * every deploy is what the protocol asks people not to do, and it degrades the
 * signal for everybody including this site. `lastmod` in the sitemap comes from
 * real git history, so it is a truthful basis for deciding what is new.
 *
 * Run after the site is built and published:
 *   node scripts/indexnow.mjs [--dry-run] [--days N]
 */
import { readFileSync, readdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = path.join(path.dirname(fileURLToPath(import.meta.url)), "..");
const OUT = path.join(ROOT, "out");
const ENDPOINT = "https://api.indexnow.org/indexnow";

const args = process.argv.slice(2);
/**
 * Submitting is opt in, and running this by hand prints instead.
 *
 * The default used to be the other way around, with `--dry-run` to hold it
 * back, and that is a bad shape for a script whose whole job is to POST to
 * somebody else's service. Reading it to see what it would do submitted 25
 * URLs to Bing. Nothing broke, because IndexNow verifies ownership by fetching
 * the key file back from the site root and that file was not live yet, so the
 * batch was discarded. It could as easily have been a live host and a list of
 * pages that had not shipped.
 *
 * So: the workflow passes --submit after a successful publish, and a person
 * running it gets the list and nothing else.
 */
const submit = args.includes("--submit");
const days = Number(args[args.indexOf("--days") + 1]) || 7;

/** The key is whichever <32 hex>.txt sits in the published root. */
function findKey() {
  const match = readdirSync(OUT).find((f) => /^[0-9a-f]{32}\.txt$/.test(f));
  if (!match) {
    throw new Error(
      "no IndexNow key file in out/. It should be public/<32 hex>.txt, and its " +
        "contents must equal its filename without the extension.",
    );
  }
  const key = match.replace(/\.txt$/, "");
  const body = readFileSync(path.join(OUT, match), "utf8").trim();
  if (body !== key) {
    throw new Error(
      `${match} contains "${body}" but must contain "${key}". IndexNow fetches ` +
        "this file and compares the two; a mismatch fails verification silently.",
    );
  }
  return key;
}

function recentUrls() {
  const sitemap = readFileSync(path.join(OUT, "sitemap.xml"), "utf8");
  const cutoff = Date.now() - days * 24 * 60 * 60 * 1000;
  const entries = [...sitemap.matchAll(/<url>([\s\S]*?)<\/url>/g)];

  return entries
    .map((m) => ({
      loc: m[1].match(/<loc>([^<]+)<\/loc>/)?.[1],
      lastmod: m[1].match(/<lastmod>([^<]+)<\/lastmod>/)?.[1],
    }))
    .filter((e) => e.loc && e.lastmod && Date.parse(e.lastmod) >= cutoff)
    .map((e) => e.loc);
}

const key = findKey();
const urlList = recentUrls();
const host = new URL(urlList[0] ?? "https://antifailure.dev").host;

if (urlList.length === 0) {
  console.log(`IndexNow: nothing changed in the last ${days} days. Not submitting.`);
  process.exit(0);
}

const payload = {
  host,
  key,
  keyLocation: `https://${host}/${key}.txt`,
  urlList,
};

console.log(`IndexNow: ${urlList.length} URL(s) changed in the last ${days} days`);
for (const u of urlList) console.log(`  ${u}`);

if (!submit) {
  console.log(
    "\nNot submitting. This prints by default; pass --submit to actually send " +
      "the batch to IndexNow, which is what deploy.yml does after a publish.",
  );
  process.exit(0);
}

const res = await fetch(ENDPOINT, {
  method: "POST",
  headers: { "content-type": "application/json; charset=utf-8" },
  body: JSON.stringify(payload),
});

// 200 accepted, 202 accepted but key still being validated. Both are success.
if (res.status === 200 || res.status === 202) {
  console.log(`\nIndexNow accepted the submission (HTTP ${res.status}).`);
  process.exit(0);
}

// A failure here must not fail the deploy. The site is already published and
// live; this is a notification that it exists, and the crawler will find it
// anyway. Loud, but not fatal.
console.error(`\nIndexNow returned HTTP ${res.status}: ${(await res.text()).slice(0, 300)}`);
console.error("The site is published regardless. 403 usually means the key file is not reachable yet.");
process.exit(0);
