import { pageMetadata } from "@/lib/seo";
import { PageJsonLd } from "@/lib/jsonld";
import { RequestDemo } from "@/components/pages/company/RequestDemo";

export const metadata = pageMetadata("/request-demo");

export default function RequestDemoPage() {
  return (
    <>
      {/* The structured data every other indexable page gets from PageShell.
          This route renders a full-bleed split instead of a PageShell, so it
          has no shell to inherit the WebPage node and breadcrumb trail from,
          the same as /signin and /signup did. The trail is not markup with
          nothing behind it: RequestDemo renders a Home link at the top left,
          which is the one visible step it describes. */}
      <PageJsonLd path="/request-demo" />
      {/* Rendered directly rather than through SiteLayout, so this supplies its
          own <main> landmark and the id the root layout's skip link points at,
          exactly as the sign-in screen does. */}
      <main id="main" tabIndex={-1}>
        <RequestDemo />
      </main>
    </>
  );
}
