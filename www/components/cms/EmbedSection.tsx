"use client";

import type { CustomSection } from "@antifailure/website";
import { useState } from "react";
import { useCms, useCmsField, useCmsString } from "./CmsProvider";
import { CmsSection } from "./Editable";
import { Container } from "@/components/layout/Container";
import { embedDocument } from "@/lib/cms/embed";

const DEFAULT_HTML = '<section class="custom-block"><h2>Build something useful.</h2><p>Make this space your own with HTML, CSS, and JavaScript.</p><button id="try">Try this interaction</button><p id="result" role="status">Ready when you are.</p></section>';
const DEFAULT_CSS = '.custom-block{font:16px/1.6 Arial,sans-serif;color:#193e30;background:#e4f1eb;padding:clamp(24px,5vw,56px);min-height:320px}.custom-block h2{font-size:clamp(28px,4vw,44px);line-height:1.1;letter-spacing:-.04em;margin:0 0 20px}.custom-block p{max-width:55ch}.custom-block button{font:inherit;background:#193e30;color:#fff;border:0;padding:12px 20px;min-height:44px;cursor:pointer}.custom-block button:focus-visible{outline:3px solid #168555;outline-offset:3px}';
const DEFAULT_JS = 'document.getElementById("try").addEventListener("click", function () { document.getElementById("result").textContent = "Your interaction works."; });';

export function EmbedSection({ section }: { section: CustomSection }) {
  const cms = useCms();
  const [interact, setInteract] = useState(false);
  const options = { sectionId: section.id, kind: "code" as const };
  const html = useCmsField({ ...options, key: `${section.id}.html`, label: "HTML", language: "html", defaultValue: DEFAULT_HTML });
  const css = useCmsField({ ...options, key: `${section.id}.css`, label: "CSS", language: "css", defaultValue: DEFAULT_CSS });
  const javascript = useCmsField({ ...options, key: `${section.id}.javascript`, label: "JavaScript", language: "javascript", defaultValue: DEFAULT_JS });
  const title = useCmsString(`${section.id}.title`, "Custom interactive content", { label: "Accessible name", sectionId: section.id });
  const srcDoc = embedDocument(typeof html === "string" ? html : DEFAULT_HTML, typeof css === "string" ? css : DEFAULT_CSS, typeof javascript === "string" ? javascript : DEFAULT_JS);
  return <CmsSection sectionId={section.id} group={section.group} label="Custom code" id={section.id} className="safe-paddings py-12 max-md:py-8">
    <Container size="1344">
      <div className="relative" data-cms-key={`${section.id}.html`}>
        <iframe title={title} srcDoc={srcDoc} sandbox="allow-scripts" referrerPolicy="no-referrer" loading="lazy" className="block w-full border-0" style={{ height: "var(--cms-embed-height, 360px)", pointerEvents: cms.editing && !interact ? "none" : undefined }} />
        {cms.editing && <button type="button" data-cms-interact className="absolute right-3 top-3 min-h-11 border border-stroke bg-white px-4 text-sm text-black shadow-sm" onClick={() => setInteract((value) => !value)}>{interact ? "Select this block" : "Try this block"}</button>}
      </div>
    </Container>
  </CmsSection>;
}
