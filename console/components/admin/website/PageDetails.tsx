"use client";

import { authoredPageContent, emptyPageBody, pageContentKey, setFieldOverride, type AuthoredPage, type FieldDefinition, type WebsiteDocument } from "@antifailure/website";
import { inputClass } from "@/components/ui";
import { sourceArticleDefault } from "@/lib/website-page";
import { RichTextInput } from "./RichTextInput";
import type { PageOption } from "./PageLibrary";

export function PageDetails({ document, page, disabled, onChange, onRemove }: {
  document: WebsiteDocument; page: AuthoredPage; disabled: boolean;
  onChange(next: WebsiteDocument, key: string): void; onRemove(): void;
}) {
  const content = authoredPageContent(document, page);
  const update = (field: string, value: string | ReturnType<typeof emptyPageBody>) => {
    const key = pageContentKey(page.path, field);
    let next = setFieldOverride(document, key, value);
    if (field !== "updated") next = setFieldOverride(next, pageContentKey(page.path, "updated"), new Date().toISOString().slice(0, 10));
    onChange(next, key);
  };
  return <div className="cms-page-details">
    <div className="cms-page-details-top"><span>{page.kind === "post" ? "Article" : "Page"}</span><strong>{page.path}</strong></div>
    <label>Title<input className={inputClass} value={content.title} disabled={disabled} maxLength={180} onChange={(event) => update("title", event.target.value)} /></label>
    <label>Introduction<textarea className={inputClass} style={{ minHeight: 92, height: 92 }} value={content.description} disabled={disabled} rows={3} maxLength={300} onChange={(event) => update("description", event.target.value)} /></label>
    <label>Search and AI summary<textarea className={inputClass} style={{ minHeight: 72, height: 72 }} value={content.summary} disabled={disabled} rows={2} maxLength={300} onChange={(event) => update("summary", event.target.value)} /></label>
    {page.kind === "post" && <label>Topics, separated by commas<input className={inputClass} value={content.tags.join(", ")} disabled={disabled} maxLength={200} onChange={(event) => update("tags", event.target.value)} /></label>}
    <label>Publication date<input className={inputClass} type="date" value={content.published} disabled={disabled} onChange={(event) => update("published", event.target.value)} /></label>
    <RichTextInput value={content.body} onChange={(value) => update("body", value)} disabled={disabled} label={page.kind === "post" ? "Article body" : "Page body"} article />
    <p className="cms-help">Add image, video, split, or code sections from the page structure. The path stays stable after creation so existing links keep working.</p>
    <button type="button" className="cms-page-remove" disabled={disabled} onClick={onRemove}>Remove this page from the draft</button>
  </div>;
}

/** Source articles retain their authored layout while metadata and every body
 * paragraph can be edited through the same page preview. */
export function SourceArticleDetails({ document, path, definitions, source, disabled, onChange }: {
  document: WebsiteDocument; path: string; definitions: FieldDefinition[]; source?: PageOption; disabled: boolean;
  onChange(next: WebsiteDocument, key: string): void;
}) {
  const key = (field: string) => pageContentKey(path, field);
  const fallback = (field: string) => sourceArticleDefault(path, field, definitions, source);
  const value = (field: string) => typeof document.fields[key(field)] === "string" ? String(document.fields[key(field)]) : fallback(field);
  const update = (field: string, text: string) => onChange(setFieldOverride(document, key(field), text, fallback(field)), key(field));
  return <div className="cms-page-details">
    <div className="cms-page-details-top"><span>Published article</span><strong>{path}</strong></div>
    <label>Title<input className={inputClass} value={value("title")} disabled={disabled} maxLength={180} onChange={(event) => update("title", event.target.value)} /></label>
    <label>Introduction<textarea className={inputClass} style={{ minHeight: 92, height: 92 }} value={value("description")} disabled={disabled} maxLength={300} rows={3} onChange={(event) => update("description", event.target.value)} /></label>
    <label>Search and AI summary<textarea className={inputClass} style={{ minHeight: 72, height: 72 }} value={value("summary")} disabled={disabled} maxLength={300} rows={2} onChange={(event) => update("summary", event.target.value)} /></label>
    <label>Topics, separated by commas<input className={inputClass} value={value("tags")} disabled={disabled} maxLength={200} onChange={(event) => update("tags", event.target.value)} /></label>
    <label>Publication date<input className={inputClass} type="date" value={value("published").slice(0, 10)} disabled={disabled} onChange={(event) => update("published", event.target.value)} /></label>
    <p className="cms-help">Click any heading or paragraph in the article preview to edit its body. The article keeps its original figures, code and tables.</p>
  </div>;
}
