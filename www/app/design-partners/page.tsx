import { DesignPartnersPage } from "@/components/pages/company/DesignPartners";
import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Design partners — Antifailure",
  description: "One real risky migration, a complete wind tunnel, a useful decision.",
};

export default function Page() {
  return <DesignPartnersPage />;
}
