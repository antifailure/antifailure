"use client";

import {
  CUSTOM_SHAPES, DIVIDER_VARIANTS, resolveCustomShape, resolveDividerVariant,
  type CustomSection as CustomSectionDefinition, type RichTextDocument,
} from "@antifailure/website";
import { CmsMedia, CmsSection, CmsText } from "./Editable";
import { useCmsCollection, useCmsField, useCmsString } from "./CmsProvider";
import { Container } from "@/components/layout/Container";
import { Button } from "@/components/layout/Button";
import { EmbedSection } from "./EmbedSection";

const paragraph = (text: string): RichTextDocument => ({ type: "doc", content: [{ type: "paragraph", content: [{ type: "text", text }] }] });
const FEATURE_DEFAULTS = [
  { id: "rehearse", title: "Run the change", body: "Test on an isolated copy of your application." },
  { id: "inspect", title: "Review the findings", body: "See what changed before it reaches production." },
  { id: "decide", title: "Decide what to ship", body: "Give your team the evidence to make the call." },
];

export function CustomSection({ section }: { section: CustomSectionDefinition }) {
  if (section.kind === "embed") return <EmbedSection section={section} />;
  if (section.kind === "shape") return <ShapeSection section={section} />;
  if (section.kind === "divider") return <DividerSection section={section} />;
  return <ContentSection section={section} />;
}

function ShapeSection({ section }: { section: CustomSectionDefinition }) {
  const { id, group } = section;
  const shape = resolveCustomShape(useCmsField({
    key: `${id}.shape`, label: "Shape", sectionId: id, kind: "select", defaultValue: "circle",
    options: CUSTOM_SHAPES.map((value) => ({ value, label: value[0].toUpperCase() + value.slice(1) })),
  }));
  const label = useCmsString(`${id}.label`, "", { label: "Shape caption", sectionId: id });
  return <CmsSection sectionId={id} label="Shape block" group={group} id={id} className="safe-paddings py-12 max-md:py-8">
    <Container size="1344">
      <div data-cms-key={`${id}.shape`} className="mx-auto w-full max-w-[240px] max-md:max-w-[180px]">
        <svg viewBox="0 0 240 240" className="block h-auto w-full" fill="currentColor" aria-hidden="true" focusable="false">
          {shape === "circle" && <circle cx="120" cy="120" r="112" />}
          {shape === "rectangle" && <rect x="8" y="40" width="224" height="160" rx="4" />}
          {shape === "arch" && <path d="M24 224V112a96 96 0 0 1 192 0v112Z" />}
          {shape === "triangle" && <path d="M120 12 232 220H8Z" />}
          {shape === "ring" && <circle cx="120" cy="120" r="96" fill="none" stroke="currentColor" strokeWidth="32" />}
        </svg>
      </div>
      <CmsText cmsKey={`${id}.label`} label="Shape caption" sectionId={id} defaultValue="" as="p" className={`${label ? "mt-5 " : ""}text-center text-base leading-7`} />
    </Container>
  </CmsSection>;
}

const WAVE_PATH = "M0 16" + Array.from({ length: 20 }, () => "q15 -20 30 0t30 0").join("");

function DividerSection({ section }: { section: CustomSectionDefinition }) {
  const { id, group } = section;
  const variant = resolveDividerVariant(useCmsField({
    key: `${id}.variant`, label: "Divider style", sectionId: id, kind: "select", defaultValue: "line",
    options: DIVIDER_VARIANTS.map((value) => ({ value, label: value[0].toUpperCase() + value.slice(1) })),
  }));
  const label = useCmsString(`${id}.label`, "", { label: "Divider caption", sectionId: id });
  return <CmsSection sectionId={id} label="Divider block" group={group} id={id} className="safe-paddings py-7 max-md:py-5">
    <Container size="1344">
      <div data-cms-key={`${id}.variant`} role="separator" aria-orientation="horizontal">
        <svg viewBox="0 0 1200 32" preserveAspectRatio="none" className="block h-8 w-full" fill="none" stroke="currentColor" aria-hidden="true" focusable="false">
          {variant === "line" && <path d="M0 16H1200" strokeWidth="1" vectorEffect="non-scaling-stroke" />}
          {variant === "dashed" && <path d="M0 16H1200" strokeWidth="1" strokeDasharray="12 10" vectorEffect="non-scaling-stroke" />}
          {variant === "dots" && <path d="M2 16H1198" strokeWidth="3" strokeDasharray="0 12" strokeLinecap="round" vectorEffect="non-scaling-stroke" />}
          {variant === "wave" && <path d={WAVE_PATH} strokeWidth="1.5" vectorEffect="non-scaling-stroke" />}
        </svg>
      </div>
      <CmsText cmsKey={`${id}.label`} label="Divider caption" sectionId={id} defaultValue="" as="p" className={`${label ? "mt-3 " : ""}text-center text-base leading-7`} />
    </Container>
  </CmsSection>;
}

function ContentSection({ section }: { section: CustomSectionDefinition }) {
  const { id, kind, group } = section;
  const heading = kind === "cta" ? "See your next change in Antifailure." : kind === "features" ? "From a change to a decision." : "Test the change before you ship.";
  const body = paragraph("Give your team a production twin to test the change, inspect the results, and decide what to deploy.");
  return <CmsSection sectionId={id} label={`${kind[0].toUpperCase()}${kind.slice(1)} block`} group={group} id={id} className={kind === "spacer" ? "h-16 max-md:h-8" : "safe-paddings py-20 max-md:py-12"}>
    {kind !== "spacer" && <Container size="1344">
      {(kind === "image" || kind === "video") ? <CustomMedia id={id} kind={kind} /> : kind === "split" ? <div data-cms-layout="true" className="grid grid-cols-2 items-center gap-16 max-md:grid-cols-1 max-md:gap-8">
        <div><CustomHeading id={id} defaultValue={heading} className="max-w-[22ch] text-[44px] leading-dense tracking-tighter max-md:text-[30px]" /><CmsText cmsKey={`${id}.body`} label="Body" sectionId={id} defaultValue={body} as="div" className="cms-rich mt-6 max-w-[60ch] text-lg leading-8 text-gray-new-40" /><CustomAction id={id} /></div><CustomMedia id={id} kind="image" />
      </div> : <>
        <CustomHeading id={id} defaultValue={heading} className="max-w-[26ch] text-[48px] leading-dense tracking-tighter max-md:text-[32px]" />
        <CmsText cmsKey={`${id}.body`} label="Body" sectionId={id} defaultValue={body} as="div" className="cms-rich mt-6 max-w-[65ch] text-lg leading-8 text-gray-new-40" />
        {kind === "features" && <FeatureList id={id} />}
        {kind === "cta" && <CustomAction id={id} />}
      </>}
    </Container>}
  </CmsSection>;
}

function CustomHeading({ id, defaultValue, className }: { id: string; defaultValue: string; className: string }) {
  const level = useCmsField({
    key: `${id}.headingLevel`, label: "Heading level", sectionId: id, kind: "select", defaultValue: "h2",
    options: [{ value: "h1", label: "H1, page title" }, { value: "h2", label: "H2, section title" }, { value: "h3", label: "H3, subsection title" }],
  });
  const tag = level === "h1" || level === "h3" ? level : "h2";
  return <CmsText cmsKey={`${id}.heading`} label="Heading" sectionId={id} defaultValue={defaultValue} as={tag} className={className} />;
}

function CustomAction({ id }: { id: string }) {
  const href = useCmsString(`${id}.buttonHref`, "/request-demo", { label: "Button destination", sectionId: id, kind: "url" });
  return <Button href={href} cmsKey={`${id}.buttonLabel`} className="mt-8"><CmsText cmsKey={`${id}.buttonLabel`} label="Button label" sectionId={id} defaultValue="Request a demo" /></Button>;
}

function FeatureList({ id }: { id: string }) {
  const items = useCmsCollection(`${id}.items`, "Features", id, FEATURE_DEFAULTS);
  return <ul className="mt-12 grid grid-cols-3 gap-10 max-md:grid-cols-1 max-md:gap-7">{items.map((item) => <li key={item.id} className="border-t border-stroke pt-6"><h3 data-cms-key={`${id}.items.${item.id}.title`} className="text-[23px] leading-8 tracking-tight">{item.title}</h3><p data-cms-key={`${id}.items.${item.id}.body`} className="mt-3 text-base leading-7 text-gray-new-40">{item.body}</p></li>)}</ul>;
}

function CustomMedia({ id, kind }: { id: string; kind: "image" | "video" }) {
  const caption = useCmsString(`${id}.caption`, "", { label: "Media caption", sectionId: id });
  return <div className="min-w-0" data-cms-media-slot="true"><CmsMedia cmsKey={`${id}.media`} label={kind === "video" ? "Video" : "Image"} sectionId={id} mediaType={kind} />{caption && <p className="mt-4 text-sm text-gray-new-40" data-cms-key={`${id}.caption`}>{caption}</p>}</div>;
}
