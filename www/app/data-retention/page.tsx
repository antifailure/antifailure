import { DataRetentionPage } from "@/components/pages/company/Legal";
import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Retention and deletion — Antifailure",
  description: "How long each thing is kept, how it goes away, and where the period is not exact.",
};

export default function Page() {
  return <DataRetentionPage />;
}
