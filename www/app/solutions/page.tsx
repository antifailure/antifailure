import { pageMetadata } from "@/lib/seo";
import { SolutionsHubPage } from "@/components/pages/solutions/Hub";

export const metadata = pageMetadata("/solutions");

export default function Page() {
  return <SolutionsHubPage />;
}
