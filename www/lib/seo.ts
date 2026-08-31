import type { Metadata } from "next";
import { OG_IMAGE, SITE_NAME, SITE_URL, absoluteUrl } from "./site";
import { getRoute } from "./routes";

/**
 * Builds a page's metadata from its entry in the route registry.
 *
 * Every indexable page on this site was previously shipping a title and a
 * description and nothing else: no canonical, no OpenGraph, no Twitter card,
 * no robots directives. A link to antifailure.dev pasted into Slack, X or
 * LinkedIn rendered as a bare URL with no image and no title. This is the
 * function that fixes that, and routing it through the registry means a new
 * page cannot forget any of it.
 *
 * `max-image-preview: large` and `max-snippet: -1` are set deliberately. They
 * are opt-ins: without them an engine is entitled to show a thumbnail-sized
 * image and a truncated snippet, which is the difference between a rich result
 * and a line of grey text.
 */
export function pageMetadata(path: string, overrides: Metadata = {}): Metadata {
  const route = getRoute(path);
  if (!route) {
    // A page asking for metadata it never registered. Fail loudly at build
    // time rather than silently shipping a page with no canonical.
    throw new Error(
      `pageMetadata("${path}"): no such route in lib/routes.ts. Add it there so it reaches the sitemap and llms.txt too.`,
    );
  }

  const url = absoluteUrl(route.path);

  return {
    // `absolute` rather than a bare string, because the root layout defines a
    // title template and every title in the registry has already been through
    // it. A plain string gets the template applied a second time and ships with
    // the site name twice, which is what this did before anybody read the
    // built output.
    title: { absolute: route.title },
    description: route.description,
    alternates: {
      canonical: url,
      types: {
        // The markdown twin of this page. Assistants and agent runtimes that
        // prefer source over rendered HTML can follow this instead of parsing
        // a 300KB document to recover 800 words.
        "text/markdown": `${url === SITE_URL ? SITE_URL : url}.md`,
      },
    },
    robots: route.indexable
      ? {
          index: true,
          follow: true,
          googleBot: {
            index: true,
            follow: true,
            "max-image-preview": "large",
            "max-snippet": -1,
            "max-video-preview": -1,
          },
        }
      : { index: false, follow: true },
    openGraph: {
      type: "website",
      siteName: SITE_NAME,
      locale: "en_US",
      url,
      title: route.title,
      description: route.description,
      images: [
        {
          url: OG_IMAGE.url,
          width: OG_IMAGE.width,
          height: OG_IMAGE.height,
          alt: OG_IMAGE.alt,
        },
      ],
    },
    twitter: {
      card: "summary_large_image",
      title: route.title,
      description: route.description,
      images: [OG_IMAGE.url],
    },
    ...overrides,
  };
}
