"use client";

import { useCallback, useEffect, useId, useRef, useState } from "react";
import type { WebsiteManifest } from "@antifailure/website";
import { Drawer } from "@/components/admin/primitives";
import { Confirm } from "@/components/ui";
import { adminMutate, adminUpload } from "@/lib/admin";
import { query } from "@/lib/api";
import styles from "./AssetLibrary.module.css";

export interface WebsiteAsset {
  id: string;
  name: string;
  kind: "image" | "video" | "font";
  mimeType: string;
  sizeBytes: number;
  width: number | null;
  height: number | null;
  isPublic: boolean;
  archived: boolean;
  createdAt: string;
}

type BuiltinAsset = WebsiteManifest["builtinAssets"][number];
type Cursor = { id: string; createdAt: string };
type Kind = WebsiteAsset["kind"];
type AssetPage = { items: WebsiteAsset[]; nextCursor: Cursor | null; skipped: number };

interface AssetLibraryProps {
  open: boolean;
  onClose: () => void;
  onSelect?: (asset: WebsiteAsset) => void;
  kind?: Kind;
  mediaOnly?: boolean;
  builtinAssets?: WebsiteManifest["builtinAssets"];
  onSelectBuiltin?: (asset: BuiltinAsset) => void;
  websiteOrigin: string;
  onAssetsChanged?: () => void;
  beforeDelete?: () => Promise<void>;
  disabled?: boolean;
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const LIMITS: Record<Kind, number> = { image: 12 * 1024 ** 2, video: 64 * 1024 ** 2, font: 4 * 1024 ** 2 };
const EXTENSIONS: Record<string, Kind> = { png: "image", jpg: "image", jpeg: "image", webp: "image", gif: "image", mp4: "video", webm: "video", woff2: "font" };
const ACCEPT: Record<Kind, string> = { image: ".png,.jpg,.jpeg,.webp,.gif", video: ".mp4,.webm", font: ".woff2" };

function record(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function assetFrom(value: unknown): WebsiteAsset | null {
  if (!record(value) || typeof value.id !== "string" || !UUID.test(value.id) || typeof value.name !== "string" ||
    !["image", "video", "font"].includes(String(value.kind)) || typeof value.mimeType !== "string" ||
    typeof value.sizeBytes !== "number" || !Number.isSafeInteger(value.sizeBytes) || value.sizeBytes < 0 ||
    typeof value.createdAt !== "string" || !Number.isFinite(Date.parse(value.createdAt)) ||
    typeof value.isPublic !== "boolean" || typeof value.archived !== "boolean") return null;
  const dimension = (v: unknown) => typeof v === "number" && Number.isFinite(v) && v > 0 ? v : null;
  return { id: value.id, name: value.name, kind: value.kind as Kind, mimeType: value.mimeType, sizeBytes: value.sizeBytes,
    width: dimension(value.width), height: dimension(value.height), isPublic: value.isPublic, archived: value.archived, createdAt: value.createdAt };
}

function pageFrom(value: unknown): AssetPage {
  if (!record(value) || !Array.isArray(value.items)) throw new Error("The file list could not be read. Try again.");
  const items = value.items.map(assetFrom).filter((asset): asset is WebsiteAsset => asset !== null);
  let nextCursor: Cursor | null = null;
  if (value.nextCursor !== null && value.nextCursor !== undefined) {
    const c = value.nextCursor;
    if (!record(c) || typeof c.id !== "string" || !UUID.test(c.id) || typeof c.createdAt !== "string" || !Number.isFinite(Date.parse(c.createdAt))) {
      throw new Error("The next page of files could not be read. Refresh the library.");
    }
    nextCursor = { id: c.id, createdAt: c.createdAt };
  }
  return { items, nextCursor, skipped: value.items.length - items.length };
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "Something went wrong. Try again.";
}

function sizeLabel(size: number): string {
  return size >= 1024 ** 2 ? `${(size / 1024 ** 2).toFixed(1)} MB` : `${Math.max(1, Math.ceil(size / 1024))} KB`;
}

function fileProblem(file: File, expectedKind?: Kind, mediaOnly = false): string | null {
  const extension = file.name.toLowerCase().split(".").at(-1) ?? "";
  const detectedKind = EXTENSIONS[extension];
  if (!detectedKind) return mediaOnly ? "Choose a PNG, JPEG, WebP, GIF, MP4, or WebM file." : "Choose a PNG, JPEG, WebP, GIF, MP4, WebM, or WOFF2 file.";
  if (mediaOnly && detectedKind === "font") return "Choose an image or video for this field. Fonts belong in the typography settings.";
  if (expectedKind && detectedKind !== expectedKind) return `Choose ${expectedKind === "image" ? "an image" : expectedKind === "video" ? "a video" : "a WOFF2 font"} for this field.`;
  if (!file.size) return "This file is empty.";
  if (file.size > LIMITS[detectedKind]) return `${detectedKind === "image" ? "Images" : detectedKind === "video" ? "Videos" : "Fonts"} can be up to ${LIMITS[detectedKind] / 1024 ** 2} MB.`;
  return null;
}

/** Preview capabilities come from the API. Accept only this API's media route,
 * never an arbitrary URL returned in a malformed response. */
function previewUrl(path: unknown, id: string): string | null {
  if (typeof path !== "string") return null;
  try {
    const origin = new URL(process.env.NEXT_PUBLIC_AF_API || window.location.origin).origin;
    const url = new URL(path, origin);
    return url.origin === origin && url.pathname === `/v1/website/media/${id}` && !url.username && !url.password ? url.href : null;
  } catch { return null; }
}

function builtinUrl(asset: BuiltinAsset, websiteOrigin: string): string | null {
  if (!asset.src.startsWith("/") || asset.src.startsWith("//")) return null;
  try {
    const base = new URL(websiteOrigin);
    if (base.protocol !== "https:" && !(base.protocol === "http:" && ["localhost", "127.0.0.1", "[::1]"].includes(base.hostname))) return null;
    const url = new URL(asset.src, base.origin);
    return url.origin === base.origin ? url.href : null;
  } catch { return null; }
}

function FileSymbol({ kind }: { kind: Kind }) {
  return <svg aria-hidden="true" viewBox="0 0 40 40" fill="none" className={styles.fileSymbol}>
    {kind === "image" ? <><rect x="6" y="8" width="28" height="24" rx="2" /><circle cx="15" cy="16" r="2" /><path d="m7 27 8-8 7 7 5-5 7 6" /></> :
      kind === "video" ? <><rect x="5" y="8" width="30" height="24" rx="2" /><path d="m17 15 8 5-8 5Z" /></> :
        <><path d="m8 30 9-22 9 22M11 23h12M28 18v12M28 22c5-7 9-3 5 2l-5 2" /></>}
  </svg>;
}

function MediaPreview({ asset, url, expanded = false }: { asset: Pick<WebsiteAsset, "id" | "name" | "kind">; url?: string; expanded?: boolean }) {
  const [failed, setFailed] = useState(false);
  const [fontReady, setFontReady] = useState(false);
  const [fontVisible, setFontVisible] = useState(false);
  const fontSample = useRef<HTMLDivElement>(null);
  const family = `website-library-${asset.id.replace(/[^a-z0-9-]/gi, "")}`;
  useEffect(() => { setFailed(false); setFontReady(false); }, [url]);
  useEffect(() => {
    if (asset.kind !== "font" || !url || !fontSample.current) return;
    const observer = new IntersectionObserver(([entry]) => {
      if (entry.isIntersecting) { setFontVisible(true); observer.disconnect(); }
    }, { rootMargin: "100px" });
    observer.observe(fontSample.current);
    return () => observer.disconnect();
  }, [asset.kind, url]);
  useEffect(() => {
    if (asset.kind !== "font" || !url || !fontVisible) return;
    let disposed = false;
    const face = new FontFace(family, `url(${JSON.stringify(url)})`);
    void face.load().then((loaded) => {
      if (!disposed) { document.fonts.add(loaded); setFontReady(true); }
    }).catch(() => { if (!disposed) setFailed(true); });
    return () => { disposed = true; document.fonts.delete(face); };
  }, [asset.kind, family, url, fontVisible]);

  if (!url || failed) return <div className={styles.previewFallback}><FileSymbol kind={asset.kind} /><span>{failed ? "Preview unavailable" : asset.kind === "font" ? "WOFF2 font" : "Preview loading"}</span></div>;
  if (asset.kind === "font") return <div ref={fontSample} className={styles.fontSample} style={fontReady ? { fontFamily: family } : undefined}>
    <span>{expanded ? "Make it yours." : "Aa Bb Cc"}</span><small>{fontReady ? "ABCDEFGHIJKLMNOPQRSTUVWXYZ\n0123456789" : "Loading font sample"}</small>
  </div>;
  if (asset.kind === "video") return <video className={styles.media} src={url} aria-label={asset.name} controls={expanded} muted={!expanded} playsInline preload="metadata" onError={() => setFailed(true)} />;
  // These private URLs use short-lived capabilities and are already bounded by
  // the media service. Next's public optimization service must not fetch them.
  // eslint-disable-next-line @next/next/no-img-element
  return <img className={styles.media} src={url} alt={expanded ? asset.name : ""} loading="lazy" onError={() => setFailed(true)} />;
}

export function AssetLibrary({ open, onClose, onSelect, kind, mediaOnly = false, builtinAssets = [], onSelectBuiltin, websiteOrigin, onAssetsChanged, beforeDelete, disabled = false }: AssetLibraryProps) {
  const id = useId();
  const input = useRef<HTMLInputElement>(null);
  const [tab, setTab] = useState<"uploaded" | "builtin">("uploaded");
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState<Kind | "all">(kind ?? "all");
  const [archived, setArchived] = useState(false);
  const [items, setItems] = useState<WebsiteAsset[]>([]);
  const [cursor, setCursor] = useState<Cursor | null>(null);
  const [loading, setLoading] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [urls, setUrls] = useState<Record<string, string>>({});
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [reload, setReload] = useState(0);
  const [previewReload, setPreviewReload] = useState(0);
  const [uploading, setUploading] = useState<{ name: string; index: number; total: number } | null>(null);
  const [uploadErrors, setUploadErrors] = useState<string[]>([]);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [selected, setSelected] = useState<WebsiteAsset | null>(null);
  const [deleting, setDeleting] = useState<WebsiteAsset | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [dragging, setDragging] = useState(false);
  const generation = useRef(0);
  const alive = useRef(true);
  const uploadInFlight = useRef(false);
  const requestedKind = kind ?? (filter === "all" ? undefined : filter);
  const effectiveKind = mediaOnly && requestedKind === "font" ? undefined : requestedKind;
  const refresh = () => setReload((r) => r + 1);
  useEffect(() => { alive.current = true; return () => { alive.current = false; generation.current++; }; }, []);
  useEffect(() => { setFilter(kind ?? "all"); setSelected(null); setTab("uploaded"); }, [kind]);
  useEffect(() => {
    if (mediaOnly) {
      setFilter((current) => current === "font" ? "all" : current);
      setSelected((current) => current?.kind === "font" ? null : current);
    }
  }, [mediaOnly]);
  useEffect(() => { if (!builtinAssets.length || kind === "font") setTab("uploaded"); }, [builtinAssets.length, kind]);
  useEffect(() => { if (!open) { setSelected(null); setDeleting(null); setDragging(false); } }, [open]);

  const readPage = useCallback(async (next?: Cursor) => {
    const current = generation.current;
    let pageCursor = next;
    let skipped = 0;
    const seen = new Set<string>();
    for (;;) {
      const page = pageFrom(await query<unknown>("admin.administration.website.assets", {
        ...(search.trim() ? { query: search.trim() } : {}), ...(effectiveKind ? { kind: effectiveKind } : {}),
        archived, limit: 30, ...(pageCursor ? { cursor: pageCursor } : {}),
      }));
      if (generation.current !== current) throw new Error("This file request was replaced by a newer search.");
      skipped += page.skipped;
      const visible = mediaOnly ? page.items.filter((asset) => asset.kind !== "font") : page.items;
      if (visible.length || !page.nextCursor || !mediaOnly) return { ...page, items: visible, skipped };
      // A page of fonts is not an empty media library. Continue to the next
      // page, retaining its cursor for normal pagination once media appears.
      if (seen.has(page.nextCursor.id)) throw new Error("The file list could not advance. Refresh the library.");
      seen.add(page.nextCursor.id);
      pageCursor = page.nextCursor;
    }
  }, [search, effectiveKind, archived, mediaOnly]);

  useEffect(() => {
    const current = ++generation.current;
    if (!open || tab !== "uploaded") return;
    setItems([]); setCursor(null); setUrls({}); setError(null); setLoading(true); setLoadingMore(false);
    const timer = window.setTimeout(() => {
      void readPage().then((page) => {
        if (generation.current !== current) return;
        setItems(page.items); setCursor(page.nextCursor);
        if (page.skipped) setNotice(`${page.skipped} ${page.skipped === 1 ? "file could" : "files could"} not be read. The other files are available.`);
      }).catch((err: unknown) => { if (generation.current === current) setError(errorMessage(err)); })
        .finally(() => { if (generation.current === current) setLoading(false); });
    }, search ? 240 : 0);
    return () => { window.clearTimeout(timer); generation.current++; };
  }, [open, tab, readPage, reload, search]);

  useEffect(() => {
    if (!open || !items.length) return;
    let canceled = false;
    const current = generation.current;
    setPreviewError(null);
    const fetchPreviews = async () => {
      const all: Record<string, string> = {};
      let missing = false;
      for (let offset = 0; offset < items.length; offset += 60) {
        const batch = items.slice(offset, offset + 60);
        const response = await query<unknown>("admin.administration.website.assetPreview", { ids: batch.map((asset) => asset.id) });
        if (!record(response) || !record(response.urls)) throw new Error("File previews could not be loaded.");
        for (const asset of batch) {
          const url = previewUrl(response.urls[asset.id], asset.id);
          if (url) all[asset.id] = url;
          else missing = true;
        }
      }
      if (!canceled && generation.current === current) {
        setUrls(all);
        if (missing) setPreviewError("Some file previews are unavailable. Retry to load them again.");
      }
    };
    void fetchPreviews().catch((err: unknown) => { if (!canceled && generation.current === current) setPreviewError(errorMessage(err)); });
    // Capabilities last 15 minutes. Refresh while the library remains open.
    const timer = window.setTimeout(() => { if (!canceled) setPreviewReload((n) => n + 1); }, 12 * 60 * 1000);
    return () => { canceled = true; window.clearTimeout(timer); };
  }, [open, items, archived, previewReload]);

  async function loadMore() {
    if (!cursor || loadingMore) return;
    const current = generation.current;
    setLoadingMore(true); setError(null);
    try {
      const page = await readPage(cursor);
      if (generation.current !== current) return;
      setItems((previous) => [...previous, ...page.items.filter((item) => !previous.some((p) => p.id === item.id))]);
      setCursor(page.nextCursor);
      if (page.skipped) setNotice("Some file details could not be read. The other files are available.");
    } catch (err) { if (generation.current === current) setError(errorMessage(err)); }
    finally { if (generation.current === current) setLoadingMore(false); }
  }

  async function upload(files: File[]) {
    if (disabled || uploadInFlight.current || !files.length) return;
    uploadInFlight.current = true;
    setUploadErrors([]); setNotice(null);
    const failures: string[] = [];
    let count = 0;
    try {
      for (const [index, file] of files.entries()) {
        if (!alive.current) break;
        const problem = fileProblem(file, mediaOnly && kind === "font" ? undefined : kind, mediaOnly);
        if (problem) { failures.push(`${file.name}: ${problem}`); continue; }
        setUploading({ name: file.name, index: index + 1, total: files.length });
        try {
          const response = await adminUpload<unknown>("/v1/admin/website/media", file);
          if (!assetFrom(response)) throw new Error("The upload response was incomplete. Refresh the library before trying again.");
          count++;
        } catch (err) { failures.push(`${file.name}: ${errorMessage(err)}`); }
      }
    } finally {
      uploadInFlight.current = false;
      if (alive.current) {
        setUploading(null); setUploadErrors(failures);
        if (count) {
          setNotice(`${count === 1 ? "File added" : `${count} files added`} to your library.`);
          setSearch(""); setTab("uploaded"); setArchived(false); setFilter(kind ?? "all"); refresh();
          onAssetsChanged?.();
        }
      }
    }
  }

  async function archive(asset: WebsiteAsset) {
    if (disabled || busyId) return;
    setBusyId(asset.id); setError(null);
    try {
      await adminMutate("admin.administration.website.archiveAsset", { id: asset.id, archived: !asset.archived });
      if (!alive.current) return;
      setSelected(null); setNotice(asset.archived ? "File restored to the library." : "File archived.");
      refresh(); onAssetsChanged?.();
    } catch (err) { if (alive.current) setError(errorMessage(err)); }
    finally { if (alive.current) setBusyId(null); }
  }

  async function deleteAsset() {
    if (!deleting || disabled || busyId) return;
    setBusyId(deleting.id); setDeleteError(null);
    try {
      await beforeDelete?.();
      await adminMutate("admin.administration.website.deleteAsset", { id: deleting.id });
      if (!alive.current) return;
      setDeleting(null); setSelected(null); setNotice("File deleted."); refresh(); onAssetsChanged?.();
    } catch (err) { if (alive.current) setDeleteError(errorMessage(err)); }
    finally { if (alive.current) setBusyId(null); }
  }

  const originals = builtinAssets.filter((asset) => (!effectiveKind || asset.kind === effectiveKind) && asset.label.toLowerCase().includes(search.trim().toLowerCase()));
  const pick = (asset: WebsiteAsset) => { if (!disabled && !asset.archived && !(mediaOnly && asset.kind === "font")) { onSelect?.(asset); onClose(); } };

  return <>
    <Drawer open={open} title={onSelect || onSelectBuiltin ? "Choose a file" : "Media library"} onClose={onClose}>
      {open ? <div className={styles.library} onDragEnter={(e) => { if (!disabled && e.dataTransfer.types.includes("Files")) { e.preventDefault(); setDragging(true); } }}
        onDragOver={(e) => { if (!disabled && e.dataTransfer.types.includes("Files")) e.preventDefault(); }}
        onDragLeave={(e) => { if (!(e.relatedTarget instanceof Node) || !e.currentTarget.contains(e.relatedTarget)) setDragging(false); }}
        onDrop={(e) => { if (e.dataTransfer.files.length) { e.preventDefault(); setDragging(false); void upload(Array.from(e.dataTransfer.files)); } }}>
        {dragging ? <div className={styles.dropOverlay}><FileSymbol kind={kind ?? "image"} /><strong>Drop files to upload</strong></div> : null}
        {selected ? <>
          <button className={styles.back} type="button" onClick={() => setSelected(null)}>← All files</button>
          <div className={styles.detailPreview}>
            <MediaPreview asset={selected} url={urls[selected.id]} expanded />
          </div>
          <div className={styles.detailBody}>
            <h3>{selected.name}</h3>
            <dl className={styles.facts}>
              <div><dt>File type</dt><dd>{selected.mimeType}</dd></div>
              <div><dt>Size</dt><dd>{sizeLabel(selected.sizeBytes)}</dd></div>
              {selected.width && selected.height ? <div><dt>Dimensions</dt><dd>{selected.width} × {selected.height}</dd></div> : null}
              <div><dt>Added</dt><dd>{new Date(selected.createdAt).toLocaleDateString()}</dd></div>
              <div><dt>Availability</dt><dd>{selected.archived ? "Archived" : selected.isPublic ? "Published" : "Draft only"}</dd></div>
            </dl>
            {onSelect && !selected.archived ? <button type="button" className={styles.primary} disabled={disabled || Boolean(busyId)} onClick={() => pick(selected)}>Use this {selected.kind}</button> : null}
            {error ? <p role="alert" className={styles.error}>{error}</p> : null}
            <div className={styles.fileActions}>
              <button type="button" className={styles.button} disabled={disabled || Boolean(busyId)} onClick={() => void archive(selected)}>{busyId === selected.id ? "Saving…" : selected.archived ? "Restore file" : "Archive file"}</button>
              <button type="button" className={styles.danger} disabled={disabled || Boolean(busyId)} onClick={() => { setDeleteError(null); setDeleting(selected); }}>Delete file</button>
            </div>
            <p className={styles.help}>Archiving hides a file from the library. Pages that use it keep their file. Files used by a saved page or version cannot be deleted.</p>
          </div>
        </> : <>
          <div className={styles.uploadArea}>
            <div><p className={styles.uploadTitle}>Your site, your assets.</p><p className={styles.help}>Drop files here or choose from your device.</p></div>
            <button type="button" className={styles.primary} disabled={disabled || Boolean(uploading)} onClick={() => input.current?.click()}>{uploading ? "Uploading…" : "Upload files"}</button>
            <input ref={input} className={styles.hidden} type="file" accept={mediaOnly ? kind === "image" || kind === "video" ? ACCEPT[kind] : `${ACCEPT.image},${ACCEPT.video}` : kind ? ACCEPT[kind] : Object.values(ACCEPT).join(",")} multiple disabled={disabled || Boolean(uploading)} aria-label="Upload website files" onChange={(e) => { const files = Array.from(e.currentTarget.files ?? []); e.currentTarget.value = ""; void upload(files); }} />
            <p className={styles.limits}>{kind === "font" ? "WOFF2 up to 4 MB." : kind === "video" ? "MP4 or WebM up to 64 MB." : kind === "image" ? "PNG, JPEG, WebP or GIF up to 12 MB." : mediaOnly ? "Images up to 12 MB. MP4 or WebM videos up to 64 MB." : "Images up to 12 MB, videos 64 MB, WOFF2 fonts 4 MB."}</p>
          </div>
          {uploading ? <p className={styles.status} role="status">Uploading {uploading.index} of {uploading.total}: <span>{uploading.name}</span></p> : null}
          {uploadErrors.length ? <div role="alert" className={styles.errorBox}><strong>{uploadErrors.length === 1 ? "One file could not be added" : "Some files could not be added"}</strong><ul>{uploadErrors.map((message, index) => <li key={`${index}-${message}`}>{message}</li>)}</ul></div> : null}
          {notice ? <p className={styles.status} role="status">{notice}<button className={styles.dismiss} type="button" aria-label="Dismiss file update" onClick={() => setNotice(null)}>×</button></p> : null}
          {builtinAssets.length > 0 && kind !== "font" ? <div className={styles.tabs} aria-label="File source">
            <button type="button" aria-pressed={tab === "uploaded"} onClick={() => setTab("uploaded")}>Uploaded</button>
            <button type="button" aria-pressed={tab === "builtin"} onClick={() => { if (filter === "font") setFilter("all"); setTab("builtin"); }}>Site originals</button>
          </div> : null}
          <div className={styles.filters}>
            <label className={styles.search}><span className={styles.srOnly}>Search files</span><svg viewBox="0 0 20 20" aria-hidden="true" fill="none"><circle cx="8.5" cy="8.5" r="5.5" /><path d="m13 13 4 4" /></svg><input id={`${id}-search`} type="search" value={search} maxLength={180} placeholder="Search files" onChange={(e) => setSearch(e.target.value)} /></label>
            {!kind ? <label><span className={styles.srOnly}>File type</span><select value={filter} onChange={(e) => setFilter(e.target.value as Kind | "all")}><option value="all">All types</option><option value="image">Images</option><option value="video">Videos</option>{tab === "uploaded" && !mediaOnly ? <option value="font">Fonts</option> : null}</select></label> : null}
          </div>
          {tab === "uploaded" ? <div className={styles.listHeading}><span>{loading ? "Loading library" : `${items.length} ${items.length === 1 ? "file" : "files"}${cursor ? " shown" : ""}`}</span><div className={styles.listTools}><button type="button" className={styles.refresh} disabled={loading} aria-label="Refresh files" title="Refresh files" onClick={refresh}><svg viewBox="0 0 20 20" fill="none" aria-hidden="true"><path d="M16 7a6.5 6.5 0 1 0 .3 5M16 3v4h-4" /></svg></button><label><input type="checkbox" checked={archived} onChange={(e) => setArchived(e.target.checked)} /> Archived</label></div></div> : <p className={styles.originalNote}>Original artwork shipped with the site. Choose it any time to return to the original design.</p>}
          {previewError ? <div className={styles.errorBox} role="alert"><p>{previewError}</p><button type="button" className={styles.textButton} onClick={() => setPreviewReload((n) => n + 1)}>Retry previews</button></div> : null}
          {tab === "uploaded" && loading ? <div className={styles.grid} aria-busy="true" aria-label="Loading files">{[0, 1, 2, 3].map((n) => <div className={styles.skeleton} key={n}><div /><span /><span /></div>)}</div> : null}
          {tab === "uploaded" && !loading && items.length > 0 ? <div className={styles.grid}>
            {items.map((asset) => <article className={`${styles.asset} ${asset.kind === "font" ? styles.fontAsset : ""}`} key={asset.id}>
              <button className={styles.preview} type="button" onClick={() => { setError(null); setSelected(asset); }} aria-label={`Preview ${asset.name}`}>
                <MediaPreview asset={asset} url={urls[asset.id]} />
                {asset.kind === "video" && !asset.archived ? <span className={styles.videoLabel}><svg viewBox="0 0 12 12" fill="currentColor" aria-hidden="true"><path d="m3 1 8 5-8 5Z" /></svg> Video</span> : null}
              </button>
              <div className={styles.assetMeta}><p title={asset.name}>{asset.name}</p><span>{sizeLabel(asset.sizeBytes)}{asset.width && asset.height ? ` · ${asset.width} × ${asset.height}` : ` · ${asset.kind === "font" ? "WOFF2" : "Video"}`}</span></div>
              {onSelect && !asset.archived ? <button type="button" className={styles.selectButton} disabled={disabled} onClick={() => pick(asset)}>Use {asset.kind}</button> : <button type="button" className={styles.selectButton} onClick={() => { setError(null); setSelected(asset); }}>File details</button>}
            </article>)}
          </div> : null}
          {tab === "builtin" && originals.length > 0 ? <div className={styles.grid}>{originals.map((asset) => {
            const url = builtinUrl(asset, websiteOrigin);
            return <article className={styles.asset} key={asset.id}>
              <div className={styles.preview}><MediaPreview asset={{ id: asset.id, name: asset.label, kind: asset.kind }} url={url ?? undefined} /></div>
              <div className={styles.assetMeta}><p title={asset.label}>{asset.label}</p><span>Site original · {asset.kind}</span></div>
              {onSelectBuiltin ? <button type="button" className={styles.selectButton} disabled={disabled || !url} onClick={() => { onSelectBuiltin(asset); onClose(); }}>Use {asset.kind}</button> : null}
            </article>;
          })}</div> : null}
          {error ? <div className={styles.errorBox} role="alert"><p>{error}</p><button type="button" className={styles.textButton} onClick={() => cursor && items.length ? void loadMore() : refresh()}>Try again</button></div> : null}
          {!loading && !error && (tab === "uploaded" ? !items.length : !originals.length) ? <div className={styles.empty}><FileSymbol kind={effectiveKind ?? "image"} /><h3>{search ? "No matching files" : archived ? "No archived files" : tab === "builtin" ? "No originals of this type" : "Your library starts here"}</h3><p>{search ? "Try another name or change the file type." : archived ? "Archived files will appear here." : tab === "builtin" ? "Choose another type or upload a file." : mediaOnly ? "Add images or videos to make this section your own." : "Add images, videos, or a font to make the site your own."}</p>{search || (!kind && filter !== "all") ? <button className={styles.button} type="button" onClick={() => { setSearch(""); setFilter(kind ?? "all"); }}>Clear filters</button> : null}</div> : null}
          {tab === "uploaded" && cursor && !loading ? <div className={styles.more}><button type="button" className={styles.button} disabled={loadingMore} onClick={() => void loadMore()}>{loadingMore ? "Loading files…" : "Load more files"}</button></div> : null}
        </>}
      </div> : null}
    </Drawer>
    <Confirm open={Boolean(deleting)} title="Delete this file?" confirmLabel="Delete file" cancelLabel="Keep file" busy={Boolean(busyId)} error={deleteError} onCancel={() => setDeleting(null)} onConfirm={() => void deleteAsset()}>
      <p><strong>{deleting?.name}</strong> will be permanently removed from your library.</p><p>Files used by a saved page or version are protected from deletion.</p>
    </Confirm>
  </>;
}

export default AssetLibrary;
