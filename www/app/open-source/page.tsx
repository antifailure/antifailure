import { OpenSourcePage } from "@/components/pages/company/OpenSource";
import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Open source — Antifailure",
  description: "Customer agent, adapters, sanitization, egress, and cleanup — the planned open-source surface.",
};

export default function Page() {
  return <OpenSourcePage />;
}
