"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { usePathname } from "next/navigation";
import {
  emptyWebsiteDocument, normalizeWebsiteDocument, projectWebsiteDocument, resolveCollection, resolveField, safeHref,
  stableStringify, validatePreviewMessage,
  type CollectionDefinition, type FieldDefinition, type FieldValue, type MediaReference,
  type PreviewChildMessage, type SectionDefinition, type WebsiteDocument, type WebsiteManifest,
} from "@antifailure/website";
import snapshot from "@/lib/cms-snapshot.generated.json";
import { CMS_FONTS, cmsStyles } from "@/lib/cms/styles";
import { controlPlaneUrl, websiteMediaUrl } from "@/lib/control-plane-routes";
import { parsePreviewConnection } from "@/lib/cms/preview-origin";
import { SitewideContentBridge } from "./SitewideContentBridge";

export const CMS_API_ORIGIN = new URL(controlPlaneUrl("website.published")).origin;
type Selection = { key?: string; sectionId?: string };
type Registry = { fields: Map<string, FieldDefinition>; sections: Map<string, SectionDefinition>; collections: Map<string, CollectionDefinition> };
interface CmsContextValue {
  document: WebsiteDocument;
  isPreview: boolean;
  editing: boolean;
  selection: Selection;
  register: (fields?: FieldDefinition[], sections?: SectionDefinition[], collections?: CollectionDefinition[]) => void;
  select: (selection: Selection) => void;
  edit: (key: string, value: FieldValue) => void;
  mediaUrl: (id: string) => string;
}
const fallback: CmsContextValue = {
  document: emptyWebsiteDocument(), isPreview: false, editing: false, selection: {},
  register: () => {}, select: () => {}, edit: () => {}, mediaUrl: websiteMediaUrl,
};
const CmsContext = createContext<CmsContextValue>(fallback);
export function useCms() { return useContext(CmsContext); }

function previewConnection(): { origin: string; session: string } | null {
  if (window.parent === window) return null;
  return parsePreviewConnection(window.location.search, CMS_API_ORIGIN, process.env.NODE_ENV !== "production");
}

export function CmsProvider({ children }: { children: ReactNode }) {
  const pathname = usePathname();
  const [framedPreview, setFramedPreview] = useState(false);
  const isPreview = pathname === "/cms-preview" || pathname === "/cms-preview/" || framedPreview;
  const [publishedDocument, setPublishedDocument] = useState<WebsiteDocument>(() => projectWebsiteDocument(normalizeWebsiteDocument(snapshot.document).document, pathname));
  const [previewDocument, setPreviewDocument] = useState<WebsiteDocument>(() => normalizeWebsiteDocument(snapshot.document).document);
  const [previewEditing, setEditing] = useState(false);
  const document = isPreview ? previewDocument : publishedDocument;
  const editing = isPreview && previewEditing;
  const [selection, setSelection] = useState<Selection>({});
  const [assetUrls, setAssetUrls] = useState<Record<string, string>>({});
  const [manifestVersion, setManifestVersion] = useState(0);
  const registry = useRef<Registry>({ fields: new Map(), sections: new Map(), collections: new Map() });
  const connection = useRef<ReturnType<typeof previewConnection>>(null);
  const scheduled = useRef(false);
  const revision = useRef(snapshot.revision);
  useEffect(() => { setFramedPreview(Boolean(previewConnection())); }, [pathname]);
  const manifestStructure = stableStringify({ sections: document.sections.custom, collections: document.collections });
  const register = useCallback((fields: FieldDefinition[] = [], sections: SectionDefinition[] = [], collections: CollectionDefinition[] = []) => {
    let changed = false;
    for (const [map, entries, key] of [[registry.current.fields, fields, "key"], [registry.current.sections, sections, "id"], [registry.current.collections, collections, "key"]] as const) {
      for (const entry of entries) {
        const id = (entry as unknown as Record<string, string>)[key];
        const target = map as Map<string, unknown>;
        if (stableStringify(target.get(id) ?? null) === stableStringify(entry)) continue;
        target.set(id, entry);
        changed = true;
      }
    }
    if (changed && !scheduled.current) {
      scheduled.current = true;
      queueMicrotask(() => { scheduled.current = false; setManifestVersion((version) => version + 1); });
    }
  }, []);
  const post = useCallback((message: Omit<PreviewChildMessage, "protocol" | "version" | "session">) => {
    const target = connection.current;
    if (target) window.parent.postMessage({ protocol: "antifailure-cms", version: 1, session: target.session, ...message }, target.origin);
  }, []);
  const select = useCallback((value: Selection) => {
    setSelection(value);
    post({ type: "select", payload: value });
  }, [post]);
  const edit = useCallback((key: string, value: FieldValue) => post({ type: "edit", payload: { key, value } }), [post]);
  const mediaUrl = useCallback((id: string) => {
    const publishedUrl = websiteMediaUrl(id);
    const capability = assetUrls[id];
    if (isPreview && capability) {
      try {
        const url = new URL(capability, CMS_API_ORIGIN);
        const expected = new URL(publishedUrl);
        if (url.origin === expected.origin && url.pathname === expected.pathname) return url.href;
      } catch { /* An invalid capability never becomes a network request. */ }
    }
    return publishedUrl;
  }, [assetUrls, isPreview]);

  useEffect(() => {
    if (isPreview) return;
    // A path change needs a fresh projection, but a response older than the
    // static HTML must never erase content that the build already published.
    revision.current = snapshot.revision;
    setPublishedDocument(projectWebsiteDocument(normalizeWebsiteDocument(snapshot.document).document, pathname));
    const controller = new AbortController();
    let fetching = false;
    const refresh = async () => {
      if (fetching || window.document.visibilityState === "hidden") return;
      fetching = true;
      try {
        const url = new URL(controlPlaneUrl("website.published"));
        url.searchParams.set("path", pathname);
        const response = await fetch(url, { credentials: "omit", signal: controller.signal, cache: "no-cache" });
        if (!response.ok) return;
        const current: unknown = await response.json();
        if (!current || typeof current !== "object" || !("revision" in current) || !("document" in current)) return;
        if (typeof current.revision !== "number" || !Number.isSafeInteger(current.revision) || current.revision <= revision.current) return;
        revision.current = current.revision;
        setPublishedDocument(normalizeWebsiteDocument(current.document).document);
      } catch { /* The rendered build remains available when the API is offline. */ }
      finally { fetching = false; }
    };
    void refresh();
    const timer = window.setInterval(() => { void refresh(); }, 60_000);
    window.addEventListener("focus", refresh);
    return () => { controller.abort(); window.clearInterval(timer); window.removeEventListener("focus", refresh); };
  }, [isPreview, pathname]);

  useEffect(() => {
    if (!isPreview) return;
    connection.current = previewConnection();
    const onMessage = (event: MessageEvent) => {
      const target = connection.current;
      if (!target || event.source !== window.parent || event.origin !== target.origin) return;
      const parsed = validatePreviewMessage(event.data, "parent-to-child");
      if (!parsed.ok || parsed.message.session !== target.session) return;
      const message = parsed.message;
      if (message.type === "init" || message.type === "update") {
        setPreviewDocument(message.payload.document);
        setAssetUrls(message.payload.assetUrls);
        setEditing(message.payload.mode === "edit");
      } else if (message.type === "select") {
        setSelection(message.payload);
        const selector = message.payload.key ? `[data-cms-key="${CSS.escape(message.payload.key)}"],[data-cms-field="${CSS.escape(message.payload.key)}"]` : `[data-cms-section="${CSS.escape(message.payload.sectionId ?? "")}"]`;
        window.document.querySelector(selector)?.scrollIntoView({ block: "center", behavior: "instant" });
      }
    };
    window.addEventListener("message", onMessage);
    // Effects in child components register the first manifest in this turn.
    setManifestVersion((version) => version + 1);
    return () => { window.removeEventListener("message", onMessage); connection.current = null; };
  }, [isPreview]);

  useEffect(() => {
    if (!isPreview || !connection.current) return;
    const activeCustom = new Set(document.sections.custom.map((section) => section.id));
    const activeSection = (id: string) => !id.startsWith("custom-") || activeCustom.has(id);
    const collections = [...registry.current.collections.values()].filter((collection) => activeSection(collection.sectionId));
    const byDepth = [...collections].sort((a, b) => b.key.length - a.key.length);
    const fields = [...registry.current.fields.values()].filter((field) => {
      if (!activeSection(field.sectionId)) return false;
      const collection = byDepth.find((candidate) => field.key.startsWith(`${candidate.key}.`));
      if (!collection) return true;
      const itemId = field.key.slice(collection.key.length + 1).split(".")[0];
      return collection.items.some((item) => item.id === itemId);
    });
    const builtinAssets: WebsiteManifest["builtinAssets"] = fields.flatMap((field) => {
      const value = field.defaultValue;
      return value && typeof value === "object" && value.type === "media" && value.source === "builtin" ? [{ id: field.key.replaceAll(".", "__"), label: field.label, src: value.src, kind: /\.(mp4|webm)(\?|$)/i.test(value.src) ? "video" as const : "image" as const }] : [];
    });
    const manifest: WebsiteManifest = { schemaVersion: 1, sourceVersion: process.env.NEXT_PUBLIC_SOURCE_VERSION || "source", fields, sections: [...registry.current.sections.values()].filter((section) => activeSection(section.id)), collections, fonts: CMS_FONTS, builtinAssets };
    post({ type: "ready", payload: { manifest } });
  }, [isPreview, manifestVersion, manifestStructure, post]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!isPreview || !editing) return;
    window.document.documentElement.dataset.cmsEditing = "true";
    const click = (event: MouseEvent) => {
      const target = event.target instanceof Element ? event.target : null;
      if (!target || target.closest("[contenteditable='true']")) return;
      const field = target.closest<HTMLElement>("[data-cms-key],[data-cms-field],[data-cms-action]");
      const section = target.closest<HTMLElement>("[data-cms-section]");
      const disclosure = target.closest("button[aria-expanded],summary,[data-cms-interact]");
      if (disclosure) { if (field) select({ key: field.dataset.cmsKey ?? field.dataset.cmsField ?? field.dataset.cmsAction, sectionId: section?.dataset.cmsSection }); return; }
      if (field || section) {
        event.preventDefault();
        event.stopPropagation();
        select({ ...(field ? { key: field.dataset.cmsKey ?? field.dataset.cmsField ?? field.dataset.cmsAction } : {}), ...(section ? { sectionId: section.dataset.cmsSection } : {}) });
      } else if (target.closest("a,button[type='submit']")) { event.preventDefault(); event.stopPropagation(); }
    };
    const submit = (event: Event) => { event.preventDefault(); event.stopPropagation(); };
    window.document.addEventListener("click", click, true);
    window.document.addEventListener("submit", submit, true);
    return () => { delete window.document.documentElement.dataset.cmsEditing; window.document.removeEventListener("click", click, true); window.document.removeEventListener("submit", submit, true); };
  }, [isPreview, editing, select]);

  useEffect(() => {
    window.document.querySelectorAll("[data-cms-selected]").forEach((element) => element.removeAttribute("data-cms-selected"));
    if (!isPreview || !editing) return;
    const selector = selection.key ? `[data-cms-key="${CSS.escape(selection.key)}"],[data-cms-field="${CSS.escape(selection.key)}"]` : selection.sectionId ? `[data-cms-section="${CSS.escape(selection.sectionId)}"]` : null;
    if (selector) window.document.querySelectorAll(selector).forEach((element) => element.setAttribute("data-cms-selected", "true"));
  }, [selection, editing, isPreview, document]);

  const value = useMemo(() => ({ document, isPreview, editing, selection, register, select, edit, mediaUrl }), [document, isPreview, editing, selection, register, select, edit, mediaUrl]);
  const content = <>
    <style>{cmsStyles(document, mediaUrl)}</style>
    {isPreview && <style>{`html[data-cms-editing] [data-cms-key],html[data-cms-editing] [data-cms-field]{cursor:text}html[data-cms-editing] [data-cms-key]:hover,html[data-cms-editing] [data-cms-field]:hover{outline:1px dashed #168555;outline-offset:3px}html[data-cms-editing] [data-cms-selected]{outline:2px solid #168555!important;outline-offset:4px}html[data-cms-editing] [data-cms-section]:hover{outline:1px dashed #16855566;outline-offset:-1px}[data-cms-placeholder]{padding:48px;border:1px dashed #87958b;background:#e4f1eb;color:#193e30;text-align:center;font:16px/1.5 Arial,sans-serif}`}</style>}
    {children}
  </>;
  return <CmsContext.Provider value={value}>{isPreview ? <div className="af-cms-preview" style={{ display: "contents" }}>{content}<SitewideContentBridge /></div> : <>{content}<SitewideContentBridge /></>}</CmsContext.Provider>;
}

export function useCmsField(definition: FieldDefinition): FieldValue {
  const cms = useCms();
  let canonical = definition;
  for (const [key, patch] of Object.entries(cms.document.collections)) {
    if (!definition.key.startsWith(`${key}.`)) continue;
    const remainder = definition.key.slice(key.length + 1);
    const separator = remainder.indexOf(".");
    const item = patch.custom.find((entry) => entry.id === remainder.slice(0, separator));
    const field = remainder.slice(separator + 1);
    if (item && Object.hasOwn(item.fields, field)) canonical = { ...definition, defaultValue: item.fields[field] };
  }
  const signature = stableStringify(canonical);
  useEffect(() => { cms.register([canonical]); }, [cms.register, signature]); // eslint-disable-line react-hooks/exhaustive-deps
  return resolveField(cms.document, canonical.key, canonical.defaultValue);
}
export function useCmsString(key: string, defaultValue: string, options: { label: string; sectionId: string; kind?: "text" | "url" | "richtext" }): string {
  const value = useCmsField({ key, defaultValue, kind: options.kind ?? "text", ...options });
  return typeof value === "string" && (options.kind !== "url" || safeHref(value)) ? value : defaultValue;
}
export function useCmsMedia(key: string, defaultValue: MediaReference | null, options: { label: string; sectionId: string }) {
  const cms = useCms();
  const value = useCmsField({ key, defaultValue, kind: "media", ...options });
  const media = value === null ? null : value && typeof value === "object" && value.type === "media" ? value : defaultValue;
  return { value: media, url: media ? media.source === "asset" ? cms.mediaUrl(media.assetId) : media.src : undefined, alt: media?.decorative ? "" : media?.alt ?? "" };
}

export interface CmsCollectionInput { key: string; label: string; sectionId: string; defaults: readonly { id: string; [key: string]: unknown }[] }
export function useCmsCollectionsBatch(definitions: readonly CmsCollectionInput[]): Record<string, { id: string; [key: string]: unknown }[]> {
  const cms = useCms();
  const signature = stableStringify({ definitions, custom: definitions.map((definition) => cms.document.collections[definition.key]?.custom ?? []) });
  useEffect(() => {
    const fields: FieldDefinition[] = [];
    const collections: CollectionDefinition[] = definitions.map((definition) => {
      const defaults: CmsCollectionInput["defaults"] = [...definition.defaults, ...(cms.document.collections[definition.key]?.custom ?? []).filter((item) => !definition.defaults.some((source) => source.id === item.id)).map((item) => ({ id: item.id, ...item.fields }))];
      const items = defaults.map((item) => {
        const values: Record<string, FieldValue> = {};
        for (const [key, value] of Object.entries(item)) {
          if (key === "id" || !["string", "number", "boolean"].includes(typeof value)) continue;
          values[key] = value as FieldValue;
          fields.push({ key: `${definition.key}.${item.id}.${key}`, label: `${String(item.title ?? item.text ?? item.label ?? item.id)}: ${key}`, kind: key.toLowerCase().includes("href") ? "url" : typeof value === "number" ? "number" : typeof value === "boolean" ? "boolean" : "text", sectionId: definition.sectionId, group: definition.label, defaultValue: value as FieldValue });
        }
        return { id: item.id, label: String(item.title ?? item.text ?? item.label ?? item.id), fields: values };
      });
      return { key: definition.key, label: definition.label, sectionId: definition.sectionId, items };
    });
    cms.register(fields, [], collections);
  }, [cms.register, signature]); // eslint-disable-line react-hooks/exhaustive-deps
  return Object.fromEntries(definitions.map((definition) => {
    const required = definition.defaults.length ? Object.keys(definition.defaults[0]).filter((key) => key !== "id" && definition.defaults.every((item) => typeof item[key] === "string")) : [];
    const items = resolveCollection(cms.document, definition.key, definition.defaults).flatMap((item) => {
      if (required.some((key) => typeof item[key] !== "string")) return [];
      const safe = { ...item };
      for (const [key, value] of Object.entries(safe)) {
        if (!key.toLowerCase().includes("href") || safeHref(value)) continue;
        const original = definition.defaults.find((entry) => entry.id === item.id)?.[key];
        if (!safeHref(original)) return [];
        safe[key] = original;
      }
      return [safe];
    });
    return [definition.key, items];
  }));
}
export function useCmsCollection<T extends { id: string }>(key: string, label: string, sectionId: string, defaults: readonly T[]): T[] {
  return useCmsCollectionsBatch([{ key, label, sectionId, defaults: defaults as unknown as CmsCollectionInput["defaults"] }])[key] as unknown as T[];
}
