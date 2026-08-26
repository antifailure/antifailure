import { ChromeProvider } from "@/components/Chrome";
import { DashboardHero } from "@/components/DashboardHero";
import { FeatureCards } from "@/components/FeatureCards";
import { FeatureShowcase } from "@/components/FeatureShowcase";
import { FirewallSection } from "@/components/FirewallSection";
import { FromPrIntro } from "@/components/FromPrIntro";
import { HeroSection } from "@/components/HeroSection";
import { IdeSection } from "@/components/IdeSection";
import { MigrationSection } from "@/components/MigrationSection";
import { ScaleFooter } from "@/components/ScaleFooter";
import { SiteHeader } from "@/components/SiteHeader";
import { TrustSplit } from "@/components/TrustSplit";
import { TwinsSection } from "@/components/TwinsSection";

export default function Page() {
  return (
    <ChromeProvider>
      <main className="bg-black">
        <SiteHeader />
        <HeroSection />
        <FeatureCards />
        <FeatureShowcase>
          <FromPrIntro />
          <IdeSection />
          <MigrationSection />
          <TwinsSection />
          <FirewallSection />
        </FeatureShowcase>
        <TrustSplit />
        <DashboardHero />
        <ScaleFooter />
      </main>
    </ChromeProvider>
  );
}
