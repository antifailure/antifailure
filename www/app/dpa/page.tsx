import { DpaPage } from "@/components/pages/company/Legal";
import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Data Processing Agreement — Antifailure",
  description: "A draft DPA written from the code: roles, security measures, and the measures that do not exist yet.",
};

export default function Page() {
  return <DpaPage />;
}
