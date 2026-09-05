import { CareersPage } from "@/components/pages/company/Careers";
import { pageMetadata } from "@/lib/seo";

export const metadata = pageMetadata("/careers");

export default function Page() {
  return <CareersPage />;
}
