"use client";

import { pageContentKey, sitePageSlug } from "@antifailure/website";
import { useCmsString } from "./CmsProvider";

const formatted = (value: string) => new Date(value).toLocaleDateString("en-US", { year: "numeric", month: "long", day: "numeric", timeZone: "UTC" });

export function BlogMeta({ slug, published, updated, tags, summary }: { slug: string; published: string; updated?: string; tags: string[]; summary: string }) {
  const path = `/blog/${slug}`;
  const sectionId = `page-${sitePageSlug(path)}`;
  const date = useCmsString(pageContentKey(path, "published"), published, { label: "Publication date", sectionId });
  const changed = useCmsString(pageContentKey(path, "updated"), updated ?? "", { label: "Updated date", sectionId });
  const topics = useCmsString(pageContentKey(path, "tags"), tags.join(", "), { label: "Topics", sectionId });
  // The summary is machine-facing, so it has no visible DOM node to select.
  // Register its actual source default for the article inspector instead.
  useCmsString(pageContentKey(path, "summary"), summary, { label: "Search and AI summary", sectionId });
  return <div className="flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-[12px] uppercase tracking-[0.12em] text-gray-new-50">
    <time dateTime={date}>{formatted(date)}</time>
    {changed && <><span aria-hidden="true">·</span><span>Updated {formatted(changed)}</span></>}
    <span aria-hidden="true">·</span><span>{topics}</span>
  </div>;
}
