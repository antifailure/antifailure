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

export default function Page() {
  return (
    <SiteLayout>
      <Hero />
      {/* Isolated Twin and Workload Studio trade places: the twin is what the
          workload runs against, so it has to be on the page before the studio
          that drives it. */}
      <TocWrapper>
        <Migrations />
        <Twins />
        <Features />
        <Workload />
        <Firewall />
      </TocWrapper>
      <Trust />
      <Cta />
    </SiteLayout>
  );
}
