import { AboutPage } from "@/components/pages/company/About";
import { pageMetadata } from "@/lib/seo";

export const metadata = pageMetadata("/about");

export default function Page() {
  return <AboutPage />;
}
