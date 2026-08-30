import { pageMetadata } from "@/lib/seo";
import { PrivacyPage } from "@/components/pages/company/Legal";

export const metadata = pageMetadata("/privacy");

export default function Page() {
  return <PrivacyPage />;
}
