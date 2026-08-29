import type { ReactNode } from "react";
import { DevtoolsPage } from "./devtools";
import { EcommercePage } from "./ecommerce";
import { FintechPage } from "./fintech";
import { MarketplacesPage } from "./marketplaces";
import { MigrationsPage } from "./migrations";
import { PlatformPage } from "./platform";
import { ReleaseGatesPage } from "./release-gates";
import { SaasPage } from "./saas";
import { WorkflowPage } from "./workflow";

export const SOLUTION_PAGE_SLUGS = [
  "saas",
  "fintech",
  "ecommerce",
  "marketplaces",
  "devtools",
  "platform",
  "migrations",
  "release-gates",
  "workflow",
] as const;

type Slug = (typeof SOLUTION_PAGE_SLUGS)[number];

const PAGES: Record<Slug, () => ReactNode> = {
  saas: SaasPage,
  fintech: FintechPage,
  ecommerce: EcommercePage,
  marketplaces: MarketplacesPage,
  devtools: DevtoolsPage,
  platform: PlatformPage,
  migrations: MigrationsPage,
  "release-gates": ReleaseGatesPage,
  workflow: WorkflowPage,
};

export function SolutionVerticalPage({ slug }: { slug: string }) {
  const Page = PAGES[slug as Slug];
  if (!Page) return null;
  return <Page />;
}
