import { SOLUTION_PAGE_SLUGS, SolutionVerticalPage } from "@/components/pages/solutions/Vertical";
import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { pageMetadata } from "@/lib/seo";

export function generateStaticParams() {
  return SOLUTION_PAGE_SLUGS.map((slug) => ({ slug }));
}

export async function generateMetadata({
  params,
}: {
  params: Promise<{ slug: string }>;
}): Promise<Metadata> {
  const { slug } = await params;
  return pageMetadata(`/solutions/${slug}`);
}

export default async function Page({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  // SOLUTION_PAGE_SLUGS is a readonly tuple of literals, so `includes` will not
  // accept a plain string. Widening for the membership test keeps the narrow
  // type useful everywhere else it is exported.
  if (!(SOLUTION_PAGE_SLUGS as readonly string[]).includes(slug)) notFound();
  return <SolutionVerticalPage slug={slug} />;
}
