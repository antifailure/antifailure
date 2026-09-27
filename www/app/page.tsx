import { HomePage } from "@/components/cms/HomePage";
import { pageMetadata } from "@/lib/seo";

// The home page shipped every other page's canonical, OpenGraph and Twitter
// card through pageMetadata and its own through nothing: it exported no
// metadata at all, so it inherited only the root layout's site-wide canonical
// and, alone among the indexable pages, advertised no markdown twin. An agent
// that discovers /pricing.md from the pricing head found nothing pointing it at
// /index.md from the home head, though the build writes that file and the host
// answers 200 for it. Routing the root through the same registry the other
// pages use is what closes that, and getRoute("/") already carries its title
// and description.
export const metadata = pageMetadata("/");

export default function Page() {
  return <HomePage />;
}
