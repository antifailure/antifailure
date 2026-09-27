import type { Metadata } from "next";
import { HomePage } from "@/components/cms/HomePage";

export const metadata: Metadata = {
  title: "Website preview | Antifailure",
  robots: { index: false, follow: false },
  alternates: { canonical: "https://antifailure.dev/" },
};

export default function Page() { return <HomePage />; }
