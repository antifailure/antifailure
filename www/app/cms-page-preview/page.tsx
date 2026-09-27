import type { Metadata } from "next";
import { PageShell } from "@/components/pages/kit";
import { AuthoredPagePreview } from "@/components/cms/AuthoredPagePreview";

export const metadata: Metadata = { title: "Page preview | Antifailure", robots: { index: false, follow: false } };
export default function PreviewPage() { return <PageShell><AuthoredPagePreview /></PageShell>; }
