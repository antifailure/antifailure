import { ContactPage } from "@/components/pages/company/Contact";
import { pageMetadata } from "@/lib/seo";

export const metadata = pageMetadata("/contact");

export default function Page() {
  return <ContactPage />;
}
