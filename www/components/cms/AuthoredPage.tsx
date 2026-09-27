"use client";

import { usePathname } from "next/navigation";
import { authoredPage, authoredPageContent, emptyPageBody, orderedPageBlockIds, pageContentKey, sitePageSlug, type AuthoredPage } from "@antifailure/website";
import { Container } from "@/components/layout/Container";
import { CmsSection, CmsText } from "./Editable";
import { CustomSection } from "./CustomSection";
import { useCms } from "./CmsProvider";

/** One renderer for a draft preview and the exported public route. */
export function AuthoredPageContent({ page }: { page: AuthoredPage }) {
  const cms = useCms();
  const pathname = usePathname();
  const current = authoredPage(cms.document, page.path) ?? page;
  const content = authoredPageContent(cms.document, current);
  const sectionId = `page-${sitePageSlug(page.path)}`;
  return <>
    <CmsSection sectionId={sectionId} label={page.kind === "post" ? "Article" : "Page"} group="page" as="header" className="safe-paddings pt-24 pb-8 max-md:pt-14 max-md:pb-7">
      <Container size="1600">
        {page.kind === "post" && <p className="font-mono text-xs uppercase tracking-[0.12em] text-gray-new-50">Writing{content.published ? ` · ${content.published}` : ""}</p>}
        <CmsText cmsKey={pageContentKey(page.path, "title")} label="Page title" sectionId={sectionId} defaultValue="" as="h1" className="mt-5 max-w-[1000px] text-[clamp(2rem,5vw,3.5rem)] leading-dense tracking-tighter" />
        <CmsText cmsKey={pageContentKey(page.path, "description")} label="Introduction" sectionId={sectionId} defaultValue="" as="p" className="mt-6 max-w-[680px] text-[18px] leading-relaxed text-gray-new-40" />
      </Container>
    </CmsSection>
    <div className="safe-paddings pb-20 max-md:pb-12"><Container size="1600"><CmsText cmsKey={pageContentKey(page.path, "body")} label="Page body" sectionId={sectionId} defaultValue={emptyPageBody()} as="div" className="prose-post cms-rich max-w-[720px]" /></Container></div>
    {pathname.replace(/\/+$/u, "") === "/cms-page-preview" && <PageBlocks path={page.path} />}
  </>;
}

/** The generic draft preview uses its queried page path. Public routes use
 * SitewideSections, which owns the same block ordering for every inner page. */
export function PageBlocks({ path }: { path: string }) {
  const cms = useCms();
  return <>{orderedPageBlockIds(cms.document, path).map((id) => {
    const section = cms.document.sections.custom.find((item) => item.id === id);
    return section ? <CustomSection key={id} section={section} /> : null;
  })}</>;
}
