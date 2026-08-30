import { SubprocessorsPage } from "@/components/pages/company/Legal";
import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Subprocessors — Antifailure",
  description: "Everyone who receives data, everyone who deliberately does not, and how the list changes.",
};

export default function Page() {
  return <SubprocessorsPage />;
}
