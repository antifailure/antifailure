import { INDEXABLE_ROUTES, pageName } from "@/lib/routes";
import { SITE_DESCRIPTION_LONG, SITE_NAME, SITE_URL, absoluteUrl } from "@/lib/site";

/**
 * /llms-full.txt
 *
 * The companion to llms.txt: the same pages, but with their content inline so
 * an assistant can answer from one fetch instead of thirty-one.
 *
 * This is generated from the route registry and the page copy, which means it
 * cannot describe a page that does not exist and cannot go stale against a
 * page that changed. It is deliberately not a dump of rendered HTML: the site
 * ships around 300KB of markup per page, almost all of it layout, and feeding
 * that to a model wastes the context it would otherwise spend on the answer.
 */
export const dynamic = "force-static";

export function GET() {
  const out: string[] = [];

  out.push(`# ${SITE_NAME}: the full text of the site`);
  out.push("");
  out.push(SITE_DESCRIPTION_LONG);
  out.push("");
  out.push(
    `Source: ${SITE_URL}. Every section below is one page of the site. Headings are the`,
    "page titles, and the URL under each heading is the canonical address for that page.",
  );
  out.push("");
  out.push("---");
  out.push("");

  for (const route of INDEXABLE_ROUTES) {
    out.push(`## ${pageName(route)}`);
    out.push("");
    out.push(`URL: ${absoluteUrl(route.path)}`);
    out.push("");
    out.push(route.description);
    out.push("");
    out.push(route.summary);
    out.push("");
    out.push("---");
    out.push("");
  }

  return new Response(out.join("\n"), {
    headers: { "content-type": "text/plain; charset=utf-8" },
  });
}
