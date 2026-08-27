import { PrivacyPage } from "@/components/pages/company/Legal";
import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Privacy Notice — Antifailure",
  description: "Production data stays in the customer boundary.",
};

export default function Page() {
  return <PrivacyPage />;
}
