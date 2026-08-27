import { PricingPage } from "@/components/pages/company/Pricing";
import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Pricing — Antifailure",
  description: "Community, team, and enterprise pricing for pre-production deployment safety.",
};

export default function Page() {
  return <PricingPage />;
}
