import type { ComponentType } from "react";
import type { Metadata } from "next";
import { pageMetadata } from "@/lib/seo";
import { notFound } from "next/navigation";
import { ArchitecturePage } from "@/components/pages/product/Architecture";
import { FirewallPage } from "@/components/pages/product/Firewall";
import { LoadPage } from "@/components/pages/product/Load";
import { MigrationsPage } from "@/components/pages/product/Migrations";
import { ReportPage } from "@/components/pages/product/Report";
import { SafeStatePage } from "@/components/pages/product/SafeState";
import { TwinsPage } from "@/components/pages/product/Twins";
import { pageTitle } from "@/lib/site";

const PAGES: Record<string, { title: string; description: string; Page: ComponentType }> = {
  twins: {
    title: pageTitle("Isolated Twin"),
    description: "A temporary copy of the application stack for every risky change.",
    Page: TwinsPage,
  },
  "safe-state": {
    title: pageTitle("Safe State"),
    description: "Sanitized, referentially consistent, production-shaped Postgres.",
    Page: SafeStatePage,
  },
  firewall: {
    title: pageTitle("Side-Effect Firewall"),
    description: "Fail-closed egress. Simulators instead of real-world side effects.",
    Page: FirewallPage,
  },
  load: {
    title: pageTitle("Load"),
    description: "Traffic shaped like production's own access log, sent at the twin.",
    Page: LoadPage,
  },
  migrations: {
    title: pageTitle("Migration Safety"),
    description: "Locks, rewrites, and query plans on a branch with production's shape.",
    Page: MigrationsPage,
  },
  report: {
    title: pageTitle("Safety Report"),
    description: "Pass or fail with evidence on the pull request.",
    Page: ReportPage,
  },
  architecture: {
    title: pageTitle("Architecture"),
    description: "Trust boundary, environment lifecycle, isolation, and Postgres strategy.",
    Page: ArchitecturePage,
  },
};

export function generateStaticParams() {
  return Object.keys(PAGES).map((slug) => ({ slug }));
}

export async function generateMetadata({
  params,
}: {
  params: Promise<{ slug: string }>;
}): Promise<Metadata> {
  const { slug } = await params;
  if (!PAGES[slug]) return { title: pageTitle("Product") };
  // From the route registry rather than from PAGES, so that a product page
  // gets the same canonical, OpenGraph card and robots directives as every
  // other page. Reading the title from two places is how they drift.
  return pageMetadata(`/product/${slug}`);
}

export default async function Page({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  const page = PAGES[slug];
  if (!page) notFound();
  const View = page.Page;
  return <View />;
}
