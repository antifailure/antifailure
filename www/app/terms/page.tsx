import { pageMetadata } from "@/lib/seo";
import { TermsPage } from "@/components/pages/company/Legal";

export const metadata = pageMetadata("/terms");

export default function Page() {
  return <TermsPage />;
}
