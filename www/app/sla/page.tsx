import { ServiceLevelsPage } from "@/components/pages/company/Legal";
import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Service levels — Antifailure",
  description: "There is no service level agreement. What is not committed, what holds anyway, and what would have to change.",
};

export default function Page() {
  return <ServiceLevelsPage />;
}
