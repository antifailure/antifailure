import type { APIRoute, GetStaticPaths } from "astro";
import { getCollection } from "astro:content";

/**
 * /docs/<any page>.md, the Markdown behind every rendered page.
 *
 * `/docs/llms-full.txt` already served the whole corpus and nothing on the site
 * linked to it, so the one thing this site could hand an assistant was built on
 * every deploy and reachable by nobody. That is the dead-capability shape: the
 * work exists, the last step to a caller does not.
 *
 * Whole-corpus and per-page are not the same job and one does not replace the
 * other. An agent being pointed at the product wants the corpus once. An agent
 * being asked about THIS page, which is the common case when somebody is
 * reading and stuck, wants one page and should not be handed 665 KB to answer
 * it. The address is the rendered one with `.md` on the end, so it is guessable
 * without being documented, which is the point: an agent that has the HTML URL
 * already has this one.
 *
 * The body is the source Markdown rather than the rendered HTML turned back
 * into text. It carries the frontmatter title and description, the code fences
 * unmangled, and the tables as tables, and it costs nothing to produce because
 * that is what the collection already holds. A conversion would introduce a
 * second description of the same page, which is the shape this repository keeps
 * finding defects in.
 */

export const getStaticPaths: GetStaticPaths = async () => {
  const docs = await getCollection("docs");
  return docs.map((doc) => ({ params: { slug: doc.id }, props: { doc } }));
};

export const GET: APIRoute = ({ props }) => {
  const doc = (props as { doc: { id: string; data: { title: string; description?: string }; body?: string } }).doc;
  const url = `https://antifailure.dev/docs/${doc.id}`.replace(/\/index$/, "");

  // A heading, the canonical address and the description, then the source. The
  // address is here because a model that reads this file and answers from it
  // should be able to cite where the answer came from, and a bare Markdown body
  // carries no clue which page it is.
  const out = [
    `# ${doc.data.title}`,
    "",
    `URL: ${url}`,
    ...(doc.data.description ? ["", doc.data.description] : []),
    "",
    "---",
    "",
    doc.body?.trim() ?? "",
    "",
  ];

  return new Response(out.join("\n"), {
    headers: {
      "content-type": "text/markdown; charset=utf-8",
      "cache-control": "public, max-age=3600",
    },
  });
};
