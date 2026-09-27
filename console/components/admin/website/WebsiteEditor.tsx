"use client";

import { useCallback, useEffect, useId, useRef, useState, useSyncExternalStore } from "react";
import Link from "next/link";
import {
  referencedAssets, setFieldOverride, stableStringify, validatePreviewMessage, validateWebsiteDocument,
  type FieldDefinition, type FieldValue, type MediaReference, type WebsiteDocument,
  type WebsiteManifest, type CustomSectionKind, type CustomSectionGroup, type SectionDefinition,
} from "@antifailure/website";
import { adminMutate, operatorMay, useAdminContext } from "@/lib/admin";
import { query, useApi } from "@/lib/api";
import { createWebsiteAutosave, type WebsiteAutosave } from "@/lib/website-autosave";
import { addSection, duplicateSection, moveSection, pageSections, removeCustomSection, resetSection, SECTION_PRESETS, type WebsiteRevision, type WebsiteState } from "@/lib/website-client";
import { Drawer } from "@/components/admin/primitives";
import { Button, Confirm, inputClass, selectClass } from "@/components/ui";
import { LogoMark } from "@/components/icons";
import { DesignInspector } from "./DesignInspector";
import { RichTextInput } from "./RichTextInput";
import { AssetLibrary, type WebsiteAsset } from "./AssetLibrary";
import { CollectionEditor } from "./CollectionEditor";
import "./website.css";

type Device = "desktop" | "tablet" | "mobile";
type Panel = "sections" | "header" | "footer" | "styles";
type Selection = { key?: string; sectionId?: string };
type PublishAttempt = { operation: "publish" | "restore"; expectedRevision: number; requestId: string; revision?: number };
const DEVICE_WIDTH: Record<Device, number> = { desktop: 1440, tablet: 820, mobile: 390 };
const WEBSITE_ORIGIN = process.env.NEXT_PUBLIC_WEBSITE_ORIGIN ?? "https://antifailure.dev";
const API_ORIGIN = process.env.NEXT_PUBLIC_AF_API ?? "";
const messageOf = (error: unknown) => error instanceof Error ? error.message : "That change could not be completed. Please try again.";

export function WebsiteEditor() {
  const { me } = useAdminContext();
  if (!operatorMay(me, "admin.website.read")) return <div className="cms-access"><h1>Website editor</h1><p>Your role does not have access to website content.</p><Link href="/admin">Back to admin</Link></div>;
  return <LoadEditor />;
}

function LoadEditor() {
  const state = useApi(() => query<WebsiteState>("admin.administration.website.get"));
  if (state.status === "loading") return <div className="cms-loading" role="status"><LogoMark className="h-7 w-7" /><h1>Opening your website</h1><p>Loading the current draft and publishing history.</p><div className="cms-loading-layout" aria-hidden><span /><span /><span /></div></div>;
  if (state.status === "error") return <div className="cms-access"><h1>The website editor could not open</h1><p role="alert">{state.error.message}</p><Button onClick={state.reload}>Try again</Button><Link href="/admin">Back to admin</Link></div>;
  return <EditorWorkspace initial={state.data} />;
}

function EditorWorkspace({ initial }: { initial: WebsiteState }) {
  const { me } = useAdminContext();
  const canWrite = operatorMay(me, "admin.website.write");
  const canPublish = operatorMay(me, "admin.website.publish");
  const [server, setServer] = useState(initial);
  const [controller] = useState<WebsiteAutosave>(() => createWebsiteAutosave({
    initialDocument: initial.document, initialRevision: initial.draftRevision,
    save: async (input) => {
      const next = await adminMutate<WebsiteState>("admin.administration.website.saveDraft", input);
      setServer(next); return next;
    },
  }));
  const snapshot = useSyncExternalStore(controller.subscribe, controller.snapshot, controller.snapshot);
  const document = snapshot.document;
  const [manifest, setManifest] = useState<WebsiteManifest | null>(null);
  const [selection, setSelection] = useState<Selection>({ sectionId: "hero" });
  const [panel, setPanel] = useState<Panel>("sections");
  const [inspectorTab, setInspectorTab] = useState<"content" | "design">("content");
  const [contentSearch, setContentSearch] = useState("");
  const [codeLanguage, setCodeLanguage] = useState("html");
  const [device, setDevice] = useState<Device>("desktop");
  const [mode, setMode] = useState<"edit" | "preview">("edit");
  const [mobilePanel, setMobilePanel] = useState<"sections" | "inspector" | null>(null);
  const [session, setSession] = useState("");
  const [previewUrl, setPreviewUrl] = useState("");
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [ready, setReady] = useState(false);
  const [assetUrls, setAssetUrls] = useState<Record<string, string>>({});
  const [assetVersion, setAssetVersion] = useState(0);
  const [fontAssets, setFontAssets] = useState<Array<{ id: string; name: string }>>([]);
  const [assetPicker, setAssetPicker] = useState<{ key?: string; kind?: "image" | "video" | "font" } | null>(null);
  const [addBlock, setAddBlock] = useState<CustomSectionGroup | null>(null);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [history, setHistory] = useState<WebsiteRevision[]>([]);
  const [historyCursor, setHistoryCursor] = useState<number | null>(null);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [historyError, setHistoryError] = useState<string | null>(null);
  const [historical, setHistorical] = useState<{ revision: number; document: WebsiteDocument } | null>(null);
  const [restoreRevision, setRestoreRevision] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [pendingPublish, setPendingPublish] = useState<PublishAttempt | null>(null);
  const publishing = useRef(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [recovery, setRecovery] = useState<WebsiteDocument | null>(null);
  const [undo, setUndo] = useState<WebsiteDocument[]>([]);
  const [redo, setRedo] = useState<WebsiteDocument[]>([]);
  const lastEdit = useRef({ key: "", at: 0 });
  const iframe = useRef<HTMLIFrameElement>(null);
  const previewArea = useRef<HTMLDivElement>(null);
  const [previewSize, setPreviewSize] = useState({ width: 900, height: 720 });
  const dragged = useRef<{ id: string; group: CustomSectionGroup } | null>(null);
  const disposeTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const draftRef = useRef(document); draftRef.current = document;
  const manifestRef = useRef(manifest); manifestRef.current = manifest;
  const selectionRef = useRef(selection); selectionRef.current = selection;
  const previewState = useRef({ document, assetUrls, mode });
  previewState.current = { document: historical?.document ?? document, assetUrls, mode: historical ? "preview" : mode };
  const disabled = !canWrite || busy || Boolean(pendingPublish) || Boolean(historical) || snapshot.status === "conflict";
  const recoveryKey = `antifailure:website:draft:${me?.adminUserId}`;

  useEffect(() => {
    if (disposeTimer.current) clearTimeout(disposeTimer.current);
    return () => { disposeTimer.current = setTimeout(() => { void controller.flush().catch(() => undefined).finally(() => controller.dispose()); }, 0); };
  }, [controller]);

  useEffect(() => {
    const id = crypto.randomUUID();
    const url = new URL("/cms-preview", WEBSITE_ORIGIN);
    url.searchParams.set("parentOrigin", window.location.origin); url.searchParams.set("session", id);
    setSession(id); setPreviewUrl(url.toString());
    const suggestedDevice: Device = window.innerWidth < 768 ? "mobile" : window.innerWidth < 1024 ? "tablet" : "desktop";
    try {
      const heldDevice = sessionStorage.getItem("antifailure:website:preview-device");
      setDevice(heldDevice === "desktop" || heldDevice === "tablet" || heldDevice === "mobile" ? heldDevice : suggestedDevice);
      const held = sessionStorage.getItem(recoveryKey);
      if (held) { const parsed = validateWebsiteDocument(JSON.parse(held)); if (parsed.ok && stableStringify(parsed.document) !== stableStringify(initial.document)) setRecovery(parsed.document); }
    } catch { setDevice(suggestedDevice); }
  }, [initial.document, recoveryKey]);

  useEffect(() => {
    try {
      if (snapshot.hasUnsavedChanges) sessionStorage.setItem(recoveryKey, stableStringify(document));
      else if (!recovery) sessionStorage.removeItem(recoveryKey);
    } catch { /* The server remains the saved draft's authority. */ }
    const leave = (event: BeforeUnloadEvent) => { if (controller.snapshot().hasUnsavedChanges) { event.preventDefault(); event.returnValue = ""; } };
    window.addEventListener("beforeunload", leave);
    return () => window.removeEventListener("beforeunload", leave);
  }, [controller, document, recovery, recoveryKey, snapshot.hasUnsavedChanges]);

  useEffect(() => {
    const el = previewArea.current; if (!el) return;
    const measure = () => setPreviewSize({ width: Math.max(200, el.clientWidth - 32), height: Math.max(300, el.clientHeight - 32) });
    measure(); const observer = new ResizeObserver(measure); observer.observe(el); return () => observer.disconnect();
  }, []);

  const edit = useCallback((next: WebsiteDocument, key = "") => {
    if (!canWrite || controller.snapshot().suspended) return;
    const before = controller.snapshot().document;
    if (stableStringify(before) === stableStringify(next)) return;
    const now = Date.now();
    if (!key || lastEdit.current.key !== key || now - lastEdit.current.at > 900) setUndo((items) => [...items.slice(-79), before]);
    lastEdit.current = { key, at: now }; setRedo([]); setError(null); setNotice(null);
    try { controller.edit({ ...next, ...(manifestRef.current ? { sourceVersion: manifestRef.current.sourceVersion } : {}) }); }
    catch (cause) { setError(messageOf(cause)); }
  }, [canWrite, controller]);

  const send = useCallback((type: "init" | "update" | "select", payload: unknown) => {
    if (!session || !iframe.current?.contentWindow) return;
    iframe.current.contentWindow.postMessage({ protocol: "antifailure-cms", version: 1, session, type, payload }, new URL(WEBSITE_ORIGIN).origin);
  }, [session]);

  useEffect(() => {
    if (!session) return;
    const receive = (event: MessageEvent) => {
      if (event.origin !== new URL(WEBSITE_ORIGIN).origin || event.source !== iframe.current?.contentWindow) return;
      const checked = validatePreviewMessage(event.data, "child-to-parent");
      if (!checked.ok || checked.message.session !== session) return;
      const message = checked.message;
      if (message.type === "ready") {
        setManifest(message.payload.manifest); setReady(true); setPreviewError(null);
        send("init", previewState.current);
        send("select", selectionRef.current);
      } else if (message.type === "select") {
        setSelection(message.payload); setPanel("sections");
        if (window.matchMedia("(max-width: 1023px)").matches) setMobilePanel("inspector");
      }
      else if (message.type === "edit") {
        const field = manifestRef.current?.fields.find((item) => item.key === message.payload.key);
        if (field && canWrite && !busy && !historical) edit(setFieldOverride(draftRef.current, field.key, message.payload.value, field.defaultValue), field.key);
      } else if (message.type === "error") setPreviewError(message.payload.message);
    };
    window.addEventListener("message", receive); return () => window.removeEventListener("message", receive);
  }, [busy, canWrite, edit, historical, send, session]);

  useEffect(() => { if (ready) send("update", { document: historical?.document ?? document, assetUrls, mode: historical ? "preview" : mode }); }, [assetUrls, document, historical, mode, ready, send]);
  useEffect(() => { if (ready) send("select", selection); }, [ready, selection, send]);
  useEffect(() => { setContentSearch(""); }, [selection.key, selection.sectionId]);
  useEffect(() => {
    if (ready || !previewUrl) return;
    const timer = setTimeout(() => setPreviewError("The website preview has not connected. Check that the latest website is available, then reload the preview."), 20000);
    return () => clearTimeout(timer);
  }, [previewUrl, ready]);

  const previewAssetIds = referencedAssets(historical?.document ?? document).sort().join(",");
  useEffect(() => {
    let active = true;
    const ids = previewAssetIds ? previewAssetIds.split(",") : [];
    const load = () => {
      if (!ids.length) { setAssetUrls({}); return; }
      const chunks = Array.from({ length: Math.ceil(ids.length / 60) }, (_, index) => ids.slice(index * 60, index * 60 + 60));
      Promise.all(chunks.map((chunk) => query<{ urls: Record<string, string> }>("admin.administration.website.assetPreview", { ids: chunk }))).then((results) => {
        if (active) setAssetUrls(Object.fromEntries(results.flatMap((result) => Object.entries(result.urls).map(([id, url]) => [id, new URL(url, API_ORIGIN || window.location.origin).toString()]))));
      }).catch((cause) => { if (active) setError(messageOf(cause)); });
    };
    load(); const timer = setInterval(load, 120000); return () => { active = false; clearInterval(timer); };
  }, [previewAssetIds, assetVersion]);

  useEffect(() => {
    let active = true;
    async function loadFonts() {
      const fonts: Array<{ id: string; name: string }> = [];
      let cursor: { id: string; createdAt: string } | null = null;
      do {
        const result: { items: WebsiteAsset[]; nextCursor: { id: string; createdAt: string } | null } = await query("admin.administration.website.assets", { kind: "font", limit: 60, ...(cursor ? { cursor } : {}) });
        fonts.push(...result.items.map(({ id, name }) => ({ id, name })));
        cursor = result.nextCursor;
      } while (cursor && active);
      if (active) setFontAssets(fonts);
    }
    void loadFonts().catch((cause) => { if (active) setError(`Custom fonts could not load. ${messageOf(cause)}`); });
    return () => { active = false; };
  }, [assetVersion]);

  useEffect(() => {
    if (!server.refresh || ["deployed", "failed", "superseded"].includes(server.refresh.status)) return;
    let active = true;
    const timer = setInterval(() => { void query<WebsiteState>("admin.administration.website.get").then((next) => { if (active) setServer((held) => ({ ...held, publishedRevision: next.publishedRevision, refresh: next.refresh })); }).catch(() => undefined); }, 10000);
    return () => { active = false; clearInterval(timer); };
  }, [server.refresh]);

  function choose(selection: Selection) { setSelection(selection); setInspectorTab("content"); setMobilePanel(null); }
  function chooseDevice(next: Device) {
    setDevice(next);
    try { sessionStorage.setItem("antifailure:website:preview-device", next); } catch { /* Device selection still works without browser storage. */ }
  }
  function undoEdit() {
    const previous = undo.at(-1); if (!previous || disabled) return;
    setRedo((items) => [...items, document]); setUndo((items) => items.slice(0, -1)); controller.edit(previous); lastEdit.current.key = "";
  }
  function redoEdit() {
    const next = redo.at(-1); if (!next || disabled) return;
    setUndo((items) => [...items, document]); setRedo((items) => items.slice(0, -1)); controller.edit(next); lastEdit.current.key = "";
  }
  useEffect(() => {
    const shortcut = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement;
      if (target.closest("input,textarea,[contenteditable=true]")) return;
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "z") { event.preventDefault(); if (event.shiftKey) redoEdit(); else undoEdit(); }
    };
    window.addEventListener("keydown", shortcut); return () => window.removeEventListener("keydown", shortcut);
  });

  async function publish(revisionToRestore?: number) {
    if (publishing.current) return;
    publishing.current = true;
    setBusy(true); setError(null); controller.suspend();
    let retainAttempt = false;
    try {
      let attempt = pendingPublish;
      if (!attempt) {
        const saved = await controller.flush();
        attempt = { operation: revisionToRestore === undefined ? "publish" : "restore", expectedRevision: saved.revision, requestId: crypto.randomUUID(), ...(revisionToRestore === undefined ? {} : { revision: revisionToRestore }) };
        setPendingPublish(attempt);
      }
      const { operation, ...input } = attempt;
      let next: WebsiteState;
      try { next = await adminMutate<WebsiteState>(operation === "publish" ? "admin.administration.website.publish" : "admin.administration.website.restore", input); }
      catch (cause) {
        const status = cause && typeof cause === "object" && "status" in cause ? cause.status : undefined;
        // A lost response may follow a committed publish. Keep the exact
        // operation until acknowledged, and freeze edits so a retry cannot
        // silently replace changes made after that uncertain publication.
        retainAttempt = !(typeof status === "number" && status >= 400 && status < 500 && status !== 408 && status !== 429);
        throw cause;
      }
      controller.reload(next.document, next.draftRevision); setServer(next); setHistorical(null); setRestoreRevision(null);
      if (attempt.operation === "restore") { setUndo([]); setRedo([]); }
      setNotice(attempt.operation === "publish" ? "Published. Your changes are live; the static pages are refreshing." : "Version restored and published.");
    } catch (cause) { setError(messageOf(cause)); }
    finally { if (!retainAttempt) { setPendingPublish(null); controller.resume(); } publishing.current = false; setBusy(false); }
  }

  async function resolveConflict(keep: boolean) {
    setBusy(true); setError(null);
    try {
      const next = await query<WebsiteState>("admin.administration.website.get");
      if (keep) controller.reapply(next.document, next.draftRevision); else { controller.reload(next.document, next.draftRevision); setUndo([]); setRedo([]); }
      setServer(next);
    } catch (cause) { setError(messageOf(cause)); } finally { setBusy(false); }
  }
  async function loadHistory(more = false) {
    setHistoryLoading(true); setHistoryError(null);
    try {
      const result = await query<{ items: WebsiteRevision[]; nextCursor: number | null }>("admin.administration.website.history", { limit: 20, ...(more && historyCursor ? { cursor: historyCursor } : {}) });
      setHistory((held) => more ? [...held, ...result.items] : result.items); setHistoryCursor(result.nextCursor);
    } catch (cause) { setHistoryError(messageOf(cause)); } finally { setHistoryLoading(false); }
  }
  async function previewHistory(revision: number) {
    setHistoryLoading(true); setHistoryError(null);
    try { const result = await query<{ document: WebsiteDocument }>("admin.administration.website.revision", { revision }); setHistorical({ revision, document: result.document }); setHistoryOpen(false); }
    catch (cause) { setHistoryError(messageOf(cause)); } finally { setHistoryLoading(false); }
  }

  function add(kind: CustomSectionKind) {
    if (!addBlock || !manifest) return;
    const id = `custom-${crypto.randomUUID()}`;
    const rows = pageSections(document, manifest, addBlock);
    const after = rows.some((item) => item.id === selection.sectionId) ? selection.sectionId! : rows.at(-1)?.id ?? null;
    edit(addSection(document, kind, addBlock, after, id)); choose({ sectionId: id }); setAddBlock(null);
  }
  function toggleSection(id: string) { edit({ ...document, sections: { ...document.sections, hidden: document.sections.hidden.includes(id) ? document.sections.hidden.filter((item) => item !== id) : [...document.sections.hidden, id] } }); }
  function selectAsset(asset: WebsiteAsset) {
    if (assetPicker?.key) {
      const field = manifest?.fields.find((item) => item.key === assetPicker.key);
      edit(setFieldOverride(document, assetPicker.key, { type: "media", source: "asset", assetId: asset.id, kind: asset.kind === "video" ? "video" : "image", alt: "" }, field?.defaultValue));
    }
    setAssetVersion((value) => value + 1); setAssetPicker(null);
  }

  const selectedSection = manifest?.sections.find((item) => item.id === selection.sectionId) ?? document.sections.custom.find((item) => item.id === selection.sectionId);
  const selectedField = manifest?.fields.find((field) => field.key === selection.key);
  const target = panel === "styles" ? "global" : selection.key ?? selection.sectionId ?? "global";
  const fields = manifest?.fields.filter((field) => selection.key ? field.key === selection.key :
    field.sectionId === selection.sectionId && !manifest.collections.some((collection) => collection.sectionId === selection.sectionId && field.key.startsWith(`${collection.key}.`))) ?? [];
  const collectionDefinitions = manifest?.collections.filter((collection) => collection.sectionId === selection.sectionId) ?? [];
  const searchTerm = contentSearch.trim().toLocaleLowerCase();
  const visibleFields = fields.filter((field) => !searchTerm || `${field.label} ${field.help ?? ""}`.toLocaleLowerCase().includes(searchTerm));
  const visibleCollections = collectionDefinitions.filter((collection) => !searchTerm || `${collection.label} ${collection.items.map((item) => item.label).join(" ")}`.toLocaleLowerCase().includes(searchTerm));
  const codeFields = visibleFields.filter((field) => field.kind === "code");
  const activeCodeField = codeFields.find((field) => field.language === codeLanguage) ?? codeFields[0];
  const selectedCustomized = selection.key ? Object.hasOwn(document.fields, selection.key) || Object.hasOwn(document.styles, selection.key) : Boolean(
    selection.sectionId && (Object.hasOwn(document.styles, selection.sectionId) || document.sections.hidden.includes(selection.sectionId) ||
      manifest?.fields.some((field) => field.sectionId === selection.sectionId && (Object.hasOwn(document.fields, field.key) || Object.hasOwn(document.styles, field.key))) ||
      collectionDefinitions.some((collection) => Object.hasOwn(document.collections, collection.key)))
  );
  const title = panel === "styles" ? "Site styles" : selectedField?.label ?? (selectedSection && "label" in selectedSection ? selectedSection.label : selectedSection?.kind) ?? "Choose an element";
  const scale = Math.min(1, previewSize.width / DEVICE_WIDTH[device]);
  const width = DEVICE_WIDTH[device];
  const saveLabel = snapshot.status === "saving" ? "Saving…" : snapshot.status === "dirty" ? "Unsaved changes" : snapshot.status === "error" ? "Save failed" : snapshot.status === "conflict" ? "Draft changed elsewhere" : snapshot.hasUnsavedChanges ? "Unsaved changes" : "All changes saved";

  function sectionRows(group: CustomSectionGroup) {
    if (!manifest) return <p className="cms-help">Connecting to the website…</p>;
    const rows = pageSections(document, manifest, group);
    return <>
      {rows.map((section, index) => <div className={`cms-section-row ${selection.sectionId === section.id ? "is-selected" : ""}`} key={section.id}
        draggable={!disabled} onDragStart={() => { dragged.current = { id: section.id, group }; }} onDragEnd={() => { dragged.current = null; }}
        onDragOver={(event) => { if (dragged.current?.group === group) event.preventDefault(); }}
        onDrop={(event) => { event.preventDefault(); if (dragged.current?.group === group) edit(moveSection(document, group, rows.map((item) => item.id), dragged.current.id, index)); dragged.current = null; }}>
        <button className="cms-section-select" aria-pressed={selection.sectionId === section.id} onClick={() => choose({ sectionId: section.id })}><span className="cms-section-marker" aria-hidden>▤</span><span>{section.label}<small>{document.sections.hidden.includes(section.id) ? "Hidden" : section.id.startsWith("custom-") ? "Custom block" : Object.hasOwn(document.styles, section.id) || manifest.fields.some((field) => field.sectionId === section.id && (Object.hasOwn(document.fields, field.key) || Object.hasOwn(document.styles, field.key))) || manifest.collections.some((collection) => collection.sectionId === section.id && Object.hasOwn(document.collections, collection.key)) ? "Customized" : "Source defaults"}</small></span></button>
        {selection.sectionId === section.id && <div className="cms-section-actions">
          <button title="Move up" aria-label={`Move ${section.label} up`} disabled={disabled || index === 0} onClick={() => edit(moveSection(document, group, rows.map((item) => item.id), section.id, index - 1))}>↑</button>
          <button title="Move down" aria-label={`Move ${section.label} down`} disabled={disabled || index === rows.length - 1} onClick={() => edit(moveSection(document, group, rows.map((item) => item.id), section.id, index + 1))}>↓</button>
          <button disabled={disabled} onClick={() => toggleSection(section.id)}>{document.sections.hidden.includes(section.id) ? "Show" : "Hide"}</button>
          {section.id.startsWith("custom-") && <button disabled={disabled} onClick={() => { const id = `custom-${crypto.randomUUID()}`; edit(duplicateSection(document, section.id, id)); choose({ sectionId: id }); }}>Copy</button>}
        </div>}
      </div>)}
      <button className="cms-add-button" disabled={disabled || !ready} onClick={() => setAddBlock(group)}>+ Add section</button>
    </>;
  }

  const sidebar = <>
    <div className="cms-panel-heading"><h2>Website</h2><button className="cms-text-button" onClick={() => { setHistoryOpen(true); void loadHistory(); }}>History</button></div>
    <nav className="cms-panel-tabs" aria-label="Website settings">
      {(["sections", "header", "footer", "styles"] as Panel[]).map((item) => <button key={item} aria-pressed={panel === item} onClick={() => {
        setPanel(item); setInspectorTab(item === "styles" ? "design" : "content");
        if (item === "header" || item === "footer") choose({ sectionId: manifest?.sections.find((section) => section.group === item)?.id ?? item });
      }}>{item === "sections" ? "Page" : item[0].toUpperCase() + item.slice(1)}</button>)}
    </nav>
    {panel === "sections" ? <div className="cms-section-list"><h3>Above the fold</h3>{sectionRows("hero")}<h3>Homepage</h3>{sectionRows("page")}</div> :
      panel === "styles" ? <div className="cms-panel-description"><h3>Your brand, everywhere.</h3><p>Set the site’s typography and colors. Select any section or text to fine-tune it separately.</p><button className="cms-add-button" onClick={() => { setAssetPicker({ kind: "font" }); }}>Manage custom fonts</button><p className="cms-help">Unchanged settings follow the website’s current design. Each device keeps its own adjustments.</p></div> :
        <div className="cms-panel-description"><h3>Shared {panel}</h3><p>Changes appear across the website.</p>{manifest?.sections.filter((item) => item.group === panel).map((item) => <button className="cms-section-select" key={item.id} onClick={() => choose({ sectionId: item.id })}>{item.label}</button>)}</div>}
    <div className="cms-sidebar-bottom"><button className="cms-library-button" onClick={() => setAssetPicker({})}>Media library <span>↗</span></button><p>Drafts autosave. Publish when you’re ready.</p></div>
  </>;

  const inspector = <>
    <div className="cms-panel-heading"><div><h2>{title}</h2><p>{panel === "styles" ? "Across the website" : selectedCustomized ? "Customized" : "Follows source defaults"}</p></div></div>
    <div className="cms-inspector-tabs"><button aria-pressed={inspectorTab === "content"} onClick={() => setInspectorTab("content")} disabled={panel === "styles"}>Content</button><button aria-pressed={inspectorTab === "design"} onClick={() => setInspectorTab("design")}>Design</button></div>
    <fieldset disabled={disabled} className="cms-inspector-body">
      {manifest && (inspectorTab === "design" || panel === "styles") ? <DesignInspector document={document} target={target} device={device} manifest={manifest} onChange={(next) => edit(next, `style:${target}`)} disabled={disabled} fontAssets={fontAssets} /> : <>
        {selectedField && <button className="cms-text-button cms-back" onClick={() => choose({ sectionId: selectedField.sectionId })}>← All section content</button>}
        {!selection.key && (fields.length > 6 || collectionDefinitions.length > 0) && <input className={`${inputClass} cms-content-search`} type="search" aria-label="Find a setting" placeholder="Find a setting…" value={contentSearch} onChange={(event) => setContentSearch(event.target.value)} />}
        {!fields.length && <p className="cms-help">Click text, media or a section in the preview to start editing.</p>}
        {searchTerm && !visibleFields.length && !visibleCollections.length && <p className="cms-help">No matching settings. Try a different word.</p>}
        {codeFields.length > 0 && <div className="cms-code-intro"><div className="cms-code-tabs" role="group" aria-label="Code language">{codeFields.map((field) => <button key={field.key} aria-pressed={field.key === activeCodeField?.key} onClick={() => setCodeLanguage(field.language ?? "html")}>{field.language === "javascript" ? "JavaScript" : (field.language ?? field.label).toUpperCase()}</button>)}</div><p className="cms-help">Code runs in its own frame. The preview updates as you edit.</p></div>}
        {visibleFields.filter((field) => field.kind !== "code" || field.key === activeCodeField?.key).map((field) => <FieldEditor key={field.key} field={field} value={Object.hasOwn(document.fields, field.key) ? document.fields[field.key] : field.defaultValue} customized={Object.hasOwn(document.fields, field.key)}
          websiteOrigin={WEBSITE_ORIGIN} assetUrls={assetUrls} disabled={disabled}
          onChange={(value) => { try { edit(setFieldOverride(document, field.key, value, field.kind === "media" && value === null ? undefined : field.defaultValue), field.key); } catch (cause) { setError(messageOf(cause)); } }}
          onReset={() => edit(setFieldOverride(document, field.key, undefined))}
          onMedia={() => setAssetPicker({ key: field.key, kind: /video|film/i.test(field.key) ? "video" : undefined })} />)}
        {!selection.key && visibleCollections.map((definition) => <CollectionEditor key={definition.key} definition={definition} document={document} disabled={disabled} onChange={edit} />)}
        {selection.sectionId && !selection.key && manifest && <div className="cms-reset-section"><button className="cms-text-button" onClick={() => edit(resetSection(document, selection.sectionId!, manifest))}>Reset section to defaults</button>
          {selection.sectionId.startsWith("custom-") && <button className="cms-text-button text-fail" onClick={() => { edit(removeCustomSection(document, selection.sectionId!)); choose({ sectionId: "hero" }); }}>Remove this section</button>}</div>}
      </>}
    </fieldset>
  </>;

  return <div className="cms-workspace">
    <header className="cms-toolbar">
      <div className="cms-toolbar-brand"><Link href="/admin" className="cms-back-link" aria-label="Back to admin">←</Link><LogoMark className="h-5 w-5" /><div><h1>Website</h1><span>Homepage</span></div></div>
      <div className="cms-save-state" role="status" aria-live="polite"><span className={snapshot.status === "error" || snapshot.status === "conflict" ? "text-fail" : ""}>{saveLabel}</span>{server.publishedRevision > 0 && <small>Live version {server.publishedRevision}</small>}</div>
      <div className="cms-device-switch" role="group" aria-label="Preview size">{(["desktop", "tablet", "mobile"] as Device[]).map((item) => <button key={item} title={`${item[0].toUpperCase() + item.slice(1)} preview`} aria-label={`${item} preview`} aria-pressed={device === item} onClick={() => chooseDevice(item)}><DeviceIcon device={item} /></button>)}</div>
      <div className="cms-toolbar-actions"><button className="cms-icon-button" title="Undo" aria-label="Undo" disabled={!undo.length || disabled} onClick={undoEdit}>↶</button><button className="cms-icon-button" title="Redo" aria-label="Redo" disabled={!redo.length || disabled} onClick={redoEdit}>↷</button><button className="cms-preview-button" aria-pressed={mode === "preview"} onClick={() => setMode(mode === "edit" ? "preview" : "edit")}>{mode === "preview" ? "Edit" : "Preview"}</button><button className="cms-publish-button" disabled={!canPublish || busy || !ready || snapshot.status === "conflict" || (Boolean(historical) && !pendingPublish)} onClick={() => void publish()}>{busy ? "Publishing…" : pendingPublish ? "Retry publish" : "Publish"}</button></div>
    </header>
    {(error || snapshot.error || notice) && <div className={`cms-banner ${error || snapshot.error ? "cms-banner-error" : ""}`} role={error || snapshot.error ? "alert" : "status"}><span>{error ?? snapshot.error?.message ?? notice}</span>{snapshot.status === "error" && <button onClick={() => void controller.retry().catch((cause) => setError(messageOf(cause)))}>Retry save</button>}<button aria-label="Dismiss message" onClick={() => { setError(null); setNotice(null); }}>×</button></div>}
    {snapshot.status === "conflict" && <div className="cms-banner cms-banner-error"><span>Another editor saved a newer draft. Load it, or explicitly replace it with your edits.</span><button disabled={busy} onClick={() => void resolveConflict(false)}>Load latest</button><button disabled={busy} onClick={() => void resolveConflict(true)}>Keep my edits</button></div>}
    {Boolean(server.warnings?.length) && <div className="cms-banner cms-banner-error"><span>{server.warnings!.length} saved {server.warnings!.length === 1 ? "setting needs" : "settings need"} review. Valid content has been recovered; review the draft and save a change before publishing.</span></div>}
    {pendingPublish && !busy && <div className="cms-banner cms-banner-error"><span>The publishing response was interrupted. Editing is paused until the result is confirmed.</span><button onClick={() => void publish()}>Retry the same publish</button></div>}
    {historical && <div className="cms-banner"><span>Previewing published version {historical.revision}. Your current draft is kept.</span><button onClick={() => setHistorical(null)}>Back to draft</button><button disabled={!canPublish || busy} onClick={() => setRestoreRevision(historical.revision)}>Restore this version</button></div>}
    <div className="cms-mobile-toolbar"><button onClick={() => setMobilePanel("sections")}>Sections</button><button onClick={() => setMobilePanel("inspector")}>Edit selection</button><select value={device} aria-label="Preview size" onChange={(event) => chooseDevice(event.target.value as Device)}><option value="desktop">Desktop</option><option value="tablet">Tablet</option><option value="mobile">Mobile</option></select></div>
    <div className="cms-mobile-history"><span role="status">{saveLabel}</span><button className="cms-icon-button" aria-label="Undo change" disabled={!undo.length || disabled} onClick={undoEdit}>↶</button><button className="cms-icon-button" aria-label="Redo change" disabled={!redo.length || disabled} onClick={redoEdit}>↷</button></div>
    <div className={`cms-panels ${mode === "preview" ? "is-previewing" : ""}`}>
      <aside className="cms-sidebar" aria-label="Page structure">{sidebar}</aside>
      <div className="cms-canvas-shell"><div className="cms-canvas-caption"><span>{mode === "edit" && !historical ? "Click any element to edit" : "Website preview"}</span><span>{width}px · {Math.round(scale * 100)}%</span></div>
        <div className="cms-canvas" ref={previewArea}>
          {previewUrl && <div className="cms-preview-frame" style={{ width: width * scale, height: previewSize.height }}><iframe ref={iframe} title="Antifailure homepage preview" src={previewUrl} style={{ width, height: previewSize.height / scale, transform: `scale(${scale})` }} /></div>}
          {!ready && <div className="cms-preview-loading" role="status"><strong>Loading the real homepage</strong><span>Your saved draft will appear here.</span></div>}
          {previewError && <div className="cms-preview-error" role="alert"><p>{previewError}</p><button className="cms-small-button" onClick={() => { setReady(false); setPreviewError(null); if (iframe.current) iframe.current.src = previewUrl; }}>Reload preview</button></div>}
        </div>
        <div className="cms-canvas-bottom"><span>{server.refresh?.status === "failed" ? "Static page refresh needs attention" : server.refresh && !["deployed", "superseded"].includes(server.refresh.status) ? "Published content is live. Refreshing static pages…" : "Source defaults stay in sync automatically"}</span>{server.refresh?.status === "failed" && <button onClick={() => void adminMutate("admin.administration.website.retryRefresh", { revision: server.refresh!.revision }).then(() => query<WebsiteState>("admin.administration.website.get")).then(setServer).catch((cause) => setError(messageOf(cause)))}>Retry refresh</button>}<a href={WEBSITE_ORIGIN} target="_blank" rel="noreferrer">View live site ↗</a></div>
      </div>
      <aside className="cms-inspector" aria-label="Element inspector">{inspector}</aside>
    </div>
    <Drawer open={mobilePanel === "sections"} title="Website settings" onClose={() => setMobilePanel(null)}>{mobilePanel === "sections" ? sidebar : null}</Drawer>
    <Drawer open={mobilePanel === "inspector"} title="Edit selection" onClose={() => setMobilePanel(null)}>{mobilePanel === "inspector" ? inspector : null}</Drawer>
    <Drawer open={Boolean(addBlock)} title="Add a section" onClose={() => setAddBlock(null)}><div className="cms-presets">{SECTION_PRESETS.map((preset) => <button key={preset.kind} className="cms-preset" onClick={() => add(preset.kind)}><PresetArt kind={preset.kind} /><strong>{preset.label}</strong><span>{preset.description}</span></button>)}</div></Drawer>
    <AssetLibrary open={Boolean(assetPicker)} onClose={() => setAssetPicker(null)} onSelect={assetPicker?.key ? selectAsset : undefined} kind={assetPicker?.kind} mediaOnly={Boolean(assetPicker?.key)} beforeDelete={async () => { await controller.flush(); }} websiteOrigin={WEBSITE_ORIGIN} builtinAssets={manifest?.builtinAssets} onAssetsChanged={() => setAssetVersion((value) => value + 1)} disabled={disabled}
      onSelectBuiltin={assetPicker?.key ? (asset) => { edit(setFieldOverride(document, assetPicker.key!, { type: "media", source: "builtin", src: asset.src, kind: asset.kind, alt: "" })); setAssetPicker(null); } : undefined} />
    <Drawer open={historyOpen} title="Published versions" onClose={() => setHistoryOpen(false)}><div className="cms-history"><p>Preview any published version, then restore it as a new version.</p>{historyError && <p role="alert" className="text-fail">{historyError}<button onClick={() => void loadHistory()}>Try again</button></p>}{!history.length && !historyLoading && <p>No published versions yet. Your first publish starts the history.</p>}{history.map((item) => <div key={item.revision} className="cms-history-row"><div><strong>Version {item.revision}{item.revision === server.publishedRevision ? " · Live" : ""}</strong><span>{new Date(item.createdAt).toLocaleString()}</span></div><button className="cms-small-button" disabled={historyLoading} onClick={() => void previewHistory(item.revision)}>Preview</button></div>)}{historyLoading && <p role="status">Loading versions…</p>}{historyCursor && <button className="cms-add-button" disabled={historyLoading} onClick={() => void loadHistory(true)}>Load older versions</button>}</div></Drawer>
    <Confirm open={restoreRevision !== null} title={`Restore version ${restoreRevision}?`} confirmLabel="Restore and publish" cancelLabel="Keep current version" tone="primary" busy={busy} error={error} onConfirm={() => { if (restoreRevision !== null) void publish(restoreRevision); }} onCancel={() => setRestoreRevision(null)}>This publishes the selected version as a new version. The current version remains in history.</Confirm>
    <Confirm open={Boolean(recovery)} title="Recover unsaved changes?" confirmLabel="Recover changes" cancelLabel="Use saved draft" tone="primary" onConfirm={() => { if (recovery) edit(recovery); setRecovery(null); }} onCancel={() => { setRecovery(null); sessionStorage.removeItem(recoveryKey); }}>This browser kept edits that were not confirmed saved. Recover them into your draft, then review before publishing.</Confirm>
  </div>;
}

function FieldEditor({ field, value, customized, onChange, onReset, onMedia, assetUrls, websiteOrigin, disabled }: {
  field: FieldDefinition; value: FieldValue; customized: boolean; onChange(value: FieldValue): void; onReset(): void; onMedia(): void; assetUrls: Record<string, string>; websiteOrigin: string; disabled: boolean;
}) {
  const id = useId();
  const media = value && typeof value === "object" && value.type === "media" ? value as MediaReference : null;
  const src = media?.source === "asset" ? assetUrls[media.assetId] : media?.source === "builtin" ? new URL(media.src, websiteOrigin).toString() : null;
  return <div className="cms-field"><div className="cms-field-label"><label htmlFor={id}>{field.label}</label>{customized && <button className="cms-text-button" onClick={onReset} disabled={disabled}>Reset</button>}</div>
    {field.kind === "richtext" ? <RichTextInput value={typeof value === "string" || (value && typeof value === "object" && value.type === "doc") ? value : ""} onChange={onChange} label={field.label} disabled={disabled} /> :
      field.kind === "media" ? <><button className="cms-media-select" onClick={onMedia} disabled={disabled}>{src ? media?.kind === "video" || /\.(mp4|webm)(\?|$)/i.test(src) || /video|film/i.test(field.key) ? <span>Video selected<br /><small>Choose a different file</small></span> : <img src={src} alt="Selected media preview" /> : <span>Choose an image or video</span>}<span className="cms-media-replace">{media ? "Replace media" : "+ Select media"}</span></button>{media && <><label className="cms-label">Alternative text<input className={inputClass} value={media.alt ?? ""} disabled={disabled || media.decorative} onChange={(event) => onChange({ ...media, alt: event.target.value })} /></label><label className="cms-check"><input type="checkbox" checked={media.decorative ?? false} onChange={(event) => onChange({ ...media, decorative: event.target.checked })} />Decorative image</label><button className="cms-text-button" onClick={() => onChange(null)}>Remove media</button></>}</> :
      field.kind === "boolean" ? <label className="cms-check"><input id={id} aria-label={field.label} type="checkbox" checked={Boolean(value)} onChange={(event) => onChange(event.target.checked)} />Enabled</label> :
        field.kind === "select" ? <select id={id} aria-label={field.label} className={selectClass} value={String(value)} onChange={(event) => onChange(field.options?.find((option) => String(option.value) === event.target.value)?.value ?? event.target.value)}>{field.options?.map((option) => <option key={String(option.value)} value={String(option.value)}>{option.label}</option>)}</select> :
          field.kind === "number" ? <input id={id} aria-label={field.label} className={inputClass} type="number" value={Number(value)} onChange={(event) => { if (event.target.value !== "") onChange(Number(event.target.value)); }} /> :
            field.kind === "url" ? <input id={id} aria-label={field.label} className={inputClass} value={String(value ?? "")} onChange={(event) => onChange(event.target.value)} spellCheck={false} /> :
              <textarea id={id} aria-label={field.label} className={`${inputClass}${field.kind === "code" ? " cms-code-input" : ""}`} value={String(value ?? "")} rows={field.kind === "code" ? 14 : String(value).length > 90 ? 4 : 2} spellCheck={field.kind !== "code"} onChange={(event) => onChange(event.target.value)} />}
    {field.help && <p className="cms-help">{field.help}</p>}
  </div>;
}

function DeviceIcon({ device }: { device: Device }) {
  return <svg width="20" height="20" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.4" aria-hidden>{device === "desktop" ? <><rect x="2" y="3" width="16" height="11" rx="1" /><path d="M10 14v3M6 17h8" /></> : device === "tablet" ? <><rect x="4" y="2" width="12" height="16" rx="1" /><path d="M9 15h2" /></> : <><rect x="6" y="1" width="8" height="18" rx="1" /><path d="M9 16h2" /></>}</svg>;
}

function PresetArt({ kind }: { kind: CustomSectionKind }) {
  if (kind === "embed") return <svg className="cms-preset-art" viewBox="0 0 160 74" fill="none" aria-hidden><rect x="14" y="12" width="132" height="50" stroke="currentColor" opacity=".2" /><path d="m61 27-14 10 14 10m38-20 14 10-14 10M86 23 74 51" stroke="currentColor" strokeWidth="2" /></svg>;
  if (kind === "shape") return <svg className="cms-preset-art" viewBox="0 0 160 74" fill="none" aria-hidden><circle cx="44" cy="37" r="20" fill="currentColor" opacity=".15" /><path d="M76 57V37a20 20 0 0 1 40 0v20Z" fill="currentColor" opacity=".55" /><circle cx="125" cy="43" r="13" stroke="currentColor" strokeWidth="3" /></svg>;
  if (kind === "divider") return <svg className="cms-preset-art" viewBox="0 0 160 74" fill="none" aria-hidden><path d="M14 23h132" stroke="currentColor" opacity=".4" /><path d="M14 48c16-20 28 20 44 0s28 20 44 0 28 20 44 0" stroke="currentColor" strokeWidth="2" /></svg>;
  return <svg className="cms-preset-art" viewBox="0 0 160 74" fill="none" aria-hidden>{kind === "spacer" ? <><path d="M12 12h136M12 62h136" stroke="currentColor" opacity=".3" /><path d="M80 20v34m-5-29 5-5 5 5m-10 24 5 5 5-5" stroke="currentColor" /></> : <>{["image", "video", "split"].includes(kind) && <><rect x="12" y="12" width={kind === "split" ? "64" : "136"} height="50" fill="currentColor" opacity=".1" /><path d={kind === "split" ? "m21 51 16-16 12 11 10-8 11 13" : "m25 53 30-27 24 21 21-14 30 20"} stroke="currentColor" opacity=".5" />{kind === "video" && <path d="m75 27 16 10-16 10Z" fill="currentColor" />}</>}{["text", "cta", "features", "split"].includes(kind) && <>{(kind === "features" ? [12, 62, 112] : [kind === "split" ? 90 : 12]).map((x) => <g key={x}><rect x={x} y="20" width={kind === "features" ? 34 : 55} height="5" fill="currentColor" /><rect x={x} y="34" width={kind === "features" ? 34 : 55} height="2" fill="currentColor" opacity=".3" /><rect x={x} y="41" width={kind === "features" ? 29 : 45} height="2" fill="currentColor" opacity=".3" />{kind === "cta" && <rect x={x} y="51" width="42" height="9" rx="2" fill="currentColor" />}</g>)}</>}</>}</svg>;
}
