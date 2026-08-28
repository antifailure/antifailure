import { SolutionsHubPage } from "@/components/pages/solutions/Hub";
import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Solutions — Antifailure",
  description: "Pre-production deployment safety for SaaS, fintech, commerce, marketplaces, and developer tools.",
};

export default function Page() {
  return <SolutionsHubPage />;
}
