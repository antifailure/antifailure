"use client";

import { useEffect, useRef } from "react";
import { usePathname } from "next/navigation";
import { resolveField, safeBuiltinSource, type FieldDefinition, type MediaReference } from "@antifailure/website";
import { useCms } from "./CmsProvider";

const BLOCKED = "script,style,noscript,pre,code,textarea,input,select,option,svg,[aria-hidden='true'],[contenteditable],[data-cms-key]:not([data-cms-sitewide-key]),[data-cms-field],[data-cms-action],[data-cms-section^='custom-']";

function hash(value: string): string {
  let first = 2166136261;
  let second = 1831565813;
  for (let index = 0; index < value.length; index += 1) {
    first = Math.imul(first ^ value.charCodeAt(index), 16777619);
    second = Math.imul(second ^ value.charCodeAt(index), 2246822519);
  }
  return `${(first >>> 0).toString(36)}${(second >>> 0).toString(36)}`;
}

function pathOf(node: Text | Element, root: Element): string {
  const parts: string[] = [];
  let element: Element | null = node instanceof Element ? node : node.parentElement;
  while (element && element !== root) {
    const siblings = element.parentElement ? [...element.parentElement.children].filter((child) => child.tagName === element!.tagName) : [];
    parts.unshift(`${element.tagName.toLowerCase()}${siblings.indexOf(element)}`);
    element = element.parentElement;
  }
  if (node instanceof Text) parts.push(`t${[...node.parentNode!.childNodes].filter((child) => child.nodeType === Node.TEXT_NODE).indexOf(node)}`);
  return parts.join("/");
}

/** Covers source-owned inner-page prose without copying it into the CMS.
 * Keys follow a DOM position, so untouched source copy keeps moving with code.
 * The editor exposes the current source value and flags orphaned overrides. */
export function SitewideContentBridge() {
  const pathname = usePathname();
  const cms = useCms();
  const originals = useRef(new Map<string, { node: Text; value: string; prefix: string; suffix: string }>());
  const images = useRef(new Map<string, { element: HTMLImageElement; src: string; srcSet: string | null; alt: string; display: string; displayPriority: string; hidden: boolean }>());
  const page = pathname === "/cms-preview" ? "home" : pathname.replace(/^\/+|\/+$/g, "").replace(/[^a-zA-Z0-9_-]+/g, "-") || "home";
  const sectionId = `page-${page}`;
  useEffect(() => {
    const seoSection = pathname === "/" || pathname === "/cms-preview" ? "hero" : sectionId;
    const sourceTitle = window.document.querySelector<HTMLMetaElement>('meta[name="af-cms-source-title"]')?.content ?? window.document.title;
    const sourceDescription = window.document.querySelector<HTMLMetaElement>('meta[name="af-cms-source-description"]')?.content ?? window.document.querySelector<HTMLMetaElement>('meta[name="description"]')?.content ?? "";
    const seoFields: FieldDefinition[] = [
      { key: `seo.${page}.title`, label: "Search and social title", kind: "text", sectionId: seoSection, defaultValue: sourceTitle },
      { key: `seo.${page}.description`, label: "Search and social description", kind: "text", sectionId: seoSection, defaultValue: sourceDescription },
    ];
    cms.register(seoFields);
    const title = resolveField(cms.document, `seo.${page}.title`, sourceTitle);
    const description = resolveField(cms.document, `seo.${page}.description`, sourceDescription);
    if (typeof title === "string" && title && window.document.title !== title) window.document.title = title;
    for (const selector of ['meta[name="description"]', 'meta[property="og:description"]', 'meta[name="twitter:description"]']) {
      const tag = window.document.querySelector<HTMLMetaElement>(selector);
      if (tag && typeof description === "string") tag.content = description;
    }
    for (const selector of ['meta[property="og:title"]', 'meta[name="twitter:title"]']) {
      const tag = window.document.querySelector<HTMLMetaElement>(selector);
      if (tag && typeof title === "string") tag.content = title;
    }
    if (pathname === "/" || pathname === "/cms-preview") return;
    const root = window.document.querySelector("main");
    if (!root) return;
    root.setAttribute("data-cms-section", sectionId);
    const fields: FieldDefinition[] = [];
    const walker = window.document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    let current: Node | null;
    while ((current = walker.nextNode())) {
      const node = current as Text;
      const parent = node.parentElement;
      if (!parent || parent.closest(BLOCKED)) continue;
      const source = node.textContent ?? "";
      const value = source.trim();
      if (value.length < 2 || value.length > 3000 || !/[a-zA-Z0-9]/.test(value)) continue;
      const key = `page.${page}.text.h${hash(pathOf(node, root))}`;
      let original = originals.current.get(key);
      if (!original || original.node !== node) {
        const start = source.indexOf(value);
        original = { node, value, prefix: source.slice(0, start), suffix: source.slice(start + value.length) };
        originals.current.set(key, original);
      }
      const label = `${parent.tagName.toLowerCase()} · ${original.value.slice(0, 72)}`;
      fields.push({ key, label, kind: "text", sectionId, defaultValue: original.value });
      if (!parent.hasAttribute("data-cms-key")) {
        parent.setAttribute("data-cms-key", key);
        parent.setAttribute("data-cms-sitewide-key", "true");
      }
      const resolved = resolveField(cms.document, key, original.value);
      const next = `${original.prefix}${typeof resolved === "string" ? resolved : original.value}${original.suffix}`;
      if (node.textContent !== next) node.textContent = next;
    }
    for (const element of root.querySelectorAll<HTMLImageElement>("img")) {
      if (element.closest("[data-cms-key]:not([data-cms-sitewide-key]),[aria-hidden='true']")) continue;
      const key = `page.${page}.image.h${hash(pathOf(element, root))}`;
      let original = images.current.get(key);
      if (!original || original.element !== element) {
        const src = element.getAttribute("src") ?? "";
        if (!safeBuiltinSource(src)) continue;
        original = { element, src, srcSet: element.getAttribute("srcset"), alt: element.getAttribute("alt") ?? "", display: element.style.getPropertyValue("display"), displayPriority: element.style.getPropertyPriority("display"), hidden: element.hidden };
        images.current.set(key, original);
      }
      const defaultValue: MediaReference = { type: "media", source: "builtin", src: original.src, kind: "image", alt: original.alt };
      fields.push({ key, label: `Image · ${original.alt || page}`, kind: "media", sectionId, defaultValue });
      element.setAttribute("data-cms-key", key);
      element.setAttribute("data-cms-sitewide-key", "true");
      const resolved = resolveField(cms.document, key, defaultValue);
      if (resolved === null) { element.hidden = true; element.style.setProperty("display", "none", "important"); }
      else if (resolved && typeof resolved === "object" && resolved.type === "media") {
        element.hidden = original.hidden;
        if (original.display) element.style.setProperty("display", original.display, original.displayPriority);
        else element.style.removeProperty("display");
        element.src = resolved.source === "asset" ? cms.mediaUrl(resolved.assetId) : resolved.src;
        if (resolved.source === "asset" || resolved.src !== original.src) element.removeAttribute("srcset");
        else if (original.srcSet) element.setAttribute("srcset", original.srcSet);
        element.alt = resolved.alt ?? original.alt;
      }
    }
    cms.register(fields, [{ id: sectionId, label: pathname.replace(/^\//, "") || "Page", group: "page" }]);
  }, [cms.document, cms.mediaUrl, cms.register, pathname, page, sectionId]);
  return null;
}
