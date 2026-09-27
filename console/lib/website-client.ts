import {
  resolveOrder, stableStringify, type WebsiteDocument, type WebsiteManifest,
  type CustomSectionKind, type CustomSectionGroup, type SectionDefinition, type FieldValue,
} from "@antifailure/website";

export interface WebsiteState {
  document: WebsiteDocument;
  draftRevision: number;
  publishedRevision: number;
  updatedAt: string;
  updatedBy: string | null;
  warnings?: Array<{ path: string; message: string }>;
  refresh: {
    revision: number;
    status: "queued" | "dispatching" | "waiting" | "deployed" | "failed" | "superseded";
    attempts: number;
    nextAttemptAt: string;
    lastError: string | null;
    deployedAt: string | null;
  } | null;
}

export interface WebsiteRevision {
  revision: number;
  createdAt: string;
  createdBy: string;
  sourceVersion: string | null;
}

export const SECTION_PRESETS: Array<{ kind: CustomSectionKind; label: string; description: string }> = [
  { kind: "text", label: "Text", description: "A heading and a story worth reading." },
  { kind: "image", label: "Image", description: "A full-width visual with a caption." },
  { kind: "video", label: "Video", description: "A product walkthrough or recorded demo." },
  { kind: "split", label: "Image + text", description: "Put the explanation beside the product." },
  { kind: "features", label: "Features", description: "A clear group of related capabilities." },
  { kind: "cta", label: "Call to action", description: "A focused invitation to take the next step." },
  { kind: "shape", label: "Shape", description: "A circle, arch, ring or geometric accent." },
  { kind: "divider", label: "Divider", description: "A line, wave or quiet break between sections." },
  { kind: "embed", label: "Custom code", description: "Your own HTML, CSS and JavaScript." },
  { kind: "spacer", label: "Space", description: "Give the next section room to breathe." },
];

function contentLabel(value: FieldValue | undefined): string {
  if (typeof value === "string") return value;
  if (value && typeof value === "object" && value.type === "doc") {
    const text = (node: unknown): string => {
      if (!node || typeof node !== "object") return "";
      if ("text" in node && typeof node.text === "string") return node.text;
      if ("content" in node && Array.isArray(node.content)) return node.content.map(text).join(" ");
      return "";
    };
    return text(value).trim();
  }
  return "";
}

export function pageSections(document: WebsiteDocument, manifest: WebsiteManifest, group: CustomSectionGroup): SectionDefinition[] {
  const defaults = manifest.sections.filter((item) => item.group === group && !item.id.startsWith("custom-"));
  const custom = document.sections.custom.filter((item) => item.group === group);
  const items = new Map<string, SectionDefinition>([
    ...defaults.map((item) => [item.id, item] as const),
    ...custom.map((item) => [item.id, {
      ...item, label: contentLabel(document.fields[`${item.id}.heading`]) || SECTION_PRESETS.find((preset) => preset.kind === item.kind)?.label || "Custom section",
    }] as const),
  ]);
  const moves = [
    ...custom.map(({ id, after }) => ({ id, after })),
    ...document.sections.moves.filter((move) => !move.group || move.group === group),
  ];
  return resolveOrder(defaults.map((item) => item.id), moves, custom.map((item) => item.id))
    .map((id) => items.get(id)!).filter(Boolean);
}

/** Persist only adjacency changes. New source sections still join the page. */
export function moveSection(document: WebsiteDocument, group: CustomSectionGroup, currentIds: string[], id: string, to: number): WebsiteDocument {
  const from = currentIds.indexOf(id);
  if (from < 0 || to < 0 || to >= currentIds.length || from === to) return document;
  const ids = [...currentIds];
  ids.splice(from, 1);
  ids.splice(to, 0, id);
  const affected = new Set([id, currentIds[from + 1], ids[to + 1]].filter(Boolean));
  const moves = document.sections.moves.filter((move) => !affected.has(move.id));
  for (const affectedId of affected) {
    const index = ids.indexOf(affectedId);
    if (index >= 0) moves.push({ id: affectedId, after: ids[index - 1] ?? null, group });
  }
  return { ...document, sections: { ...document.sections, moves } };
}

export function addSection(document: WebsiteDocument, kind: CustomSectionKind, group: CustomSectionGroup, after: string | null, id: string): WebsiteDocument {
  const fields: Record<string, FieldValue> = { ...document.fields };
  if (["text", "split", "features", "cta"].includes(kind)) fields[`${id}.heading`] = kind === "cta" ? "See Antifailure in action." : "Your next change, tested.";
  if (["text", "split", "features", "cta"].includes(kind)) fields[`${id}.body`] = { type: "doc", content: [{ type: "paragraph", content: [{ type: "text", text: "Rehearse your change on an isolated production twin before you deploy." }] }] };
  if (kind === "cta") {
    fields[`${id}.buttonLabel`] = "Request a demo";
    fields[`${id}.buttonHref`] = "/request-demo";
  }
  return { ...document, fields, sections: { ...document.sections, custom: [...document.sections.custom, { id, kind, group, after }] } };
}

export function duplicateSection(document: WebsiteDocument, id: string, copyId: string): WebsiteDocument {
  const source = document.sections.custom.find((item) => item.id === id);
  if (!source) return document;
  const fields = { ...document.fields };
  const styles = { ...document.styles };
  const collections = { ...document.collections };
  for (const [key, value] of Object.entries(document.fields)) if (key.startsWith(`${id}.`)) fields[`${copyId}${key.slice(id.length)}`] = structuredClone(value);
  for (const [key, value] of Object.entries(document.styles)) if (key === id || key.startsWith(`${id}.`)) styles[`${copyId}${key.slice(id.length)}`] = structuredClone(value);
  for (const [key, value] of Object.entries(document.collections)) if (key === id || key.startsWith(`${id}.`)) collections[`${copyId}${key.slice(id.length)}`] = structuredClone(value);
  return { ...document, fields, styles, collections, sections: { ...document.sections, custom: [...document.sections.custom, { ...source, id: copyId, after: id }] } };
}

export function removeCustomSection(document: WebsiteDocument, id: string): WebsiteDocument {
  const without = <T>(values: Record<string, T>) => Object.fromEntries(Object.entries(values).filter(([key]) => key !== id && !key.startsWith(`${id}.`)));
  return {
    ...document, fields: without(document.fields), styles: without(document.styles),
    collections: without(document.collections),
    sections: {
      custom: document.sections.custom.filter((item) => item.id !== id).map((item) => item.after === id ? { ...item, after: null } : item),
      hidden: document.sections.hidden.filter((item) => item !== id),
      moves: document.sections.moves.filter((item) => item.id !== id && item.after !== id),
    },
  };
}

export function resetSection(document: WebsiteDocument, sectionId: string, manifest: WebsiteManifest): WebsiteDocument {
  const fields = { ...document.fields };
  const styles = { ...document.styles };
  const collections = { ...document.collections };
  delete styles[sectionId];
  for (const field of manifest.fields.filter((item) => item.sectionId === sectionId)) { delete fields[field.key]; delete styles[field.key]; }
  for (const collection of manifest.collections.filter((item) => item.sectionId === sectionId)) {
    delete collections[collection.key];
    for (const key of Object.keys(fields)) if (key.startsWith(`${collection.key}.`)) delete fields[key];
  }
  return { ...document, fields, styles, collections, sections: { ...document.sections, hidden: document.sections.hidden.filter((id) => id !== sectionId) } };
}

export function changedDocument(a: WebsiteDocument, b: WebsiteDocument): boolean { return stableStringify(a) !== stableStringify(b); }
