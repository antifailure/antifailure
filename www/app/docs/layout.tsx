import type { Metadata } from "next";
import { DocsShell } from "@/components/docs/DocsShell";
import { SiteLayout } from "@/components/layout/SiteLayout";

export const metadata: Metadata = {
  title: "Antifailure docs",
  description:
    "How Antifailure proves whether a deployment is safe before it ships — twin, safe state, firewall, and pass / warning / block.",
};

export default function DocsLayout({ children }: { children: React.ReactNode }) {
  return (
    <SiteLayout overlay={false}>
      <DocsShell>{children}</DocsShell>
    </SiteLayout>
  );
}
