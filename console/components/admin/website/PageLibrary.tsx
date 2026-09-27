"use client";

import { useEffect, useId, useState, type FormEvent } from "react";
import { pageContentKey, type WebsiteDocument } from "@antifailure/website";
import { createAuthoredPage } from "@/lib/website-page";
import { Drawer } from "@/components/admin/primitives";
import { inputClass } from "@/components/ui";

export type PageOption = { path: string; title: string; section: string; description?: string; published?: string; tags?: string[] };

const slug = (value: string) => value.toLowerCase().replace(/[^a-z0-9]+/gu, "-").replace(/^-+|-+$/gu, "").slice(0, 70);

export function PageLibrary({ document, options, active, disabled, catalogReady, startKind, onOpen, onCreate }: {
  document: WebsiteDocument; options: PageOption[]; active: string; disabled: boolean; catalogReady: boolean;
  startKind?: "page" | "post" | null;
  onOpen(path: string): void; onCreate(document: WebsiteDocument, path: string): void;
}) {
  const [kind, setKind] = useState<"page" | "post" | null>(null);
  const [title, setTitle] = useState("");
  const [path, setPath] = useState("");
  const [description, setDescription] = useState("");
  const [search, setSearch] = useState("");
  const [error, setError] = useState("");
  const formId = useId();
  useEffect(() => { if (startKind) setKind(startKind); }, [startKind]);
  const all = [...options, ...(document.pages ?? []).filter((page) => !options.some((option) => option.path === page.path)).map((page) => ({ path: page.path, title: String(document.fields[pageContentKey(page.path, "title")] ?? page.path), section: page.kind === "post" ? "Writing" : "New pages" }))];
  const shown = all.filter((page) => `${page.title} ${page.path} ${page.section}`.toLowerCase().includes(search.toLowerCase()));

  function create(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!kind || disabled || !catalogReady) return;
    let created;
    try { created = createAuthoredPage(document, all.map((page) => page.path), { kind, title, description, path, today: new Date().toISOString().slice(0, 10) }); }
    catch (cause) { setError(cause instanceof Error ? cause.message : "This page could not be created."); return; }
    onCreate(created.document, created.path);
    setKind(null); setTitle(""); setPath(""); setDescription(""); setError("");
  }

  return <div className="cms-page-library">
    <div className="cms-page-library-actions"><button disabled={disabled || !catalogReady} onClick={() => { setKind("page"); setError(""); }}>+ New page</button><button disabled={disabled || !catalogReady} onClick={() => { setKind("post"); setError(""); }}>+ New article</button></div>
    {!catalogReady && <p className="cms-help" role="status">Checking existing website paths before creating a page. Reload the editor if this does not finish.</p>}
    <label className="cms-page-library-search">Find a page<input type="search" className={inputClass} value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Title or path" /></label>
    <div className="cms-page-library-list">{shown.map((page) => <button key={page.path} aria-current={active === page.path ? "page" : undefined} onClick={() => onOpen(page.path)}><span>{page.title}</span><small>{page.section} · {page.path}</small></button>)}</div>
    {!shown.length && <p className="cms-help">No pages match that search.</p>}
    <Drawer open={Boolean(kind && catalogReady)} title={kind === "post" ? "Create an article" : "Create a page"} onClose={() => setKind(null)} actions={<button type="submit" form={formId} disabled={disabled || !catalogReady} className="cms-page-create-submit">Create draft page</button>}>
      {kind && <form id={formId} className="cms-page-create" onSubmit={create}>
        <p>Your new {kind === "post" ? "article" : "page"} stays private until you publish. Its URL is set when you create it.</p>
        <label>Title<input className={inputClass} value={title} onChange={(event) => { setTitle(event.target.value); if (!path) setError(""); }} maxLength={180} required autoFocus /></label>
        <label>Path<input className={inputClass} value={path} onChange={(event) => setPath(event.target.value)} placeholder={kind === "post" ? `/blog/${slug(title) || "article-name"}` : `/${slug(title) || "page-name"}`} spellCheck={false} /></label>
        <label>Introduction<textarea className={inputClass} style={{ minHeight: 116, height: 116 }} value={description} onChange={(event) => setDescription(event.target.value)} maxLength={300} rows={4} required /></label>
        <p>After creating the draft, add the body, images and sections in the editor.</p>
        {error && <p role="alert" className="cms-page-create-error">{error}</p>}
      </form>}
    </Drawer>
  </div>;
}
