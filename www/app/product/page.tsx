import { OverviewPage } from "@/components/pages/product/Overview";
import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Product — Antifailure",
  description:
    "A disposable production twin that proves whether a deployment is safe before it ships.",
};

export default function Page() {
  return <OverviewPage />;
}
