import { DeveloperPolicyPage } from "@/components/pages/company/Legal";
import { pageMetadata } from "@/lib/seo";

export const metadata = pageMetadata("/developer-policy");

export default function Page() {
  return <DeveloperPolicyPage />;
}
