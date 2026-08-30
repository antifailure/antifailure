import { Cta } from "@/components/home/Cta";
import { Features } from "@/components/home/Features";
import { Firewall } from "@/components/home/Firewall";
import { Hero } from "@/components/home/Hero";
import { Migrations } from "@/components/home/Migrations";
import { TocWrapper } from "@/components/home/Toc";
import { Trust } from "@/components/home/Trust";
import { Twins } from "@/components/home/Twins";
import { Workload } from "@/components/home/Workload";
import { SiteLayout } from "@/components/layout/SiteLayout";
import { pageMetadata } from "@/lib/seo";

// The root layout already carries site-wide defaults, but the home page still
// declares its own so that its canonical, its markdown alternate and its
// robots directives come from the same registry as every other route rather
// than being the one page that is special.
export const metadata = pageMetadata("/");

export default function Page() {
  return (
    <SiteLayout>
      <Hero />
      <TocWrapper>
        <Workload />
        <Migrations />
        <Twins />
        <Firewall />
        <Features />
      </TocWrapper>
      <Trust />
      <Cta />
    </SiteLayout>
  );
}
