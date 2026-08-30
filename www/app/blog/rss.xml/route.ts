import { POSTS_BY_DATE, postModified } from "@/lib/blog";
import { SITE_DESCRIPTION, SITE_NAME, absoluteUrl } from "@/lib/site";

/**
 * /blog/rss.xml
 *
 * A feed is unfashionable and cheap, and the things that still read one are
 * exactly the things worth reaching: aggregators, newsletter tooling, and the
 * crawlers that use a feed to learn a site has published something without
 * re-crawling it. It is also the only surface here that announces a change
 * rather than waiting to be asked about one.
 */
export const dynamic = "force-static";

/** Escapes the five characters XML cares about. */
function xml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&apos;");
}

export function GET() {
  const self = absoluteUrl("/blog/rss.xml");
  const latest = POSTS_BY_DATE[0];

  const items = POSTS_BY_DATE.map((post) => {
    const url = absoluteUrl(`/blog/${post.slug}`);
    return [
      "    <item>",
      `      <title>${xml(post.title)}</title>`,
      `      <link>${url}</link>`,
      // Permanent and unique. The URL works because these never move.
      `      <guid isPermaLink="true">${url}</guid>`,
      `      <pubDate>${new Date(post.published).toUTCString()}</pubDate>`,
      `      <description>${xml(post.dek)}</description>`,
      ...post.tags.map((t) => `      <category>${xml(t)}</category>`),
      "    </item>",
    ].join("\n");
  }).join("\n");

  const feed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom">
  <channel>
    <title>${xml(SITE_NAME)}</title>
    <link>${absoluteUrl("/blog")}</link>
    <description>${xml(SITE_DESCRIPTION)}</description>
    <language>en</language>
    <atom:link href="${self}" rel="self" type="application/rss+xml" />
    <lastBuildDate>${new Date(postModified(latest)).toUTCString()}</lastBuildDate>
    <docs>https://www.rssboard.org/rss-specification</docs>
${items}
  </channel>
</rss>
`;

  return new Response(feed, {
    headers: { "content-type": "application/rss+xml; charset=utf-8" },
  });
}
