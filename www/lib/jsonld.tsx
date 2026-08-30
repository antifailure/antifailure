import {
  DOCS_URL,
  OG_IMAGE,
  REPO_URL,
  SAME_AS,
  SITE_CATEGORY,
  SITE_DESCRIPTION,
  SITE_DESCRIPTION_LONG,
  SITE_NAME,
  SITE_URL,
  absoluteUrl,
} from "./site";
import { breadcrumbTrail } from "./routes";

/**
 * Structured data, emitted as one linked graph rather than several loose
 * blocks.
 *
 * The distinction matters. Three separate <script> tags describing an
 * Organization, a WebSite and a WebPage are three unconnected assertions, and
 * a consumer has to guess that they are about the same thing. Giving each node
 * a stable `@id` and pointing the others at it says so outright, which is what
 * lets a knowledge graph resolve "Antifailure" to one entity instead of
 * several similarly named ones.
 *
 * All of this is rendered server side and appears in the HTML the crawler
 * receives. Structured data injected by client JavaScript is invisible to most
 * AI crawlers, which do not execute scripts.
 */

const ORG_ID = `${SITE_URL}/#organization`;
const SITE_ID = `${SITE_URL}/#website`;
const SOFTWARE_ID = `${SITE_URL}/#software`;

function JsonLd({ data }: { data: unknown }) {
  return (
    <script
      type="application/ld+json"
      // The payload is built from module constants in this repository, never
      // from user input, so there is nothing here to escape beyond closing a
      // script tag early.
      dangerouslySetInnerHTML={{
        __html: JSON.stringify(data).replace(/</g, "\\u003c"),
      }}
    />
  );
}

/**
 * The site-wide graph. Rendered once, in the root layout, on every page.
 */
export function SiteJsonLd() {
  const graph = [
    {
      "@type": "Organization",
      "@id": ORG_ID,
      name: SITE_NAME,
      url: SITE_URL,
      description: SITE_DESCRIPTION_LONG,
      logo: {
        "@type": "ImageObject",
        url: absoluteUrl("/icon.svg"),
        contentUrl: absoluteUrl("/icon.svg"),
      },
      // How a knowledge graph confirms that this site, the GitHub
      // organization, and anything else the project owns are one entity.
      sameAs: [...SAME_AS],
      knowsAbout: [
        "PostgreSQL schema migrations",
        "Database branching",
        "Data masking and anonymization",
        "Ephemeral preview environments",
        "Pre-production testing",
        "Release safety and deployment gates",
        "Egress control and network isolation",
        "Load testing with production-shaped traffic",
      ],
    },
    {
      "@type": "WebSite",
      "@id": SITE_ID,
      url: SITE_URL,
      name: SITE_NAME,
      description: SITE_DESCRIPTION,
      publisher: { "@id": ORG_ID },
      inLanguage: "en",
    },
    {
      // Both types, because it is a piece of software you install and it is
      // also a public source repository. Declaring only one loses half of it.
      "@type": ["SoftwareApplication", "SoftwareSourceCode"],
      "@id": SOFTWARE_ID,
      name: SITE_NAME,
      url: SITE_URL,
      description: SITE_DESCRIPTION_LONG,
      applicationCategory: "DeveloperApplication",
      applicationSubCategory: SITE_CATEGORY,
      operatingSystem: "Linux, macOS",
      codeRepository: REPO_URL,
      programmingLanguage: ["Go", "TypeScript", "SQL"],
      runtimePlatform: "Docker",
      softwareHelp: { "@type": "CreativeWork", url: DOCS_URL },
      author: { "@id": ORG_ID },
      publisher: { "@id": ORG_ID },
      image: absoluteUrl(OG_IMAGE.url),
      offers: {
        // The community tier is genuinely free to run. Saying so in structured
        // data is what makes a price appear in a result instead of nothing.
        "@type": "Offer",
        price: "0",
        priceCurrency: "USD",
        category: "Community",
        url: absoluteUrl("/pricing"),
      },
    },
  ];

  return <JsonLd data={{ "@context": "https://schema.org", "@graph": graph }} />;
}

/**
 * Per-page node plus its breadcrumb trail, linked back to the site graph.
 * Rendered by the shared page shell, so every route below the root gets one.
 */
export function PageJsonLd({ path }: { path: string }) {
  const trail = breadcrumbTrail(path);
  if (trail.length === 0) return null;

  const page = trail[trail.length - 1];
  const url = absoluteUrl(page.path);

  const graph: Record<string, unknown>[] = [
    {
      "@type": "WebPage",
      "@id": `${url}#webpage`,
      url,
      name: page.title,
      description: page.description,
      isPartOf: { "@id": SITE_ID },
      about: { "@id": SOFTWARE_ID },
      inLanguage: "en",
      primaryImageOfPage: absoluteUrl(OG_IMAGE.url),
    },
  ];

  // A trail of one is just the home page pointing at itself, which is noise.
  if (trail.length > 1) {
    graph.push({
      "@type": "BreadcrumbList",
      "@id": `${url}#breadcrumb`,
      itemListElement: trail.map((r, i) => ({
        "@type": "ListItem",
        position: i + 1,
        name: r.path === "/" ? "Home" : r.title.replace(/\s+—\s+Antifailure$/, ""),
        item: absoluteUrl(r.path),
      })),
    });
  }

  return <JsonLd data={{ "@context": "https://schema.org", "@graph": graph }} />;
}

export type FaqEntry = { question: string; answer: string };

/**
 * FAQPage markup. Each question and answer becomes an individually addressable
 * citation candidate, which is the highest-value structured data type for
 * getting quoted by an answer engine.
 *
 * The pairs passed in must be the same text the page actually renders. Marking
 * up an answer a reader cannot see is cloaking, and it gets the markup ignored.
 */
export function FaqJsonLd({ path, entries }: { path: string; entries: FaqEntry[] }) {
  if (entries.length === 0) return null;
  const url = absoluteUrl(path);
  return (
    <JsonLd
      data={{
        "@context": "https://schema.org",
        "@type": "FAQPage",
        "@id": `${url}#faq`,
        isPartOf: { "@id": SITE_ID },
        mainEntity: entries.map((e) => ({
          "@type": "Question",
          name: e.question,
          acceptedAnswer: { "@type": "Answer", text: e.answer },
        })),
      }}
    />
  );
}
