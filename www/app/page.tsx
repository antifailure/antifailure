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
      {/* Isolated Twin comes before Load: the twin is what the traffic is sent
          at, so it has to be on the page before the section that sends it. */}
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
