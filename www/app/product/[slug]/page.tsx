import type { ComponentType } from "react";
import type { Metadata } from "next";
import { notFound } from "next/navigation";
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

const PAGES: Record<string, { title: string; description: string; Page: ComponentType }> = {
  twins: {
    title: "Isolated Twin — Antifailure",
    description: "A temporary copy of the application stack for every risky change.",
    Page: TwinsPage,
  },
  "safe-state": {
    title: "Safe State — Antifailure",
    description: "Sanitized, referentially consistent, production-shaped Postgres.",
    Page: SafeStatePage,
  },
  firewall: {
    title: "Side-Effect Firewall — Antifailure",
    description: "Fail-closed egress. Simulators instead of real-world side effects.",
    Page: FirewallPage,
  },
  workload: {
    title: "Workload Studio — Antifailure",
    description: "Observed patterns, deterministic scenarios, and exploratory users.",
    Page: WorkloadPage,
  },
  "exploratory-users": {
    title: "Exploratory users — Antifailure",
    description: "Exploratory AI users inside Workload Studio.",
    Page: ExploratoryUsersPage,
  },
  migrations: {
    title: "Migration Safety — Antifailure",
    description: "Locks, query plans, rollback feasibility on a production-shaped twin.",
    Page: MigrationsPage,
  },
  report: {
    title: "Safety Report — Antifailure",
    description: "Pass, warning, or block with evidence on the pull request.",
    Page: ReportPage,
  },
  "change-intelligence": {
    title: "Change Intelligence — Antifailure",
    description: "What to validate for this pull request, and at what fidelity.",
    Page: ChangeIntelligencePage,
  },
  oracle: {
    title: "Differential Oracle — Antifailure",
    description: "Baseline vs candidate against equivalent state and behavior.",
    Page: OraclePage,
  },
  fidelity: {
    title: "Fidelity Graph — Antifailure",
    description: "An explicit model of what the twin reproduced.",
    Page: FidelityPage,
  },
  architecture: {
    title: "Architecture — Antifailure",
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
  const page = PAGES[slug];
  if (!page) return { title: "Product — Antifailure" };
  return { title: page.title, description: page.description };
}

export default async function Page({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  const page = PAGES[slug];
  if (!page) notFound();
  const View = page.Page;
  return <View />;
}
