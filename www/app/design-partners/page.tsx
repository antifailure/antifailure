import { pageMetadata } from "@/lib/seo";
import { DesignPartnersPage } from "@/components/pages/company/DesignPartners";

export const metadata = pageMetadata("/design-partners");

export default function Page() {
  return <DesignPartnersPage />;
}
