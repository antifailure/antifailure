import type { ReactNode } from "react";
import { MarketingPage } from "@/components/layout/MarketingPage";

export function InnerPage({
  eyebrow,
  title,
  lead,
  children,
}: {
  eyebrow: string;
  title: string;
  lead: string;
  children: ReactNode;
}) {
  return (
    <MarketingPage eyebrow={eyebrow} title={title} lead={lead}>
      {children}
    </MarketingPage>
  );
}
