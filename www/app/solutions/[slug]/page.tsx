import { SOLUTION_PAGE_SLUGS, SolutionVerticalPage } from "@/components/pages/solutions/Vertical";
import type { Metadata } from "next";
import { pageMetadata } from "@/lib/seo";
import { notFound } from "next/navigation";

const META: Record<string, { title: string; description: string }> = {
  saas: { title: "B2B SaaS — Antifailure", description: "Daily deploys, migration anxiety, tenant-shaped twins." },
  fintech: { title: "Fintech — Antifailure", description: "Billing and ledger-safe production twins." },
  marketplaces: { title: "Marketplaces — Antifailure", description: "Queues, workers, dual-writes." },
  devtools: { title: "Developer tools — Antifailure", description: "Schema changes on large tables." },
};

export function generateStaticParams() {
  return SOLUTION_PAGE_SLUGS.map((slug) => ({ slug }));
}

export async function generateMetadata({
  params,
}: {
  params: Promise<{ slug: string }>;
}): Promise<Metadata> {
  const { slug } = await params;
  if (!META[slug]) return { title: "Solutions — Antifailure" };
  return pageMetadata(`/solutions/${slug}`);
}

export default async function Page({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  if (!META[slug]) notFound();
  return <SolutionVerticalPage slug={slug} />;
}
