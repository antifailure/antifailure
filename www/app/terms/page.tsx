import { TermsPage } from "@/components/pages/company/Legal";
import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Terms of Use — Antifailure",
  description: "A proving ground, not a guarantee. The promise is evidence, not zero-failure.",
};

export default function Page() {
  return <TermsPage />;
}
