import { pageMetadata } from "@/lib/seo";
import { CompanyPage } from "@/components/pages/company/Company";

export const metadata = pageMetadata("/company");

export default function Page() {
  return <CompanyPage />;
}
