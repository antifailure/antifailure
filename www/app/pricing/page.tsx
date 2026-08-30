import { pageMetadata } from "@/lib/seo";
import { PricingPage } from "@/components/pages/company/Pricing";

export const metadata = pageMetadata("/pricing");

export default function Page() {
  return <PricingPage />;
}
