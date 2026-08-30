import type { ReactNode } from "react";
import { DevtoolsPage } from "./devtools";
import { FintechPage } from "./fintech";
import { MarketplacesPage } from "./marketplaces";
import { SaasPage } from "./saas";

export const SOLUTION_PAGE_SLUGS = [
  "saas",
  "fintech",
  "marketplaces",
  "devtools",
] as const;

type Slug = (typeof SOLUTION_PAGE_SLUGS)[number];

const PAGES: Record<Slug, () => ReactNode> = {
  saas: SaasPage,
  fintech: FintechPage,
  marketplaces: MarketplacesPage,
  devtools: DevtoolsPage,
};

export function SolutionVerticalPage({ slug }: { slug: string }) {
  const Page = PAGES[slug as Slug];
  if (!Page) return null;
  return <Page />;
}
