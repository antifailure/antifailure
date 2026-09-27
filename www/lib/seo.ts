import type { Metadata } from "next";
import { OG_IMAGE, SITE_NAME, SITE_URL, absoluteUrl } from "./site";
import { getRoute } from "./routes";
import { normalizeWebsiteDocument, resolveField, sitePageSlug } from "@antifailure/website";
import snapshot from "./cms-snapshot.generated.json";

const published = normalizeWebsiteDocument(snapshot.document).document;

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
  const slug = sitePageSlug(path);
  const titleValue = resolveField(published, `seo.${slug}.title`, route.title);
  const descriptionValue = resolveField(published, `seo.${slug}.description`, route.description);
  const title = typeof titleValue === "string" && titleValue.trim() ? titleValue : route.title;
  const description = typeof descriptionValue === "string" && descriptionValue.trim() ? descriptionValue : route.description;

  return {
    // `absolute` rather than a bare string, because the root layout defines a
    // title template and every title in the registry has already been through
    // it. A plain string gets the template applied a second time and ships with
    // the site name twice, which is what this did before anybody read the
    // built output.
    title: { absolute: title },
    description,
    alternates: {
      canonical: url,
      // The markdown twin of this page. Assistants and agent runtimes that
      // prefer source over rendered HTML can follow this instead of parsing a
      // 300KB document to recover 800 words.
      //
      // Only on indexable pages, because only indexable pages get a twin.
      // scripts/markdown-twins.mjs deliberately skips anything carrying
      // noindex, so /signin and /signup advertised a /signin.md and a
      // /signup.md that the build never wrote and the host answered 404 for.
      // Publishing the two missing files would silence the link checker and
      // would be the wrong fix: it would put a page we asked crawlers to
      // ignore back on the menu in a machine-readable form. The link is what
      // was untrue, so the link is what goes.
      ...(route.indexable
        ? {
            types: {
              // The root is the one page whose twin is not `${url}.md`. Its url
              // is the origin with no path (absoluteUrl("/") returns SITE_URL),
              // so `${url}.md` produced https://antifailure.dev.md, a host that
              // does not resolve, rather than the /index.md the build actually
              // writes. Every other page's twin sits at its own path plus .md.
              "text/markdown": url === SITE_URL ? `${SITE_URL}/index.md` : `${url}.md`,
            },
          }
        : {}),
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
      title,
      description,
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
      title,
      description,
      images: [OG_IMAGE.url],
    },
    ...overrides,
    ...(title !== route.title || description !== route.description ? {
      title: { absolute: title }, description,
      openGraph: { ...({ type: "website", siteName: SITE_NAME, locale: "en_US", url, images: [{ url: OG_IMAGE.url, width: OG_IMAGE.width, height: OG_IMAGE.height, alt: OG_IMAGE.alt }] } as const), ...overrides.openGraph, title, description },
      twitter: { ...({ card: "summary_large_image", images: [OG_IMAGE.url] } as const), ...overrides.twitter, title, description },
    } : {}),
    other: { ...overrides.other, "af-cms-source-title": route.title, "af-cms-source-description": route.description },
  };
}
