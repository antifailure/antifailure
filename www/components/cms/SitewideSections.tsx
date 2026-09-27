"use client";

import { usePathname } from "next/navigation";
import { pageBlockPrefix, resolveOrder, sitePageSlug, type CustomSection as Section } from "@antifailure/website";
import { useCms } from "./CmsProvider";
import { CustomSection } from "./CustomSection";

/** Put page-specific blocks inside the real page main, before the shared footer.
 * The home page keeps its richer ordered section renderer. */
export function SitewideSections({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const cms = useCms();
  if (pathname === "/" || pathname === "/cms-preview") return <>{children}</>;
  const prefix = pageBlockPrefix(pathname);
  const pageId = `page-${sitePageSlug(pathname)}`;
  const sections = cms.document.sections.custom.filter((section) => section.group === "page" && section.id.startsWith(prefix));
  const ids = sections.map((section) => section.id);
  const moves = cms.document.sections.moves.filter((move) => ids.includes(move.id)).map((move) => move.after === pageId ? { ...move, after: null } : move);
  const order = resolveOrder(ids, moves, [], cms.document.sections.hidden);
  const byId = new Map<string, Section>(sections.map((section) => [section.id, section]));
  const hidden = cms.document.sections.hidden.includes(pageId);
  return <>
    <div data-cms-section={pageId} hidden={hidden} style={hidden ? undefined : { display: "contents" }}>{children}</div>
    {order.map((id) => <CustomSection key={id} section={byId.get(id)!} />)}
    {cms.isPreview && sections.filter((section) => cms.document.sections.hidden.includes(section.id)).map((section) => <div hidden key={section.id}><CustomSection section={section} /></div>)}
  </>;
}
