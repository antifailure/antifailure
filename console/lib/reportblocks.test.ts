import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { parseReport, segments, type Block } from "./reportblocks.ts";

// ---------------------------------------------------------------------------
// Inline
// ---------------------------------------------------------------------------

test("inline text with no shapes is one text segment", () => {
  assert.deepEqual(segments("just words"), [{ kind: "text", text: "just words" }]);
});

test("bold, inline code and a markdown link are each their own segment", () => {
  assert.deepEqual(segments("a **b** c `d` e [f](http://x)"), [
    { kind: "text", text: "a " },
    { kind: "bold", text: "b" },
    { kind: "text", text: " c " },
    { kind: "code", text: "d" },
    { kind: "text", text: " e " },
    { kind: "link", text: "f", href: "http://x" },
  ]);
});

test("an html <code> and <a href> are read as code and a link", () => {
  assert.deepEqual(segments("<code>x</code> and <a href=\"http://y\">Y</a>"), [
    { kind: "code", text: "x" },
    { kind: "text", text: " and " },
    { kind: "link", text: "Y", href: "http://y" },
  ]);
});

test("a bare url is a link to itself, and one inside a markdown link is not matched twice", () => {
  assert.deepEqual(segments("see http://z now"), [
    { kind: "text", text: "see " },
    { kind: "link", text: "http://z", href: "http://z" },
    { kind: "text", text: " now" },
  ]);
  assert.deepEqual(segments("[t](http://z)"), [{ kind: "link", text: "t", href: "http://z" }]);
});

// ---------------------------------------------------------------------------
// Blocks
// ---------------------------------------------------------------------------

function kinds(blocks: Block[]): string[] {
  return blocks.map((b) => b.kind);
}

test("headings carry their level and text", () => {
  assert.deepEqual(parseReport("### Big\n\n#### Small"), [
    { kind: "heading", level: 3, text: "Big" },
    { kind: "heading", level: 4, text: "Small" },
  ]);
});

test("a github table is a header, a rule and its rows, and the rule is not a row", () => {
  const [block] = parseReport("| A | B |\n| --- | --- |\n| 1 | 2 |\n| 3 | 4 |");
  assert.deepEqual(block, {
    kind: "table",
    header: ["A", "B"],
    rows: [
      ["1", "2"],
      ["3", "4"],
    ],
  });
});

test("a pipe line without a dashes rule under it is a paragraph, not a table", () => {
  assert.deepEqual(kinds(parseReport("| not | a table |")), ["paragraph"]);
});

test("a details block keeps its summary and its non-blank lines", () => {
  const md = "<details><summary>How to</summary>\n\nstep one\n\nstep two\n\n</details>";
  assert.deepEqual(parseReport(md), [
    { kind: "details", summary: "How to", lines: ["step one", "step two"] },
  ]);
});

test("a sub footer and a bullet list each parse to their own block", () => {
  assert.deepEqual(parseReport("- one\n- two\n\n<sub>signed</sub>"), [
    { kind: "list", items: ["one", "two"] },
    { kind: "sub", text: "signed" },
  ]);
});

test("consecutive plain lines join into one paragraph, and comments are dropped", () => {
  assert.deepEqual(parseReport("<!-- marker -->\nline one\nline two"), [
    { kind: "paragraph", text: "line one line two" },
  ]);
});

// ---------------------------------------------------------------------------
// Wiring
// ---------------------------------------------------------------------------

// A parser with no caller is a parser that renders nothing, and every test
// above it passes just as happily with the page rendering none of it. This is a
// source read and it says so: the report only reaches a person if the run page
// mounts the report component and the component the parser feeds.
test("the runs page mounts the report and the report reads the pull request", () => {
  const page = readFileSync(new URL("../app/(app)/runs/page.tsx", import.meta.url), "utf8");
  assert.ok(page.includes("<ReportMarkdown"), "runs/page.tsx never mounts the report renderer");
  assert.ok(page.includes("<PullRequestReport"), "runs/page.tsx never mounts PullRequestReport");
  // A regex rather than a substring, and not for whitespace tolerance alone:
  // routecheck reads every .ts and .tsx file under console for call sites, and
  // a literal `query("runs.report"` written here is read as a call site whose
  // path it cannot resolve, which fails that gate over a source read. Escaping
  // the parenthesis keeps the assertion and leaves nothing for it to mistake.
  assert.match(page, /query\(\s*"runs\.report"/, "PullRequestReport never calls runs.report");
  const view = readFileSync(new URL("./reportmarkdown.tsx", import.meta.url), "utf8");
  assert.ok(view.includes("parseReport("), "the renderer never calls the parser this file tests");
});
