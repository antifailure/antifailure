import { execFileSync } from "node:child_process";
import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";

/**
 * The changelog, built from `.changes/` and from nothing else.
 *
 * Those fragments existed for months before anything rendered them. 125 of
 * them, written carefully, specific about what changed and about what is off
 * by default, and their only reader in the whole repository was a gate that
 * checks documentation paths. That is this project's most familiar shape: the
 * hard part done, the last step missing, and a capability that looks finished
 * from every angle except use.
 *
 * There is no generated file in the middle. A checked-in CHANGELOG.md was the
 * obvious alternative and it was rejected for a specific reason: an entry's
 * date comes from the commit that landed it, which does not exist until after
 * that commit is written, so `just _generated` would have gone red on every
 * pull request that added a fragment and been right to. A gate that fires on
 * correct work is a gate people delete. This reads the real files at build
 * time instead, so the page cannot be stale by construction.
 *
 * Everything here runs at build time only, in a Node process, from
 * app/changelog/page.tsx.
 */

const WWW = process.cwd();
const REPO = path.join(WWW, "..");
const DIR = path.join(REPO, ".changes");

/** The four a fragment may open with. tools/changecheck refuses a fifth. */
export type Category = "added" | "fixed" | "changed" | "security";
const CATEGORIES: Category[] = ["added", "fixed", "changed", "security"];

export type Span =
  | { kind: "text"; text: string }
  | { kind: "code"; text: string }
  | { kind: "strong"; text: string }
  | { kind: "link"; text: string; href: string };

export type Block = { kind: "p"; spans: Span[] } | { kind: "ul"; items: Span[][] };

/** One `# category` heading and the prose under it. */
export type Section = { category: Category; blocks: Block[] };

export type Entry = {
  /** The fragment's file name without its extension, and the anchor on the page. */
  slug: string;
  sections: Section[];
  /**
   * ISO date of the commit that landed this entry on main, or null when git
   * could not say. Null is rendered as its own group rather than guessed at:
   * a date invented for a changelog is worse than an admitted gap, because
   * nothing downstream can tell it from a real one.
   */
  landed: string | null;
};

export type Day = { date: string | null; entries: Entry[] };

export type Release = {
  /** A tag name, or null for the entries no tag contains yet. */
  tag: string | null;
  /** ISO date the tag was cut. Null for unreleased. */
  date: string | null;
  /** Entries in this release, newest day first. */
  days: Day[];
  /**
   * Why a release has no public entries, when it has none. Two different
   * things look identical on the page and are not: a release cut before
   * fragments existed, and a release every one of whose fragments was
   * internal. Saying which is the difference between an empty section and a
   * dishonest one.
   */
  emptyBecause: "predates the convention" | "every entry was internal" | null;
};

/* -------------------------------------------------------------------------
 * Parsing
 * ---------------------------------------------------------------------- */

const INLINE = /(`[^`]+`|\*\*[^*]+\*\*|\[[^\]]+\]\([^)]+\))/g;

/**
 * Inline markup, and only the three shapes the fragments actually contain.
 *
 * Measured rather than assumed: 783 spans of inline code, two of bold, no
 * fenced blocks, no heading below the first level, and one file using
 * bullets. Writing a general markdown parser for that would be several
 * hundred lines guarding against constructs nothing has ever written. Links
 * are the one shape not present today and supported anyway, because the cost
 * of not supporting them is a raw `[text](url)` shipped to a reader.
 */
function spans(text: string): Span[] {
  const out: Span[] = [];
  let last = 0;
  for (const match of text.matchAll(INLINE)) {
    const at = match.index ?? 0;
    if (at > last) out.push({ kind: "text", text: text.slice(last, at) });
    const token = match[0];
    if (token.startsWith("`")) {
      out.push({ kind: "code", text: token.slice(1, -1) });
    } else if (token.startsWith("**")) {
      out.push({ kind: "strong", text: token.slice(2, -2) });
    } else {
      const cut = token.indexOf("](");
      out.push({
        kind: "link",
        text: token.slice(1, cut),
        href: token.slice(cut + 2, -1),
      });
    }
    last = at + token.length;
  }
  if (last < text.length) out.push({ kind: "text", text: text.slice(last) });
  return out;
}

/**
 * A fragment's sections, in the order they were written.
 *
 * Hard-wrapped prose is joined back into a paragraph. The fragments wrap at 79
 * columns, so rendering their line breaks would put a ragged edge down a page
 * whose measure is set by its own type.
 */
function parse(body: string): Section[] {
  const sections: Section[] = [];
  let current: Section | null = null;
  let paragraph: string[] = [];
  let list: string[] = [];

  const flush = () => {
    if (!current) {
      paragraph = [];
      list = [];
      return;
    }
    if (paragraph.length > 0) {
      current.blocks.push({ kind: "p", spans: spans(paragraph.join(" ")) });
      paragraph = [];
    }
    if (list.length > 0) {
      current.blocks.push({ kind: "ul", items: list.map((item) => spans(item)) });
      list = [];
    }
  };

  for (const raw of body.split("\n")) {
    const line = raw.trimEnd();
    if (line.startsWith("# ")) {
      flush();
      const name = line.slice(2).trim() as Category;
      current = { category: CATEGORIES.includes(name) ? name : "changed", blocks: [] };
      sections.push(current);
      continue;
    }
    if (line.trim() === "") {
      flush();
      continue;
    }
    if (/^\s*-\s+/.test(line)) {
      if (paragraph.length > 0 && current) {
        // A paragraph ends where a list begins, blank line or not.
        current.blocks.push({ kind: "p", spans: spans(paragraph.join(" ")) });
        paragraph = [];
      }
      list.push(line.replace(/^\s*-\s+/, ""));
      continue;
    }
    if (list.length > 0) {
      // A continuation of the last bullet. These fragments wrap at 79
      // columns, so a bullet longer than that arrives as an indented line.
      list[list.length - 1] += ` ${line.trim()}`;
      continue;
    }
    paragraph.push(line.trim());
  }
  flush();
  return sections;
}

/* -------------------------------------------------------------------------
 * Dates and releases, from git
 * ---------------------------------------------------------------------- */

const STRICT = process.env.CI === "true" || process.env.CI === "1";

function git(args: string[]): string {
  return execFileSync("git", args, {
    cwd: REPO,
    encoding: "utf8",
    maxBuffer: 32 * 1024 * 1024,
    stdio: ["ignore", "pipe", "ignore"],
  });
}

let warned = false;

/**
 * CI is where a wrong page ships. A developer's build is a preview, and a
 * clone made by hand has history; the workflow's checkout is the one that can
 * be shallow, and under `actions/checkout`'s default depth of 1 git answers
 * happily and wrongly. So the build stops here rather than publishing every
 * entry under one undated heading, which is the failure lib/lastmod.ts had to
 * stop making about the sitemap for exactly the same reason.
 */
function unavailable(why: string): void {
  const guidance =
    `the changelog cannot date its entries (${why}).\n` +
    "Every entry would render under one undated heading. Set fetch-depth: 0 " +
    "on actions/checkout so the build can read real dates.";
  if (STRICT) throw new Error(guidance);
  if (!warned) {
    warned = true;
    console.warn(`[changelog] ${guidance}`);
  }
}

/** Distinguishes a `--format=%aI` line from a `--name-status` line. */
const ISO_LINE = /^\d{4}-\d{2}-\d{2}T/;

/**
 * When each fragment landed on main, walking first parents.
 *
 * "Landed on main" rather than "was written", and the difference is not
 * cosmetic: four of the 125 fragments have no adding commit findable with
 * `--diff-filter=A` at all, because they arrived through a merge and git
 * computes no diff for a merge unless it is asked to.
 * `--diff-merges=first-parent` asks, and it answers for every one of them.
 *
 * It is also the honest date for a changelog. A reader asking what changed
 * since they last looked is asking about the trunk, not about the day
 * somebody started writing on a branch.
 */
function landingDates(): Map<string, string> {
  const dates = new Map<string, string>();
  let out: string;
  try {
    if (git(["rev-parse", "--is-shallow-repository"]).trim() === "true") {
      unavailable(
        "this is a shallow checkout, so every file looks like it landed in the one commit that exists",
      );
      return dates;
    }
    out = git([
      "log",
      "--first-parent",
      "--diff-merges=first-parent",
      "--name-status",
      "--reverse",
      "--format=%aI",
      "--",
      ".changes",
    ]);
  } catch {
    unavailable("git could not be asked");
    return dates;
  }

  let when = "";
  for (const line of out.split("\n")) {
    if (ISO_LINE.test(line)) {
      when = line.trim();
      continue;
    }
    if (line.trim() === "") continue;
    const fields = line.split("\t");
    const status = fields[0];
    const file = fields[fields.length - 1];
    // A rename brings the new name in for the first time, so it dates the
    // entry under that name exactly as an add does.
    if (!status.startsWith("A") && !status.startsWith("R")) continue;
    if (!file.startsWith(".changes/") || dates.has(file)) continue;
    dates.set(file, when);
  }
  if (dates.size === 0) unavailable("no commit in this history touches .changes");
  return dates;
}

type Tag = { name: string; date: string; fragments: Set<string>; hasDir: boolean };

/** Every release tag, oldest first, with the fragments its tree carries. */
function tags(): Tag[] {
  let names: string[];
  try {
    names = git(["tag", "--list", "v*", "--sort=version:refname"])
      .split("\n")
      .map((n) => n.trim())
      .filter(Boolean);
  } catch {
    return [];
  }
  const out: Tag[] = [];
  for (const name of names) {
    try {
      const date = git(["log", "-1", "--format=%aI", name]).trim();
      const listed = git(["ls-tree", "-r", "--name-only", name, "--", ".changes"])
        .split("\n")
        .map((f) => f.trim())
        .filter(Boolean);
      out.push({ name, date, fragments: new Set(listed), hasDir: listed.length > 0 });
    } catch {
      // A tag that cannot be read is left out rather than guessed at.
    }
  }
  return out;
}

/* -------------------------------------------------------------------------
 * Assembly
 * ---------------------------------------------------------------------- */

/**
 * `.internal.md` marks a change that is real and that nobody outside this
 * repository could observe: a gitignore rule for a build artifact, a test that
 * had been summing over an empty table, a lockfile drifting between two
 * workspaces. They are kept because they are worth having in the repository
 * and they are never published, because a changelog full of them teaches a
 * reader that the changelog is not about them.
 */
function isPublic(file: string): boolean {
  return file.endsWith(".md") && !file.endsWith(".internal.md");
}

function readEntries(dates: Map<string, string>): Entry[] {
  const entries: Entry[] = [];
  for (const file of readdirSync(DIR).sort()) {
    if (!isPublic(file)) continue;
    const sections = parse(readFileSync(path.join(DIR, file), "utf8"));
    if (sections.length === 0) continue;
    entries.push({
      slug: file.replace(/\.md$/, ""),
      sections,
      landed: dates.get(`.changes/${file}`) ?? null,
    });
  }
  return entries;
}

function groupByDay(entries: Entry[]): Day[] {
  const byDay = new Map<string, Entry[]>();
  const undated: Entry[] = [];
  for (const entry of entries) {
    if (!entry.landed) {
      undated.push(entry);
      continue;
    }
    const key = entry.landed.slice(0, 10);
    const list = byDay.get(key);
    if (list) list.push(entry);
    else byDay.set(key, [entry]);
  }
  const days: Day[] = [...byDay.entries()]
    .sort((a, b) => (a[0] < b[0] ? 1 : -1))
    .map(([date, list]) => ({
      date,
      // Newest landing first within a day, then by slug, so two entries that
      // landed in the same commit hold a stable order across builds.
      entries: list.sort((a, b) =>
        a.landed === b.landed
          ? a.slug.localeCompare(b.slug)
          : (a.landed as string) < (b.landed as string)
            ? 1
            : -1,
      ),
    }));
  if (undated.length > 0) {
    days.push({ date: null, entries: undated.sort((a, b) => a.slug.localeCompare(b.slug)) });
  }
  return days;
}

/**
 * The releases, newest first, with the unreleased entries at the top.
 *
 * As of the first build of this page every one of the 125 fragments is
 * unreleased. `v0.1.0` and `v0.1.1` were both cut on 26 August 2026 and
 * neither tree contains a `.changes` directory at all, so the convention began
 * after both of them. That is why the two render as empty sections saying so,
 * rather than being filled in with work that plausibly shipped in them. An
 * accurate unreleased section beats a version history that is fiction, and
 * nothing here is allowed to guess which release an entry belongs to: an entry
 * is in a release when that release's own tree carries the file.
 */
export function releases(): Release[] {
  const dates = landingDates();
  const entries = readEntries(dates);

  const claimed = new Set<string>();
  const out: Release[] = [];

  for (const tag of tags()) {
    const mine = entries.filter(
      (entry) => !claimed.has(entry.slug) && tag.fragments.has(`.changes/${entry.slug}.md`),
    );
    for (const entry of mine) claimed.add(entry.slug);
    out.push({
      tag: tag.name,
      date: tag.date,
      days: groupByDay(mine),
      emptyBecause:
        mine.length > 0 ? null : tag.hasDir ? "every entry was internal" : "predates the convention",
    });
  }
  out.reverse();

  out.unshift({
    tag: null,
    date: null,
    days: groupByDay(entries.filter((entry) => !claimed.has(entry.slug))),
    emptyBecause: null,
  });
  return out;
}

/** How many public entries a release carries, for the count beside its name. */
export function entryCount(release: Release): number {
  return release.days.reduce((total, day) => total + day.entries.length, 0);
}

/**
 * A stable, readable date, fixed to UTC for the reason lib/blog.ts fixes its
 * own: `toLocaleDateString` with no timezone renders in the build machine's
 * zone on the server and in the reader's in the browser, and React then
 * reports that the two disagree.
 */
export function formatDay(date: string): string {
  return new Date(`${date}T00:00:00Z`).toLocaleDateString("en-GB", {
    year: "numeric",
    month: "long",
    day: "numeric",
    timeZone: "UTC",
  });
}

/** The most recent day anything landed, for the sitemap's lastmod. */
export function changelogModified(): Date | null {
  const all = [...landingDates().values()].sort();
  const newest = all[all.length - 1];
  return newest ? new Date(newest) : null;
}
