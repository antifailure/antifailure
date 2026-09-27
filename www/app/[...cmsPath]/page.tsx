import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { builtAuthoredPage, builtAuthoredPages } from "@/lib/authored-pages";
import { PageShell } from "@/components/pages/kit";
import { AuthoredPageContent } from "@/components/cms/AuthoredPage";
import { PageJsonLd } from "@/lib/jsonld";
import { pageMetadata } from "@/lib/seo";

export function generateStaticParams() {
  const pages = builtAuthoredPages().filter((page) => page.kind === "page").map((page) => ({ cmsPath: page.path.slice(1).split("/") }));
  // Next's static exporter requires one parameter even for a fresh CMS. The
  // reserved resolver path renders the normal 404 and is never linked or listed.
  return pages.length ? pages : [{ cmsPath: ["cms-page-resolver"] }];
}

export async function generateMetadata({ params }: { params: Promise<{ cmsPath: string[] }> }): Promise<Metadata> {
  const path = `/${(await params).cmsPath.join("/")}`;
  const entry = builtAuthoredPage(path);
  if (!entry || entry.page.kind !== "page") return { robots: { index: false, follow: false } };
  return pageMetadata(path);
}

export default async function AuthoredRoute({ params }: { params: Promise<{ cmsPath: string[] }> }) {
  const path = `/${(await params).cmsPath.join("/")}`;
  const entry = builtAuthoredPage(path);
  if (!entry || entry.page.kind !== "page") notFound();
  return <PageShell><PageJsonLd path={path} /><AuthoredPageContent page={entry.page} /></PageShell>;
}
