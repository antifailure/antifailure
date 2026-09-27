import type { Metadata } from "next";
import { HomePage } from "@/components/cms/HomePage";
import { getRoute } from "@/lib/routes";

const home = getRoute("/");

export const metadata: Metadata = {
  title: "Website preview | Antifailure",
  robots: { index: false, follow: false },
  alternates: { canonical: "https://antifailure.dev/" },
  other: home ? { "af-cms-source-title": home.title, "af-cms-source-description": home.description } : {},
};

export default function Page() { return <HomePage />; }
