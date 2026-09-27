import { isAssetId, isSafeFieldKey, type StyleValues, type WebsiteDocument } from "@antifailure/website";

export const CMS_FONTS = [
  { key: "inter", label: "Inter", family: "Inter, Arial, sans-serif" },
  { key: "geist", label: "Geist", family: "Geist, Arial, sans-serif" },
  { key: "geist-mono", label: "Geist Mono", family: "'Geist Mono', monospace" },
  { key: "system-serif", label: "Editorial serif", family: "Georgia, 'Times New Roman', serif" },
  { key: "system-sans", label: "System sans", family: "Arial, Helvetica, sans-serif" },
  { key: "system-mono", label: "System monospace", family: "'Courier New', monospace" },
];

const pixelProperties = new Set(["fontSize", "letterSpacing", "paddingTop", "paddingRight", "paddingBottom", "paddingLeft", "marginTop", "marginBottom", "gap", "maxWidth", "minHeight", "width", "borderRadius"]);
const unitlessProperties = new Set(["fontWeight", "lineHeight", "opacity", "zIndex"]);
const name = (value: string) => value.replace(/[A-Z]/g, (letter) => `-${letter.toLowerCase()}`);

export function styleDeclarations(style: StyleValues): string {
  const declarations: string[] = [];
  for (const [key, value] of Object.entries(style)) {
    if (typeof value === "number" && Number.isFinite(value)) {
      if (pixelProperties.has(key)) declarations.push(`${name(key)}:${value}px!important`);
      if (unitlessProperties.has(key)) declarations.push(`${name(key)}:${value}!important`);
    }
  }
  if (style.fontFamily) {
    const bundled: Record<string, string> = { inter: "var(--font-inter),Arial,sans-serif", geist: "var(--font-geist-sans),Arial,sans-serif", "geist-mono": "var(--font-geist-mono),monospace" };
    const font = bundled[style.fontFamily] ?? CMS_FONTS.find((candidate) => candidate.key === style.fontFamily)?.family;
    if (font) declarations.push(`font-family:${font}!important`);
    else if (style.fontFamily.startsWith("asset:") && isAssetId(style.fontFamily.slice(6))) declarations.push(`font-family:"cms-${style.fontFamily.slice(6)}",sans-serif!important`);
  }
  for (const key of ["color", "backgroundColor"] as const) {
    const value = style[key];
    if (value && /^#[0-9a-f]{3}(?:[0-9a-f]{3})?(?:[0-9a-f]{2})?$/i.test(value)) declarations.push(`${name(key)}:${value}!important`);
  }
  if (style.textAlign && ["left", "right", "center", "justify"].includes(style.textAlign)) declarations.push(`text-align:${style.textAlign}!important`);
  if (style.position && ["relative", "absolute", "fixed"].includes(style.position)) declarations.push(`position:${style.position}!important`);
  if (style.x !== undefined || style.y !== undefined) declarations.push(`${style.position ? "" : "position:relative;"}left:${style.x ?? 0}px;top:${style.y ?? 0}px`);
  if (style.imageFit) declarations.push(`object-fit:${style.imageFit}!important`);
  if (style.focalX !== undefined || style.focalY !== undefined) declarations.push(`object-position:${style.focalX ?? 50}% ${style.focalY ?? 50}%!important`);
  if (style.layout === "stack") declarations.push("display:flex!important;flex-direction:column!important;align-items:stretch!important");
  if (style.layout === "center") declarations.push("text-align:center!important;justify-items:center!important");
  return declarations.join(";");
}

/** The three media queries do not overlap: a desktop edit never silently
 * overrides the authored phone layout. Missing values retain source CSS. */
export function cmsStyles(document: WebsiteDocument, mediaUrl: (id: string) => string): string {
  const rules: string[] = [".cms-rich p+p{margin-top:1em}.cms-rich ul,.cms-rich ol{padding-left:1.5em}.cms-rich ul{list-style:disc}.cms-rich ol{list-style:decimal}"];
  const fonts = new Set<string>();
  for (const [key, responsive] of Object.entries(document.styles)) {
    if (!isSafeFieldKey(key)) continue;
    const fieldSelector = `[data-cms-key=${JSON.stringify(key)}]:not([data-cms-action] *),[data-cms-field=${JSON.stringify(key)}]:not([data-cms-action] *)`;
    const selector = key === "global" ? "body" : `${fieldSelector},[data-cms-section=${JSON.stringify(key)}],[data-cms-action=${JSON.stringify(key)}]`;
    for (const breakpoint of ["desktop", "tablet", "mobile"] as const) {
      const style = responsive[breakpoint];
      if (!style) continue;
      const media = breakpoint === "desktop" ? "(min-width:1024px)" : breakpoint === "tablet" ? "(min-width:768px) and (max-width:1023px)" : "(max-width:767px)";
      let declarations = styleDeclarations(style);
      if (style.minHeight !== undefined) declarations += `;--cms-embed-height:${style.minHeight}px`;
      // Hero height and the film's vertical anchor share these variables.
      if (key === "hero" && style.paddingTop !== undefined) declarations += `;--hero-top:${style.paddingTop}px;--hero-shift:${style.paddingTop - 384}px;padding-top:0!important`;
      rules.push(`@media ${media}{${selector}{${declarations}}}`);
      if (key === "global") {
        const text = styleDeclarations({ color: style.color, fontFamily: style.fontFamily });
        rules.push(`@media ${media}{:where(main [data-cms-key]:not([data-cms-section="cta"] *):not([data-cms-action] *):not([data-cms-media-slot]),main [data-cms-key]:not([data-cms-section="cta"] *)>strong){${text}}}`);
      } else {
        const text = styleDeclarations({ color: style.color, fontFamily: style.fontFamily, fontSize: style.fontSize, fontWeight: style.fontWeight, lineHeight: style.lineHeight, letterSpacing: style.letterSpacing, textAlign: style.textAlign });
        rules.push(`@media ${media}{[data-cms-section=${JSON.stringify(key)}] :where([data-cms-key]:not([data-cms-action] *):not([data-cms-media-slot]),[data-cms-key]>strong){${text}}}`);
      }
      if (key === "header" || key === "footer") {
        const text = styleDeclarations({ color: style.color, fontFamily: style.fontFamily, fontSize: style.fontSize });
        rules.push(`@media ${media}{:where([data-cms-section=${JSON.stringify(key)}] [data-cms-key]:not([data-cms-action] *)){${text}}}`);
        if (key === "header" && style.backgroundColor) rules.push(`@media ${media}{[data-cms-section="header"] .header,[data-cms-section="header"] #mobile-menu,[data-cms-section="header"] [id^="submenu-"]{${styleDeclarations({ backgroundColor: style.backgroundColor })}}}`);
      }
      if (style.imageFit || style.focalX !== undefined || style.focalY !== undefined || style.borderRadius !== undefined) {
        const mediaSelector = selector.split(",").flatMap((part) => [`${part}>img`, `${part}>video`]).join(",");
        rules.push(`@media ${media}{${mediaSelector}{${styleDeclarations({ imageFit: style.imageFit, focalX: style.focalX, focalY: style.focalY, borderRadius: style.borderRadius })}${style.imageFit === "cover" ? `;height:${style.minHeight ? `${style.minHeight}px` : "100%"};min-height:inherit` : ""}}}`);
      }
      if (key === "hero" && style.paddingTop !== undefined) rules.push(`@media ${media}{[data-cms-section="hero"]>div:first-child{padding-top:${style.paddingTop}px!important}}`);
      if (key === "hero" && style.paddingTop !== undefined) {
        const offsetMedia = breakpoint === "desktop" ? "(min-width:1024px) and (max-width:1279px)" : media;
        const authoredTop = breakpoint === "desktop" ? 216 : breakpoint === "tablet" ? 208 : 212;
        rules.push(`@media ${offsetMedia}{[data-cms-section="hero"]{--cms-hero-offset:${style.paddingTop - authoredTop}px}}`);
      }
      const sectionSelector = `[data-cms-section=${JSON.stringify(key)}]`;
      if (style.layout === "stack") rules.push(`@media ${media}{${sectionSelector} [data-cms-layout]{display:grid!important;grid-template-columns:minmax(0,1fr)!important}}`);
      if (style.layout === "media-left" || style.layout === "media-right") rules.push(`@media ${media}{${sectionSelector} [data-cms-layout]{display:grid!important;grid-template-columns:minmax(0,1fr) minmax(0,1fr)!important}${sectionSelector} [data-cms-layout]>[data-cms-media-slot]{order:${style.layout === "media-left" ? "-1" : "1"}}}`);
      if (style.fontFamily?.startsWith("asset:") && isAssetId(style.fontFamily.slice(6))) fonts.add(style.fontFamily.slice(6));
    }
  }
  for (const id of fonts) rules.unshift(`@font-face{font-family:"cms-${id}";src:url(${JSON.stringify(mediaUrl(id))}) format("woff2");font-display:swap;font-weight:100 900}`);
  return rules.join("\n");
}
