import { CompanyPage } from "@/components/pages/company/Company";
import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "About — Antifailure",
  description: "Antifailure is an open-core pre-production deployment safety platform.",
};

export default function Page() {
  return <CompanyPage />;
}
