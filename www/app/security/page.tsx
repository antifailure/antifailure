import { pageMetadata } from "@/lib/seo";
import { SecurityPage } from "@/components/pages/company/Security";

export const metadata = pageMetadata("/security");

export default function Page() {
  return <SecurityPage />;
}
