import type { APIRoute } from "astro";
import { getCollection } from "astro:content";

/**
 * /docs/llms-full.txt: the complete documentation as one plain-text file.
 *
 * This is the single most useful thing this site can hand an assistant, and it
 * is nearly free to produce: the docs are already markdown, so there is nothing
 * to convert. A model answering "how do I mask a Postgres branch with
 * Antifailure" can read one file instead of crawling every page of Starlight
 * chrome, and the answer it gives will be grounded in the current text rather
 * than in whatever it absorbed months ago.
 *
 * This comment said "41 pages" and there were 81 by the time anybody counted.
 * A number in prose that nothing computes is a number that drifts, and the
 * same 41 was being served to models in www/app/llms.txt/route.ts, where it
 * amounted to advice to crawl the site instead of taking the one file. The
 * count that ships is derived now, in www/lib/docs-facts.ts; this sentence
 * states none, because a page count in a comment about why the file exists
 * adds nothing a reader needs.
 *
 * The audience is not only web crawlers. Cursor, Claude Code, Copilot and MCP
 * servers all fetch files like this when a developer points them at a tool they
 * are trying to use, and that is the case that matters most for a CLI whose
 * users are holding an assistant while they work.
 *
 * Ordered by the sidebar so the file reads the way the documentation reads:
 * install it, learn the words, follow a guide, then look things up.
 */

const GROUP_ORDER = [
  "getting-started",
  "concepts",
  "guides",
  "providers",
  "reference",
  "security",
  "self-hosting",
  "enterprise",
  "contributing",
];

function groupOf(id: string): string {
  const [first] = id.split("/");
  return id.includes("/") ? first : "";
}

export const GET: APIRoute = async () => {
  const docs = await getCollection("docs");

  const sorted = [...docs].sort((a, b) => {
    const ga = GROUP_ORDER.indexOf(groupOf(a.id));
    const gb = GROUP_ORDER.indexOf(groupOf(b.id));
    // Ungrouped pages (the landing page) sort first.
    const ra = groupOf(a.id) === "" ? -1 : ga === -1 ? 99 : ga;
    const rb = groupOf(b.id) === "" ? -1 : gb === -1 ? 99 : gb;
    if (ra !== rb) return ra - rb;

    const oa = (a.data.sidebar?.order ?? 999) as number;
    const ob = (b.data.sidebar?.order ?? 999) as number;
    if (oa !== ob) return oa - ob;
    return a.id.localeCompare(b.id);
  });

  const out: string[] = [
    "# Antifailure documentation",
    "",
    "> A disposable copy of your production stack for every pull request: masked",
    "> Postgres, contained third-party APIs, and agents that use your app like people.",
    "",
    "This file is the complete documentation as plain text, generated at build time",
    "from the same markdown that renders at https://antifailure.dev/docs. Each section",
    "below is one page, and the URL under each heading is its canonical address.",
    "",
    "---",
    "",
  ];

  for (const doc of sorted) {
    const url = `https://antifailure.dev/docs/${doc.id}`.replace(/\/index$/, "");
    out.push(`## ${doc.data.title}`);
    out.push("");
    out.push(`URL: ${url}`);
    if (doc.data.description) {
      out.push("");
      out.push(doc.data.description);
    }
    out.push("");
    out.push(doc.body?.trim() ?? "");
    out.push("");
    out.push("---");
    out.push("");
  }

  return new Response(out.join("\n"), {
    headers: {
      "content-type": "text/plain; charset=utf-8",
      "cache-control": "public, max-age=3600",
    },
  });
};
