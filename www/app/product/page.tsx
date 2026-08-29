import { pageMetadata } from "@/lib/seo";
import { OverviewPage } from "@/components/pages/product/Overview";

export const metadata = pageMetadata("/product");

export default function Page() {
  return <OverviewPage />;
}
