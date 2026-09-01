import { execFileSync } from "node:child_process";
import path from "node:path";

/**
 * Facts about the documentation site, counted rather than remembered.
 *
 * WHY THIS FILE EXISTS. `llms.txt` offered "all 41 documentation pages as one
 * plain-text file" and there were 81. The sentence was written when it was
 * true, forty pages were added, and nothing anywhere connected the number to
 * the thing it counted. The audience makes it worse than an ordinary stale
 * number: `llms.txt` is read by models deciding whether one fetch will answer
 * a question, so understating by half is advice to go and crawl the site
 * instead. The identical number sat in a comment in `docs/src/pages/`.
 *
 * This is the same shape as `lastmod.ts`, which asks git when a page last
 * changed rather than stamping the build clock, and for the same reason: a
 * number a build can compute must never be a number a person retypes.
 *
 * It runs at BUILD TIME only. `llms.txt` and `llms-full.txt` are static
 * routes, so this executes once during `next build` and the answer is baked
 * into the generated text.
 *
 * IT THROWS RATHER THAN GUESSING. There is no fallback constant, deliberately.
 * A fallback here would be indistinguishable downstream from a counted answer
 * and would reintroduce exactly the defect this removes, quietly. A build that
 * cannot see the documentation tree should stop and say so, which is the trade
 * `lastmod.ts` argues for in its own header.
 */

/** Where the documentation pages live, relative to the repository root. */
const DOCS_DIR = "docs/src/content/docs";

/**
 * Candidate roots, because the build's working directory is `www` on Vercel
 * and in `just` alike, and a test may run from somewhere else. Ordered from
 * the most likely.
 */
function candidateRoots(): string[] {
  const cwd = process.cwd();
  return [path.resolve(cwd, ".."), cwd, path.resolve(cwd, "..", "..")];
}

/**
 * Every documentation page, by path, read from git rather than from the disk.
 *
 * git rather than a directory walk for the reason `claimcheck` gives for the
 * same choice: a walk sees whatever happens to be lying around, including a
 * draft nobody committed and any build output, so its answer depends on whose
 * machine it runs on. `git ls-files` answers with what the repository
 * contains, which is what the published site is built from.
 */
export function documentationPages(): string[] {
  let lastError: unknown;
  for (const root of candidateRoots()) {
    try {
      const out = execFileSync("git", ["-C", root, "ls-files", "-z", "--", DOCS_DIR], {
        encoding: "utf8",
        stdio: ["ignore", "pipe", "ignore"],
      });
      const pages = out
        .split("\0")
        .filter((f) => f.endsWith(".md") || f.endsWith(".mdx"))
        .sort();
      if (pages.length > 0) return pages;
    } catch (err) {
      lastError = err;
    }
  }
  throw new Error(
    `docs-facts: no documentation pages found under ${DOCS_DIR} from any of ` +
      `${candidateRoots().join(", ")}. llms.txt states how many pages the ` +
      `full-text file contains and that number is counted here, so a build ` +
      `that cannot see the documentation tree must stop rather than publish a ` +
      `number nobody computed.` +
      (lastError instanceof Error ? ` Last error: ${lastError.message}` : ""),
  );
}

/** How many pages `/docs/llms-full.txt` contains. */
export function documentationPageCount(): number {
  return documentationPages().length;
}
