"use client";

import { useEffect, type ReactNode } from "react";
import { resolveOrder, type SectionDefinition } from "@antifailure/website";
import { useCms } from "./CmsProvider";
import { CustomSection } from "./CustomSection";

export interface SourceSection extends SectionDefinition { content: ReactNode }

export function useOrderedSections(group: "page" | "hero", source: readonly SourceSection[]) {
  const cms = useCms();
  const definitions = source.map(({ content: _content, ...definition }) => definition);
  const signature = JSON.stringify(definitions);
  useEffect(() => { cms.register([], definitions); }, [cms.register, signature]); // eslint-disable-line react-hooks/exhaustive-deps
  const custom = cms.document.sections.custom.filter((section) => section.group === group);
  const moves = [...custom.map(({ id, after }) => ({ id, after })), ...cms.document.sections.moves.filter((move) => !move.group || move.group === group)];
  const order = resolveOrder(source.map((section) => section.id), moves, custom.map((section) => section.id), cms.document.sections.hidden);
  const entries = new Map(source.map((section) => [section.id, section]));
  for (const section of custom) entries.set(section.id, { ...section, label: `${section.kind} block`, content: <CustomSection section={section} /> });
  return {
    sections: order.flatMap((id) => entries.has(id) ? [entries.get(id)!] : []),
    // A hidden source section still registers its fresh defaults with the
    // editor. Published HTML omits it entirely.
    hidden: cms.isPreview ? [...entries.values()].filter((section) => cms.document.sections.hidden.includes(section.id)) : [],
  };
}

export function SectionGroup({ group, source }: { group: "page" | "hero"; source: readonly SourceSection[] }) {
  const { sections, hidden } = useOrderedSections(group, source);
  return <>{sections.map((section) => <div key={section.id} style={{ display: "contents" }}>{section.content}</div>)}{hidden.map((section) => <div key={section.id} hidden>{section.content}</div>)}</>;
}
