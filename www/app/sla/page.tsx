import { ServiceLevelsPage } from "@/components/pages/company/Legal";
import { pageMetadata } from "@/lib/seo";

// From lib/routes.ts, like every other page. Written out by hand these
// four had a title and a description and nothing else: no canonical, no
// OpenGraph, no Twitter card, no markdown alternate, and no place in the
// sitemap, because the registry is what the sitemap is built from.
export const metadata = pageMetadata("/sla");

export default function Page() {
  return <ServiceLevelsPage />;
}
