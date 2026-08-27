import { SecurityPage } from "@/components/pages/company/Security";
import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Security — Antifailure",
  description: "Fail closed. Production data stays in the customer boundary.",
};

export default function Page() {
  return <SecurityPage />;
}
