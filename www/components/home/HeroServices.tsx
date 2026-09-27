"use client";

import Link from "next/link";
import { useCallback, useEffect, useRef, useState } from "react";
import { useCmsCollection, useCmsString } from "@/components/cms/CmsProvider";
import { CmsMedia, CmsSection, CmsText } from "@/components/cms/Editable";
import { FeatureDiagram, type FeatureKind } from "./visuals/FeatureDiagram";

export const HERO_SERVICES: {
  id: string;
  title: string;
  lineOne: string;
  lineTwo: string;
  kind: FeatureKind;
  href: string;
}[] = [
  { id: "twin", title: "Isolated Twin", lineOne: "Your stack, isolated", lineTwo: "for each change.", kind: "twin", href: "/product/twins" },
  { id: "state", title: "Safe State", lineOne: "Masked Postgres.", lineTwo: "Relationships intact.", kind: "state", href: "/product/safe-state" },
  { id: "firewall", title: "Side-Effect Firewall", lineOne: "Test payments and mail.", lineTwo: "Contain external calls.", kind: "firewall", href: "/product/firewall" },
  { id: "workload", title: "Load", lineOne: "Your traffic mix.", lineTwo: "Against the new build.", kind: "workload", href: "/product/load" },
  { id: "migration", title: "Migration Safety", lineOne: "Locks, rewrites, plans.", lineTwo: "Before you deploy.", kind: "migration", href: "/product/migrations" },
];

const SERVICE_CONTENT = HERO_SERVICES.map(({ id, title, lineOne, lineTwo, href }) => ({ id, title, lineOne, lineTwo, href }));

function ServiceItem({ item }: { item: (typeof SERVICE_CONTENT)[number] }) {
  const key = `hero-services.items.${item.id}`;
  const source = HERO_SERVICES.find((service) => service.id === item.id);
  const href = useCmsString(`${key}.href`, source?.href ?? item.href ?? "/product/twins", {
    label: `${source?.title ?? item.title ?? "Feature"} link`, sectionId: "hero-services", kind: "url",
  });
  const kind = source?.kind;
  return (
    <li className="min-w-0 max-lg:w-[220px] max-lg:shrink-0 max-lg:snap-start">
      <Link
        prefetch={false}
        href={href}
        className="group block border-t border-stroke pt-4 focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-black"
      >
        <p className="min-h-[72px] text-base leading-6 tracking-extra-tight text-gray-new-40 [overflow-wrap:anywhere]">
          <CmsText cmsKey={`${key}.title`} label="Feature name" sectionId="hero-services" defaultValue={source?.title ?? item.title ?? ""} as="span" className="block font-medium text-gray-new-10 group-hover:underline group-hover:underline-offset-4" />
          <CmsText cmsKey={`${key}.lineOne`} label="First description line" sectionId="hero-services" defaultValue={source?.lineOne ?? item.lineOne ?? ""} as="span" className="block" />
          <CmsText cmsKey={`${key}.lineTwo`} label="Second description line" sectionId="hero-services" defaultValue={source?.lineTwo ?? item.lineTwo ?? ""} as="span" className="block" />
        </p>
        <div className="mt-4 h-[164px]">
          <CmsMedia cmsKey={`${key}.visual`} label={`${source?.title ?? item.title ?? "Feature"} illustration`} sectionId="hero-services">
            {kind ? <FeatureDiagram kind={kind} /> : null}
          </CmsMedia>
        </div>
      </Link>
    </li>
  );
}

export function HeroServices() {
  const services = useCmsCollection("hero-services.items", "Product features", "hero-services", SERVICE_CONTENT);
  const listLabel = useCmsString("hero-services.listLabel", "What a run does", { label: "Feature list accessible label", sectionId: "hero-services" });
  const previousLabel = useCmsString("hero-services.previousLabel", "Previous features", { label: "Previous button accessible label", sectionId: "hero-services" });
  const nextLabel = useCmsString("hero-services.nextLabel", "Next features", { label: "Next button accessible label", sectionId: "hero-services" });
  const track = useRef<HTMLUListElement>(null);
  const [edges, setEdges] = useState({ start: true, end: false });
  const readEdges = useCallback(() => {
    const element = track.current;
    if (!element) return;
    const start = element.scrollLeft < 2;
    const end = element.scrollLeft + element.clientWidth >= element.scrollWidth - 2;
    setEdges((previous) => previous.start === start && previous.end === end ? previous : { start, end });
  }, []);
  useEffect(() => {
    const element = track.current;
    if (!element) return;
    const observer = new ResizeObserver(readEdges);
    observer.observe(element);
    readEdges();
    return () => observer.disconnect();
  }, [readEdges, services.length]);
  const move = (direction: number) => {
    const element = track.current;
    const item = element?.firstElementChild;
    if (!element || !item) return;
    element.scrollBy({
      left: direction * (item.getBoundingClientRect().width + 20),
      behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth",
    });
  };
  return (
    <CmsSection sectionId="hero-services" label="Product overview" group="hero" as="div">
    <ul
      ref={track}
      onScroll={readEdges}
      aria-label={listLabel}
      className="grid grid-cols-5 gap-6 max-lg:-mx-8 max-lg:flex max-lg:snap-x max-lg:snap-mandatory max-lg:scroll-px-8 max-lg:gap-5 max-lg:overflow-x-auto max-lg:px-8 max-lg:pb-3 max-md:-mx-5 max-md:scroll-px-5 max-md:px-5"
    >
      {services.map((item) => <ServiceItem key={item.id} item={item} />)}
    </ul>
    <div className="mt-3 hidden items-center justify-between max-lg:flex">
      <CmsText cmsKey="hero-services.explore" label="Carousel hint" sectionId="hero-services" defaultValue={services.length === 5 ? "Explore all five" : "Explore features"} as="span" className="text-xs text-gray-new-40" />
      <div className="flex gap-2">
        <button type="button" onClick={() => move(-1)} disabled={edges.start} aria-label={previousLabel} className="flex size-11 items-center justify-center border border-stroke text-lg text-gray-new-10 hover:bg-sage focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-black disabled:cursor-not-allowed disabled:border-gray-new-90 disabled:text-gray-new-80 disabled:hover:bg-transparent">←</button>
        <button type="button" onClick={() => move(1)} disabled={edges.end} aria-label={nextLabel} className="flex size-11 items-center justify-center border border-stroke text-lg text-gray-new-10 hover:bg-sage focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-black disabled:cursor-not-allowed disabled:border-gray-new-90 disabled:text-gray-new-80 disabled:hover:bg-transparent">→</button>
      </div>
    </div>
    </CmsSection>
  );
}
