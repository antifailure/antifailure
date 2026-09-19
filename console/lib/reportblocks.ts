// Parsing the engine's report markdown, kept apart from the component that
// renders it so it can be tested without a DOM.
//
// It is not a general markdown engine. It parses exactly the shapes
// Run.Markdown in engine/internal/report/report.go emits, and anything it does
// not recognise falls through as a paragraph rather than as an error, because a
// report that renders most of itself beats a report that renders none of it.
// reportmarkdown.tsx maps the blocks this returns onto elements.

// ---------------------------------------------------------------------------
// Inline
// ---------------------------------------------------------------------------

export type Segment =
  | { kind: "text"; text: string }
  | { kind: "bold"; text: string }
  | { kind: "code"; text: string }
  | { kind: "link"; text: string; href: string };

// One regex, alternating over every inline shape, so a match is taken left to
// right and a URL inside a link's target is not also matched as a bare URL.
const INLINE =
  /(\*\*[^*]+\*\*)|(`[^`]+`)|(<code>[^<]*<\/code>)|(<a\s+href="[^"]+"[^>]*>[^<]*<\/a>)|(\[[^\]]+\]\([^)]+\))|(https?:\/\/[^\s)<]+)/g;

/** A run of text split into its inline shapes. A pure function of the string. */
export function segments(text: string): Segment[] {
  const out: Segment[] = [];
  let last = 0;
  for (const m of text.matchAll(INLINE)) {
    const at = m.index ?? 0;
    if (at > last) out.push({ kind: "text", text: text.slice(last, at) });
    const token = m[0];
    if (token.startsWith("**")) {
      out.push({ kind: "bold", text: token.slice(2, -2) });
    } else if (token.startsWith("`")) {
      out.push({ kind: "code", text: token.slice(1, -1) });
    } else if (token.startsWith("<code>")) {
      out.push({ kind: "code", text: token.slice(6, -7) });
    } else if (token.startsWith("<a")) {
      const href = /href="([^"]+)"/.exec(token)?.[1] ?? "#";
      const label = token.slice(token.indexOf(">") + 1, token.lastIndexOf("</a>"));
      out.push({ kind: "link", text: label, href });
    } else if (token.startsWith("[")) {
      const cut = token.indexOf("](");
      out.push({ kind: "link", text: token.slice(1, cut), href: token.slice(cut + 2, -1) });
    } else {
      out.push({ kind: "link", text: token, href: token });
    }
    last = at + token.length;
  }
  if (last < text.length) out.push({ kind: "text", text: text.slice(last) });
  return out;
}

// ---------------------------------------------------------------------------
// Blocks
// ---------------------------------------------------------------------------

export type Block =
  | { kind: "heading"; level: number; text: string }
  | { kind: "table"; header: string[]; rows: string[][] }
  | { kind: "details"; summary: string; lines: string[] }
  | { kind: "sub"; text: string }
  | { kind: "list"; items: string[] }
  | { kind: "paragraph"; text: string };

const isTableRow = (line: string) => line.trimStart().startsWith("|");
const isRule = (line: string) => /^\s*\|?[\s:|-]*-[\s:|-]*\|?\s*$/.test(line) && line.includes("-");
const isBullet = (line: string) => /^[-*]\s+/.test(line.trim());
const isComment = (line: string) => line.trim().startsWith("<!--") && line.trim().endsWith("-->");

function cells(line: string): string[] {
  return line
    .trim()
    .replace(/^\|/, "")
    .replace(/\|$/, "")
    .split("|")
    .map((c) => c.trim());
}

/**
 * The report markdown as a list of blocks.
 *
 * A line cursor rather than a split-and-map, because a table and a folded block
 * each span several lines and have to be consumed as one. Blank lines and HTML
 * comment markers are dropped. This is the whole grammar.
 */
export function parseReport(source: string): Block[] {
  const lines = source.replace(/\r\n/g, "\n").split("\n");
  const blocks: Block[] = [];
  let i = 0;

  while (i < lines.length) {
    const line = lines[i]!;
    const trimmed = line.trim();

    if (trimmed === "" || isComment(line)) {
      i++;
      continue;
    }

    const heading = /^(#{2,4})\s+(.*)$/.exec(trimmed);
    if (heading) {
      blocks.push({ kind: "heading", level: heading[1]!.length, text: heading[2]! });
      i++;
      continue;
    }

    // A GitHub table: a header row, a dashes rule, then body rows. Without the
    // rule on the second line it is not a table, it is a line that starts with
    // a pipe, and it falls through to a paragraph.
    if (isTableRow(line) && i + 1 < lines.length && isRule(lines[i + 1]!)) {
      const header = cells(line);
      const rows: string[][] = [];
      i += 2;
      while (i < lines.length && isTableRow(lines[i]!) && !isRule(lines[i]!)) {
        rows.push(cells(lines[i]!));
        i++;
      }
      blocks.push({ kind: "table", header, rows });
      continue;
    }

    const open = /^<details><summary>(.*?)<\/summary>\s*$/.exec(trimmed);
    if (open) {
      const inner: string[] = [];
      i++;
      while (i < lines.length && lines[i]!.trim() !== "</details>") {
        if (lines[i]!.trim() !== "") inner.push(lines[i]!.trim());
        i++;
      }
      i++; // consume </details>
      blocks.push({ kind: "details", summary: open[1]!, lines: inner });
      continue;
    }

    const sub = /^<sub>(.*)<\/sub>$/.exec(trimmed);
    if (sub) {
      blocks.push({ kind: "sub", text: sub[1]! });
      i++;
      continue;
    }

    if (isBullet(trimmed)) {
      const items: string[] = [];
      while (i < lines.length && isBullet(lines[i]!)) {
        items.push(lines[i]!.trim().replace(/^[-*]\s+/, ""));
        i++;
      }
      blocks.push({ kind: "list", items });
      continue;
    }

    // Anything else is a paragraph, and consecutive plain lines join into one.
    // It stops at the start of a real table (a header with a rule under it) but
    // consumes a lone pipe line, so a line that starts with a pipe and is not a
    // table advances the cursor rather than being refused by every branch at
    // once, which was an infinite loop.
    const para: string[] = [];
    while (
      i < lines.length &&
      lines[i]!.trim() !== "" &&
      !/^(#{2,4})\s/.test(lines[i]!.trim()) &&
      !(isTableRow(lines[i]!) && i + 1 < lines.length && isRule(lines[i + 1]!)) &&
      !/^<details>/.test(lines[i]!.trim()) &&
      !/^<sub>/.test(lines[i]!.trim()) &&
      !isBullet(lines[i]!) &&
      !isComment(lines[i]!)
    ) {
      para.push(lines[i]!.trim());
      i++;
    }
    blocks.push({ kind: "paragraph", text: para.join(" ") });
  }

  return blocks;
}
