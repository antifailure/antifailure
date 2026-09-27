"use client";

import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { pageBlockPrefix, setFieldOverride, setStyleOverride, sitePageSlug, validateWebsiteDocument, type CustomSectionKind, type WebsiteDocument, type WebsiteManifest } from "@antifailure/website";
import { adminMutate } from "@/lib/admin";
import { addSection, moveSection, pageSections } from "@/lib/website-client";
import { WebsitePromptRunGuard } from "@/lib/website-prompt-run";

type Proposal = {
  message: string;
  edits: Array<{ key: string; value: string | number | boolean }>;
  styles: Array<{ target: string; breakpoint: "desktop" | "tablet" | "mobile"; property: string; value: string | number }>;
  actions: Array<{ operation: "add-section" | "hide-section" | "show-section" | "move-section"; sectionId: string; after: string; kind: string; heading: string; body: string }>;
  usage: { inputTokens: number; outputTokens: number };
};
type Message = { role: "user" | "assistant"; text: string };
const errorMessage = (error: unknown) => error instanceof Error ? error.message : "The assistant could not respond. Try again.";

export function applyPromptChanges(document: WebsiteDocument, manifest: WebsiteManifest, proposal: Proposal, page: string): WebsiteDocument {
  let next = document;
  const fields = new Map(manifest.fields.map((field) => [field.key, field]));
  const targets = new Set(["global", ...manifest.sections.map((section) => section.id), ...manifest.fields.map((field) => field.key)]);
  for (const change of proposal.edits) {
    const field = fields.get(change.key);
    if (!field || ["media", "code", "richtext"].includes(field.kind)) throw new Error("The assistant suggested a field that is no longer on this page.");
    next = setFieldOverride(next, change.key, change.value, field.defaultValue);
  }
  for (const change of proposal.styles) {
    if (!targets.has(change.target)) throw new Error("The assistant suggested a style outside this page.");
    next = setStyleOverride(next, change.target, change.breakpoint, change.property as Parameters<typeof setStyleOverride>[3], change.value);
  }
  const pageId = `page-${sitePageSlug(page)}`;
  for (const action of proposal.actions) {
    if (action.operation === "add-section") {
      const validKinds = ["text", "image", "video", "split", "features", "cta", "spacer", "shape", "divider", "embed"];
      if (!validKinds.includes(action.kind)) throw new Error("The assistant suggested an unavailable block.");
      const id = `${page === "/" ? "custom-" : pageBlockPrefix(page)}${crypto.randomUUID()}`;
      next = addSection(next, action.kind as CustomSectionKind, "page", action.after || (page === "/" ? null : pageId), id);
      if (action.heading && ["text", "split", "features", "cta"].includes(action.kind)) next = setFieldOverride(next, `${id}.heading`, action.heading);
      if (action.body && ["text", "split", "features", "cta"].includes(action.kind)) next = setFieldOverride(next, `${id}.body`, { type: "doc", content: [{ type: "paragraph", content: [{ type: "text", text: action.body }] }] });
    } else if (action.operation === "hide-section" || action.operation === "show-section") {
      if (!manifest.sections.some((section) => section.id === action.sectionId)) throw new Error("The assistant suggested a section outside this page.");
      const hidden = next.sections.hidden.filter((id) => id !== action.sectionId);
      if (action.operation === "hide-section") hidden.push(action.sectionId);
      next = { ...next, sections: { ...next.sections, hidden } };
    } else if (action.operation === "move-section") {
      if (page !== "/" && !action.after) throw new Error("Choose a section after which to move this block.");
      const current = pageSections(next, manifest, "page").filter((section) => page === "/" ? !section.id.startsWith("custom-p") : section.id === pageId || section.id.startsWith(pageBlockPrefix(page))).map((section) => section.id);
      const from = current.indexOf(action.sectionId);
      const anchor = action.after ? current.indexOf(action.after) : -1;
      if (from < 0 || (action.after && anchor < 0) || action.after === action.sectionId) throw new Error("The assistant suggested an unavailable move.");
      const destination = action.after ? anchor < from ? anchor + 1 : anchor : 0;
      next = moveSection(next, "page", current, action.sectionId, destination);
    }
  }
  const checked = validateWebsiteDocument(next);
  if (!checked.ok) throw new Error("The assistant suggestion does not pass website validation.");
  return checked.document;
}

export function PromptPanel({ document, manifest, page, sectionId, selectedKey, disabled, onApply }: {
  document: WebsiteDocument; manifest: WebsiteManifest | null; page: string; sectionId?: string; selectedKey?: string;
  disabled: boolean; onApply: (next: WebsiteDocument) => void;
}) {
  const [prompt, setPrompt] = useState("");
  const [scope, setScope] = useState<"selection" | "section" | "page">("section");
  const [messages, setMessages] = useState<Message[]>([]);
  const [pendingProposal, setProposal] = useState<{ page: string; value: Proposal } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const scopeId = useId();
  const inputId = useId();
  const thread = useRef<HTMLDivElement>(null);
  const run = useRef(new WebsitePromptRunGuard(page));
  run.current.pageChanged(page);
  const proposal = pendingProposal?.page === page ? pendingProposal.value : null;
  useEffect(() => {
    setMessages([]); setProposal(null); setBusy(false); setError(null); setNotice(null); setPrompt("");
    return () => { run.current.invalidate(); };
  }, [page]);
  useEffect(() => { thread.current?.scrollTo({ top: thread.current.scrollHeight, behavior: "instant" }); }, [messages, proposal, busy, error]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!manifest || !prompt.trim() || busy || disabled) return;
    const eligible = manifest.fields.filter((field) => ["text", "url", "number", "boolean", "select"].includes(field.kind));
    const scoped = scope === "selection" && selectedKey ? eligible.filter((field) => field.key === selectedKey)
      : scope === "section" && sectionId ? eligible.filter((field) => field.sectionId === sectionId) : eligible;
    const candidates = scoped.map((field) => ({
      key: field.key, label: field.label, kind: field.kind,
      value: Object.hasOwn(document.fields, field.key) ? document.fields[field.key] : field.defaultValue,
      ...(field.options ? { options: field.options } : {}),
    })).filter((field) => ["string", "number", "boolean"].includes(typeof field.value) && String(field.value).length <= 3000);
    const fields: typeof candidates = [];
    let contextBytes = 0;
    for (const field of candidates) {
      const size = JSON.stringify(field).length;
      if (fields.length >= 70 || contextBytes + size > 11_000) break;
      fields.push(field);
      contextBytes += size;
    }
    if (!fields.length) { setError("Choose an editable text, number, or section first."); return; }
    const request = prompt.trim();
    const requestedRun = run.current.begin(page);
    setBusy(true); setError(null); setNotice(null); setProposal(null);
    try {
      const result = await adminMutate<Proposal>("admin.administration.website.propose", {
        prompt: request, page, selection: selectedKey ?? sectionId,
        fields, targets: manifest.sections.map((section) => section.id),
        fontKeys: manifest.fonts.map((font) => font.key),
        conversation: messages.slice(-6),
      });
      if (!run.current.isCurrent(requestedRun)) return;
      setMessages((held) => [...held.slice(-8), { role: "user", text: request }, { role: "assistant", text: result.message }]);
      setProposal(result.edits.length || result.styles.length || result.actions.length ? { page: requestedRun.page, value: result } : null);
      setPrompt("");
      if (!result.edits.length && !result.styles.length && !result.actions.length) setNotice("No changes were suggested. You can ask a more specific question.");
    } catch (cause) { if (run.current.isCurrent(requestedRun)) setError(errorMessage(cause)); }
    finally { if (run.current.isCurrent(requestedRun)) setBusy(false); }
  }

  function apply() {
    if (!manifest || !proposal) return;
    try {
      onApply(applyPromptChanges(document, manifest, proposal, page));
      setProposal(null); setNotice("Added to your draft. Check the preview, then publish when ready."); setError(null);
    } catch (cause) { setError(errorMessage(cause)); }
  }

  return <div className="cms-prompt-panel">
    <div className="cms-prompt-intro"><span className="cms-prompt-mark" aria-hidden>□</span><h3>Shape the page with a prompt.</h3><p>Describe the change. Review exactly what the assistant suggests before adding it to your draft.</p></div>
    <div className="cms-prompt-scope"><label htmlFor={scopeId}>Change</label><select id={scopeId} value={scope} onChange={(event) => setScope(event.target.value as typeof scope)}>
      <option value="selection" disabled={!selectedKey}>Selected element</option><option value="section" disabled={!sectionId}>Current section</option><option value="page">Entire page</option>
    </select></div>
    <div className="cms-prompt-thread" ref={thread} aria-live="polite">
      {!messages.length && <div className="cms-prompt-example"><span>Try</span><button type="button" onClick={() => setPrompt("Make this section clearer and more direct for a technical buyer. Keep every claim accurate.")}>Make this section clearer for a technical buyer ↗</button><button type="button" onClick={() => setPrompt("Tighten the hierarchy and spacing in this section without changing the brand palette.")}>Tighten the hierarchy and spacing ↗</button></div>}
      {messages.map((message, index) => <div key={index} className={`cms-prompt-message is-${message.role}`}><span>{message.role === "user" ? "You" : "Antifailure"}</span><p>{message.text}</p></div>)}
      {busy && <div className="cms-prompt-thinking" role="status">Preparing a small, reviewable edit…</div>}
      {proposal && <div className="cms-prompt-proposal"><div className="cms-prompt-proposal-head"><strong>Proposed changes</strong><span>{proposal.edits.length + proposal.styles.length + proposal.actions.length}</span></div>
        {proposal.edits.map((edit) => <div className="cms-prompt-change" key={edit.key}><small>{manifest?.fields.find((field) => field.key === edit.key)?.label ?? edit.key}</small><span>{String(edit.value)}</span></div>)}
        {proposal.styles.map((style, index) => <div className="cms-prompt-change" key={`${style.target}-${style.property}-${index}`}><small>{style.target} · {style.breakpoint} · {style.property}</small><span>{style.value}</span></div>)}
        {proposal.actions.map((action, index) => <div className="cms-prompt-change" key={`action-${index}`}><small>{action.operation.replaceAll("-", " ")}</small><span>{action.operation === "add-section" ? `${action.kind} block${action.heading ? ` · ${action.heading}` : ""}` : action.sectionId}</span></div>)}
        <div className="cms-prompt-proposal-actions"><button type="button" disabled={disabled} onClick={apply}>Apply to draft</button><button type="button" onClick={() => setProposal(null)}>Discard</button></div>
      </div>}
      {notice && <p className="cms-prompt-notice" role="status">{notice}</p>}
      {error && <p className="cms-prompt-error" role="alert">{error}</p>}
    </div>
    <form onSubmit={submit} className="cms-prompt-form"><label htmlFor={inputId}>Your instruction</label><textarea id={inputId} value={prompt} onChange={(event) => setPrompt(event.target.value)} placeholder="Make this section easier to understand…" maxLength={3000} rows={4} disabled={disabled || busy} /><div><span>Nothing goes live until you publish.</span><button type="submit" disabled={disabled || busy || !manifest || !prompt.trim()}>Send ↗</button></div></form>
  </div>;
}
