import Link from "next/link";
import type { Metadata } from "next";
import { builtAuthoredPage, builtAuthoredPages } from "@/lib/authored-pages";
import { PageHero, PageSection, PageShell } from "@/components/pages/kit";
import { pageMetadata } from "@/lib/seo";

const authored = builtAuthoredPages().filter((page) => page.kind === "page").flatMap((page) => {
  const entry = builtAuthoredPage(page.path);
  return entry ? [entry] : [];
});

export const metadata: Metadata = authored.length ? pageMetadata("/pages") : {
  title: "Pages | Antifailure", robots: { index: false, follow: false },
};

export default function PagesIndex() {
  return <PageShell>
    <PageHero path={authored.length ? "/pages" : undefined} eyebrow="Pages" title="From the Antifailure team." lead="Guides and explanations from the people building Antifailure." actions={null} />
    <PageSection>{authored.length ? <ol className="border-t border-black/12">{authored.map(({ page, content }) => <li key={page.path} className="border-b border-black/12 py-8"><Link href={page.path} prefetch={false} className="group block"><h2 className="text-[clamp(1.5rem,3vw,2.25rem)] tracking-tight group-hover:underline">{content.title}</h2><p className="mt-3 max-w-[65ch] text-base leading-7 text-gray-new-40">{content.description}</p></Link></li>)}</ol> : <div className="border-t border-black/12 pt-8"><p className="max-w-[65ch] text-base leading-7 text-gray-new-40">More guides will appear here as they are published. For now, read the <Link href="/blog" className="text-black underline underline-offset-4">Writing</Link> or explore the <Link href="/docs" className="text-black underline underline-offset-4">documentation</Link>.</p></div>}</PageSection>
  </PageShell>;
}
