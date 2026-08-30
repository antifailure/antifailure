import { pageMetadata } from "@/lib/seo";
import { OpenSourcePage } from "@/components/pages/company/OpenSource";

export const metadata = pageMetadata("/open-source");

export default function Page() {
  return <OpenSourcePage />;
}
