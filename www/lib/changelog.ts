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

/**
 * The order the categories are read in, which is not the order they are
 * validated in. New capability, then behaviour that moved under you, then what
 * was broken, then what was insecure. It runs from the entry you read because
 * you are curious to the entry you read because you have to.
 */
export const CATEGORY_ORDER: Category[] = ["added", "changed", "fixed", "security"];

export type Span =
  | { kind: "text"; text: string }
  | { kind: "code"; text: string }
  | { kind: "strong"; spans: Span[] }
  | { kind: "em"; spans: Span[] }
  | { kind: "link"; text: string; href: string };

/** A span's words with its markup taken off, for measuring and for cutting. */
function plain(span: Span): string {
  return span.kind === "strong" || span.kind === "em"
    ? span.spans.map(plain).join("")
    : span.text;
}

export type Block = { kind: "p"; spans: Span[] } | { kind: "ul"; items: Span[][] };

/** One `# category` heading and the prose under it. */
export type Section = { category: Category; blocks: Block[] };

export type Entry = {
  /** The fragment's file name without its extension, and the anchor on the page. */
  slug: string;
  /**
   * The entry's opening sentence, lifted out of its first paragraph.
   *
   * This is the line somebody scans, and it is the author's own sentence
   * rather than a title derived from the file name. The file names are the
   * other candidate and they are not reliably readable: `loadcp`,
   * `installcheck` and `billing-stripe` say nothing, while their opening
   * sentences say "the control plane can take money". Measured over every
   * public fragment: all of them open with a paragraph, all of them have a
   * sentence boundary in it, the median lead is 90 characters and the longest
   * is 315, which the page clamps rather than cuts.
   *
   * It is removed from the body, so an open entry reads as a lede and the
   * paragraphs under it, with nothing said twice.
   */
  headline: Span[];
  /**
   * Every category this entry declares, in the order it declares them. Twelve
   * of the public fragments declare two. The first one files the entry under a
   * heading; all of them are shown on its row and all of them match a filter,
   * because an entry that both adds and fixes is not found under one word.
   */
  categories: Category[];
  sections: Section[];
  /**
   * ISO date of the commit that landed this entry on main, or null when git
   * could not say. Null is rendered as its own group rather than guessed at:
   * a date invented for a changelog is worse than an admitted gap, because
   * nothing downstream can tell it from a real one.
   */
  landed: string | null;
};

/** One category's entries within a release. Empty groups are not built. */
export type Group = { category: Category; entries: Entry[] };

export type Release = {
  /** A tag name, or null for the entries no tag contains yet. */
  tag: string | null;
  /** ISO date the tag was cut. Null for unreleased. */
  date: string | null;
  /**
   * Entries grouped by category, in CATEGORY_ORDER, newest first within a
   * group.
   *
   * This page grouped by day first, and the day is the right axis for a
   * project with a release every fortnight. It is the wrong one for a release
   * that carries 190 entries landed over one week: it produced seven headings
   * with twenty-seven entries under each, which is the same wall with dates
   * on it. The question a reader of a release actually asks is what kind of
   * change it is, and whether there is a security entry in it. Every row still
   * carries the day it landed, so the chronology is not lost, only demoted.
   */
  groups: Group[];
  /** The first and last day anything in this release landed. Null when nothing is dated. */
  span: { from: string; to: string } | null;
  /** How many entries this build could not date. Rendered, never hidden. */
  undated: number;
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

const INLINE = /(`[^`]+`|\*\*[^*]+\*\*|\*[^*\s][^*]*\*|\[[^\]]+\]\([^)]+\))/g;

/**
 * Inline markup, and only the shapes the fragments actually contain.
 *
 * Measured rather than assumed, across the 190 public fragments: 1349 spans of
 * inline code, 12 of bold, two of italic, one link, no fenced blocks, no
 * heading below the first level, and one file using bullets. Writing a general
 * markdown parser for that would be several hundred lines guarding against
 * constructs nothing has ever written.
 *
 * Bold and italic hold spans rather than text, because they are the two that
 * can contain something else and did. `**`af runner install`, the second
 * command the installer prints, could not succeed**` shipped its backticks to
 * the reader as backticks, and `*abandoned*` shipped its asterisks, on a page
 * a prospective customer reads. Both are two characters of raw markup on a
 * marketing site, which is the kind of defect nothing fails on and everybody
 * sees.
 *
 * The alternation is ordered, and that ordering is what keeps a lone asterisk
 * from eating a bold run: at the position of a `**` the bold branch is tried
 * first and wins. A `*` inside a code span is never reached at all, because
 * the backtick branch matches from the backtick, which comes first.
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
      out.push({ kind: "strong", spans: spans(token.slice(2, -2)) });
    } else if (token.startsWith("*")) {
      out.push({ kind: "em", spans: spans(token.slice(1, -1)) });
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
 * The opening sentence, and everything after it.
 *
 * The cut is made on the plain text the spans carry and then applied to the
 * spans themselves, so a headline never ends in the middle of a code span or
 * half a link. A boundary is a full stop, question mark or exclamation mark
 * followed by whitespace or by the end of the paragraph, which is what leaves
 * `Version 1.0.` and `Next 15.5.23` intact: the full stop inside a version
 * number is followed by a digit.
 *
 * A cut landing inside anything but a plain text span moves to that span's end
 * rather than splitting it. Nothing in the corpus does that today; the
 * alternative is a broken `<code>` shipped to a reader on the day one does.
 */
function splitLead(input: Span[]): { headline: Span[]; rest: Span[] } {
  const flat = input.map(plain).join("");
  const boundary = flat.match(/[.!?](?=\s|$)/);
  if (!boundary || boundary.index === undefined) return { headline: input, rest: [] };
  const cut = boundary.index + 1;

  const headline: Span[] = [];
  const rest: Span[] = [];
  let at = 0;
  for (const span of input) {
    const end = at + plain(span).length;
    if (end <= cut) {
      headline.push(span);
    } else if (at >= cut) {
      rest.push(span);
    } else if (span.kind === "text") {
      headline.push({ kind: "text", text: span.text.slice(0, cut - at) });
      rest.push({ kind: "text", text: span.text.slice(cut - at) });
    } else {
      headline.push(span);
    }
    at = end;
  }

  // The space that separated the two sentences belongs to neither.
  if (rest.length > 0 && rest[0].kind === "text") {
    const trimmed = rest[0].text.replace(/^\s+/, "");
    if (trimmed === "") rest.shift();
    else rest[0] = { kind: "text", text: trimmed };
  }
  return { headline, rest };
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

/**
 * The lede, taken off the front of the entry.
 *
 * Every public fragment opens its first section with a paragraph, so the
 * fallback below has never run. It is here because a fragment opening with a
 * bullet list is valid to `tools/changecheck` and would otherwise render a row
 * with no line on it at all, which is worse than a row headed by its own file
 * name.
 */
function lede(slug: string, sections: Section[]): Span[] {
  const first = sections[0]?.blocks[0];
  if (!first || first.kind !== "p") {
    const words = slug.replace(/-/g, " ");
    return [{ kind: "text", text: words.charAt(0).toUpperCase() + words.slice(1) }];
  }
  const { headline, rest } = splitLead(first.spans);
  if (rest.length > 0) first.spans = rest;
  else sections[0].blocks.shift();
  return headline;
}

function readEntries(dates: Map<string, string>): Entry[] {
  const entries: Entry[] = [];
  for (const file of readdirSync(DIR).sort()) {
    if (!isPublic(file)) continue;
    const sections = parse(readFileSync(path.join(DIR, file), "utf8"));
    if (sections.length === 0) continue;
    const slug = file.replace(/\.md$/, "");
    entries.push({
      slug,
      headline: lede(slug, sections),
      categories: [...new Set(sections.map((section) => section.category))],
      sections,
      landed: dates.get(`.changes/${file}`) ?? null,
    });
  }
  return entries;
}

/**
 * Newest landing first, then by slug.
 *
 * The slug tiebreak is what holds two entries that landed in the same commit
 * in the same order across builds. An entry this build could not date sorts
 * last rather than first: it is an admitted gap, not the newest news.
 */
function byRecency(a: Entry, b: Entry): number {
  if (a.landed === b.landed) return a.slug.localeCompare(b.slug);
  if (!a.landed) return 1;
  if (!b.landed) return -1;
  return a.landed < b.landed ? 1 : -1;
}

/**
 * An entry is filed under the first category it declares and shows all of
 * them, so it appears exactly once on the page and under one heading. Filing
 * it under both would give two elements one id, and an anchor pointing at a
 * duplicate is a link that lands in a different place depending on the
 * browser.
 */
function groupByCategory(entries: Entry[]): Group[] {
  const groups: Group[] = [];
  for (const category of CATEGORY_ORDER) {
    const mine = entries.filter((entry) => entry.categories[0] === category).sort(byRecency);
    if (mine.length > 0) groups.push({ category, entries: mine });
  }
  return groups;
}

function landedSpan(entries: Entry[]): { from: string; to: string } | null {
  const days = entries
    .map((entry) => entry.landed)
    .filter((landed): landed is string => landed !== null)
    .map((landed) => landed.slice(0, 10))
    .sort();
  if (days.length === 0) return null;
  return { from: days[0], to: days[days.length - 1] };
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
      groups: groupByCategory(mine),
      span: landedSpan(mine),
      undated: mine.filter((entry) => !entry.landed).length,
      emptyBecause:
        mine.length > 0 ? null : tag.hasDir ? "every entry was internal" : "predates the convention",
    });
  }
  out.reverse();

  const unreleased = entries.filter((entry) => !claimed.has(entry.slug));
  out.unshift({
    tag: null,
    date: null,
    groups: groupByCategory(unreleased),
    span: landedSpan(unreleased),
    undated: unreleased.filter((entry) => !entry.landed).length,
    emptyBecause: null,
  });
  return out;
}

/** How many public entries a release carries, for the count beside its name. */
export function entryCount(release: Release): number {
  return release.groups.reduce((total, group) => total + group.entries.length, 0);
}

/**
 * The id a category heading answers to, as `v1-0-0-security`.
 *
 * A tag carries dots, which are legal in an id and are an attribute selector
 * away from being a class in every stylesheet and a mistake in every
 * `querySelector` that forgets to escape them. The page reaches these from
 * JavaScript, so they are spelled without.
 */
export function groupAnchor(release: Release, category: Category): string {
  return `${(release.tag ?? "unreleased").replace(/[^a-zA-Z0-9]+/g, "-")}-${category}`;
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

/**
 * The same day, short, for the column beside an entry.
 *
 * "2 Sep 2026" rather than "2 September 2026", because the long form is 16
 * monospace capitals with 0.12em between them, which is 140px in a 132px
 * column. The `datetime` attribute beside it carries the unabbreviated date
 * for anything reading the page rather than looking at it.
 */
export function formatShortDay(date: string): string {
  return new Date(`${date}T00:00:00Z`).toLocaleDateString("en-GB", {
    year: "numeric",
    month: "short",
    day: "numeric",
    timeZone: "UTC",
  });
}

/**
 * The days a release's work landed between, written the way a person says it.
 *
 * The repeated half of the two dates comes off: "26 to 29 August 2026" rather
 * than "26 August 2026 to 29 August 2026", which is the same fact with eleven
 * words in it. Both dates are still in the markup, on the entries themselves.
 */
export function formatSpan(from: string, to: string): string {
  if (from === to) return formatDay(from);
  const [fromYear, fromMonth] = from.split("-");
  const [toYear, toMonth] = to.split("-");
  const day = (date: string) => String(Number(date.slice(8, 10)));
  if (fromYear === toYear && fromMonth === toMonth) {
    return `${day(from)} to ${formatDay(to)}`;
  }
  if (fromYear === toYear) {
    const head = formatDay(from).replace(new RegExp(` ${fromYear}$`), "");
    return `${head} to ${formatDay(to)}`;
  }
  return `${formatDay(from)} to ${formatDay(to)}`;
}

/** The most recent day anything landed, for the sitemap's lastmod. */
export function changelogModified(): Date | null {
  const all = [...landingDates().values()].sort();
  const newest = all[all.length - 1];
  return newest ? new Date(newest) : null;
}
