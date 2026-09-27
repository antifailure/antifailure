"use client";

import { Fragment, type ReactNode } from "react";
import { Cta } from "@/components/home/Cta";
import { Features } from "@/components/home/Features";
import { Firewall } from "@/components/home/Firewall";
import { Hero } from "@/components/home/Hero";
import { Migrations } from "@/components/home/Migrations";
import { TocWrapper } from "@/components/home/Toc";
import { Trust } from "@/components/home/Trust";
import { Twins } from "@/components/home/Twins";
import { Workload } from "@/components/home/Workload";
import { SiteLayout } from "@/components/layout/SiteLayout";
import { useOrderedSections, type SourceSection } from "./SectionGroup";
import { useCms } from "./CmsProvider";

const SOURCE_SECTIONS: SourceSection[] = [
  { id: "hero", label: "Hero", group: "page", content: <Hero /> },
  { id: "migrations", label: "Migration Safety", group: "page", inToc: true, content: <Migrations /> },
  { id: "twins", label: "Isolated Twin", group: "page", inToc: true, content: <Twins /> },
  { id: "features", label: "Safety properties", group: "page", inToc: true, content: <Features /> },
  { id: "workload", label: "Load", group: "page", inToc: true, content: <Workload /> },
  { id: "firewall", label: "Side-Effect Firewall", group: "page", inToc: true, content: <Firewall /> },
  { id: "trust", label: "Data boundary", group: "page", content: <Trust /> },
  { id: "cta", label: "Closing call to action", group: "page", content: <Cta /> },
];

export function HomePage() {
  const cms = useCms();
  const { sections, hidden } = useOrderedSections("page", SOURCE_SECTIONS);
  const output: ReactNode[] = [];
  let index = 0;
  while (index < sections.length) {
    const current = sections[index];
    if (!current.inToc) { output.push(<Fragment key={current.id}>{current.content}</Fragment>); index++; continue; }
    const run: SourceSection[] = [];
    while (index < sections.length && sections[index].inToc) { run.push(sections[index]); index++; }
    const links = run.map((section) => {
      const label = cms.document.fields[`${section.id}.heading.label`];
      return { id: section.id, title: typeof label === "string" ? label : section.label };
    });
    output.push(<TocWrapper key={`toc-${run[0].id}`} sections={links}>{run.map((section) => <Fragment key={section.id}>{section.content}</Fragment>)}</TocWrapper>);
  }
  return <SiteLayout overlay={sections[0]?.id === "hero"}>{output}{hidden.map((section) => <div key={section.id} hidden>{section.content}</div>)}</SiteLayout>;
}
