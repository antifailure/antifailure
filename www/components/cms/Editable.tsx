"use client";

import { createElement, forwardRef, useEffect, useRef, useState, type HTMLAttributes, type ReactNode } from "react";
import type { MediaReference, RichTextDocument, SectionGroup } from "@antifailure/website";
import { useCms, useCmsField, useCmsMedia } from "./CmsProvider";
import { RichText } from "@/lib/cms/richtext";

type CmsTextProps = {
  cmsKey: string; label: string; sectionId: string; defaultValue: string | RichTextDocument;
  as?: "span" | "p" | "h1" | "h2" | "h3" | "h4" | "div" | "strong"; className?: string; children?: ReactNode;
};
export function CmsText({ cmsKey, label, sectionId, defaultValue, as = "span", className, children }: CmsTextProps) {
  const cms = useCms();
  const value = useCmsField({ key: cmsKey, label, sectionId, defaultValue, kind: typeof defaultValue === "string" ? "text" : "richtext" });
  const [inline, setInline] = useState(false);
  const element = useRef<HTMLElement | null>(null);
  const canType = cms.editing && typeof value === "string";
  const start = () => { if (canType) setInline(true); };
  useEffect(() => { if (inline) element.current?.focus(); }, [inline]);
  useEffect(() => { if (!cms.editing) setInline(false); }, [cms.editing]);
  const finish = () => {
    if (!inline) return;
    const next = element.current?.innerText ?? "";
    setInline(false);
    if (next !== value) cms.edit(cmsKey, next);
  };
  const overridden = Object.hasOwn(cms.document.fields, cmsKey);
  const content = !overridden && children !== undefined ? children : value && typeof value === "object" && value.type === "doc" ? <RichText value={value} inline={as !== "div"} /> : typeof value === "string" ? value : "";
  return createElement(as, {
    ref: element, className, "data-cms-key": cmsKey,
    ...(cms.editing ? { tabIndex: 0, "aria-description": `Edit ${label}. Press Enter to select.`, title: typeof value === "string" ? `Double-click to edit ${label}` : `Edit ${label}`, ...(inline ? { role: "textbox", "aria-label": `Edit ${label}`, "aria-multiline": true } : {}) } : {}),
    contentEditable: inline || undefined, suppressContentEditableWarning: inline,
    onDoubleClick: start,
    onBlur: finish,
    onKeyDown: (event: React.KeyboardEvent<HTMLElement>) => {
      if (!cms.editing) return;
      if (event.key === "Enter" && !inline) { event.preventDefault(); cms.select({ key: cmsKey, sectionId }); start(); }
      else if (event.key === "Escape" && inline) { event.preventDefault(); if (element.current) element.current.innerText = String(value); setInline(false); }
      else if ((event.metaKey || event.ctrlKey) && event.key === "Enter") { event.preventDefault(); finish(); }
    },
    onPaste: (event: React.ClipboardEvent<HTMLElement>) => {
      if (!inline) return;
      event.preventDefault();
      const selection = window.getSelection();
      if (!selection?.rangeCount) return;
      const range = selection.getRangeAt(0);
      range.deleteContents();
      const text = window.document.createTextNode(event.clipboardData.getData("text/plain"));
      range.insertNode(text); range.setStartAfter(text); range.collapse(true); selection.removeAllRanges(); selection.addRange(range);
    },
  }, inline ? String(value) : content);
}

interface CmsSectionProps extends HTMLAttributes<HTMLElement> {
  sectionId: string; label: string; group: SectionGroup; as?: "section" | "div" | "header" | "footer";
}
export const CmsSection = forwardRef<HTMLElement, CmsSectionProps>(function CmsSection({ sectionId, label, group, as = "section", children, ...props }, ref) {
  const cms = useCms();
  useEffect(() => { cms.register([], [{ id: sectionId, label, group }]); }, [cms.register, sectionId, label, group]);
  const hidden = cms.document.sections.hidden.includes(sectionId);
  if (hidden && !cms.isPreview) return null;
  return createElement(as, { ...props, ref, "data-cms-section": sectionId, hidden: hidden || props.hidden }, children);
});

export function CmsMedia({ cmsKey, label, sectionId, defaultValue = null, children, className, mediaType }: {
  cmsKey: string; label: string; sectionId: string; defaultValue?: MediaReference | null; children?: ReactNode; className?: string; mediaType?: "image" | "video";
}) {
  const cms = useCms();
  const media = useCmsMedia(cmsKey, defaultValue, { label, sectionId });
  const raw = cms.document.fields[cmsKey];
  const replaced = Object.hasOwn(cms.document.fields, cmsKey) && (raw === null || (typeof raw === "object" && raw.type === "media"));
  if (!replaced && children !== undefined) return <div data-cms-key={cmsKey} data-cms-media-slot="true" style={cms.document.styles[cmsKey] ? undefined : { display: "contents" }} className={className}>{children}</div>;
  if (!media.url) return cms.isPreview ? <div data-cms-key={cmsKey} data-cms-media-slot="true" data-cms-placeholder>{label}. Select an image or video in the editor.</div> : null;
  const video = media.value?.kind ? media.value.kind === "video" : mediaType === "video" || /\.(mp4|webm)(\?|$)/i.test(media.url);
  return <div data-cms-key={cmsKey} data-cms-media-slot="true" className={className}>
    {video ? <video src={media.url} controls playsInline preload="metadata" className="block h-auto w-full" aria-label={media.alt || label} /> : <img src={media.url} alt={media.alt} loading="lazy" className="block h-auto w-full" />}
  </div>;
}
