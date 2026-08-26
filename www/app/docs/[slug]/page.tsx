import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { DOC_PAGES } from "@/components/docs/pages";
import { DOC_META, DOC_SLUGS, isDocSlug } from "@/lib/docs";

type Params = { slug: string };

export function generateStaticParams() {
  return DOC_SLUGS.map((slug) => ({ slug }));
}

export async function generateMetadata({
  params,
}: {
  params: Promise<Params>;
}): Promise<Metadata> {
  const { slug } = await params;
  const meta = DOC_META[slug];
  if (!meta) return { title: "Antifailure docs" };
  return {
    title: `${meta.title} — Antifailure docs`,
    description: meta.description,
  };
}

export default async function DocsSlugPage({ params }: { params: Promise<Params> }) {
  const { slug } = await params;
  if (!isDocSlug(slug)) notFound();
  const Page = DOC_PAGES[slug];
  return <Page />;
}
