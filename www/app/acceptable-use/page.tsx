import { AcceptableUsePage } from "@/components/pages/company/Legal";
import { pageMetadata } from "@/lib/seo";

export const metadata = pageMetadata("/acceptable-use");

export default function Page() {
  return <AcceptableUsePage />;
}
