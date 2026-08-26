import type { Metadata } from "next";
import { ChromeProvider } from "@/components/Chrome";
import { DocsShell } from "@/components/docs/DocsShell";
import { ScaleFooter } from "@/components/ScaleFooter";
import { SiteHeader } from "@/components/SiteHeader";

export const metadata: Metadata = {
  title: "Antifailure docs",
  description:
    "How Antifailure proves whether a deployment is safe before it ships — twin, safe state, firewall, and pass / warning / block.",
};

export default function DocsLayout({ children }: { children: React.ReactNode }) {
  return (
    <ChromeProvider>
      <div className="min-h-screen bg-black">
        <SiteHeader />
        <DocsShell>{children}</DocsShell>
        <ScaleFooter />
      </div>
    </ChromeProvider>
  );
}
