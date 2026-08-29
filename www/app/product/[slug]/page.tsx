import type { ComponentType } from "react";
import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { pageMetadata } from "@/lib/seo";
import { ArchitecturePage } from "@/components/pages/product/Architecture";
import { ChangeIntelligencePage } from "@/components/pages/product/ChangeIntelligence";
import { ExploratoryUsersPage } from "@/components/pages/product/ExploratoryUsers";
import { FidelityPage } from "@/components/pages/product/Fidelity";
import { FirewallPage } from "@/components/pages/product/Firewall";
import { MigrationsPage } from "@/components/pages/product/Migrations";
import { OraclePage } from "@/components/pages/product/Oracle";
import { ReportPage } from "@/components/pages/product/Report";
import { SafeStatePage } from "@/components/pages/product/SafeState";
import { TwinsPage } from "@/components/pages/product/Twins";
import { WorkloadPage } from "@/components/pages/product/Workload";

const PAGES: Record<string, ComponentType> = {
  "twins": TwinsPage,
  "safe-state": SafeStatePage,
  "firewall": FirewallPage,
  "workload": WorkloadPage,
  "exploratory-users": ExploratoryUsersPage,
  "migrations": MigrationsPage,
  "report": ReportPage,
  "change-intelligence": ChangeIntelligencePage,
  "oracle": OraclePage,
  "fidelity": FidelityPage,
  "architecture": ArchitecturePage,
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
  // The title and description come from lib/routes.ts, which is also what the
  // sitemap and llms.txt read, so a product page cannot describe itself one way
  // to a browser and another way to a crawler.
  return pageMetadata(`/product/${slug}`);
}

export default async function Page({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  const page = PAGES[slug];
  if (!page) notFound();
  const View = page;
  return <View />;
}
