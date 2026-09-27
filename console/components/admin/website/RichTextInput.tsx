"use client";

import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { Mark, type JSONContent } from "@tiptap/core";
import { EditorContent, useEditor, useEditorState } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import Link from "@tiptap/extension-link";
import Underline from "@tiptap/extension-underline";
import {
  safeHref,
  type RichTextDocument,
  type RichTextHeading,
  type RichTextInline,
  type RichTextList,
  type RichTextListItem,
  type RichTextMark,
  type RichTextParagraph,
} from "@antifailure/website";

export interface RichTextInputProps {
  value: RichTextDocument | string;
  onChange: (value: RichTextDocument) => void;
  disabled?: boolean;
  label: string;
  article?: boolean;
}

const Emphasis = Mark.create({
  name: "emphasis",
  parseHTML: () => [{ tag: "span[data-site-emphasis]" }],
  renderHTML: () => ["span", { "data-site-emphasis": "true", class: "text-muted" }, 0],
});

const extensions = [
  StarterKit.configure({ heading: false, blockquote: false, codeBlock: false, horizontalRule: false, strike: false, link: false, underline: false, trailingNode: false }),
  Link.configure({
    openOnClick: false,
    enableClickSelection: true,
    autolink: false,
    linkOnPaste: false,
    defaultProtocol: "https",
    isAllowedUri: (url) => safeHref(url),
    HTMLAttributes: { target: null, rel: "noopener noreferrer" },
  }),
  Underline,
  Emphasis,
];
const articleExtensions = [
  StarterKit.configure({ heading: { levels: [2, 3] }, blockquote: false, codeBlock: false, horizontalRule: false, strike: false, link: false, underline: false, trailingNode: false }),
  Link.configure({ openOnClick: false, enableClickSelection: true, autolink: false, linkOnPaste: false, defaultProtocol: "https", isAllowedUri: (url) => safeHref(url), HTMLAttributes: { target: null, rel: "noopener noreferrer" } }),
  Underline,
  Emphasis,
];

/** Tiptap can emit presentation attributes such as a link target or an ordered
 * list's start. Persist only the shared website schema, never editor HTML. */
function toRichText(source: JSONContent | string): RichTextDocument {
  if (typeof source === "string") {
    return { type: "doc", content: source.split("\n").map((text) => ({ type: "paragraph", ...(text ? { content: [{ type: "text", text }] } : {}) })) };
  }
  const inline = (node: JSONContent): RichTextInline | undefined => {
    if (node.type === "hardBreak") return { type: "hardBreak" };
    if (node.type !== "text" || typeof node.text !== "string" || !node.text) return undefined;
    const marks: RichTextMark[] = [];
    for (const mark of node.marks ?? []) {
      if (mark.type === "link" && safeHref(mark.attrs?.href)) marks.push({ type: "link", attrs: { href: mark.attrs.href } });
      else if (mark.type === "bold" || mark.type === "italic" || mark.type === "underline" || mark.type === "code" || mark.type === "emphasis") marks.push({ type: mark.type });
    }
    return { type: "text", text: node.text, ...(marks.length ? { marks } : {}) };
  };
  const paragraph = (node: JSONContent): RichTextParagraph => {
    const content = (node.content ?? []).map(inline).filter((part): part is RichTextInline => !!part);
    return { type: "paragraph", ...(content.length ? { content } : {}) };
  };
  const blocks = (nodes: JSONContent[], depth = 0): Array<RichTextParagraph | RichTextHeading | RichTextList> => {
    const result: Array<RichTextParagraph | RichTextHeading | RichTextList> = [];
    if (depth > 8) return result;
    for (const node of nodes) {
      if (node.type === "paragraph") result.push(paragraph(node));
      else if (node.type === "heading" && (node.attrs?.level === 2 || node.attrs?.level === 3)) result.push({ type: "heading", attrs: { level: node.attrs.level }, content: paragraph(node).content });
      else if (node.type === "bulletList" || node.type === "orderedList") {
        const content: RichTextListItem[] = (node.content ?? []).filter((child) => child.type === "listItem").map((child) => {
          const children = blocks(child.content ?? [], depth + 2).filter((part): part is RichTextParagraph | RichTextList => part.type !== "heading");
          return { type: "listItem", content: children.length ? children : [{ type: "paragraph" }] };
        });
        if (content.length) result.push({ type: node.type, content });
      }
    }
    return result;
  };
  const content = blocks(source.content ?? []);
  return { type: "doc", content: content.length ? content : [{ type: "paragraph" }] };
}

function Tool({ title, active, disabled, onClick, children }: { title: string; active?: boolean; disabled: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button type="button" title={title} aria-label={title} aria-pressed={active} disabled={disabled} onMouseDown={(event) => event.preventDefault()} onClick={onClick} className={`inline-flex h-11 min-w-11 items-center justify-center rounded-md px-2 text-[13px] transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ink disabled:cursor-not-allowed disabled:opacity-40 ${active ? "bg-ink text-card" : "text-muted hover:bg-paper hover:text-ink"}`}>
      {children}
    </button>
  );
}

export function RichTextInput({ value, onChange, disabled = false, label, article = false }: RichTextInputProps) {
  const id = useId();
  const [linkOpen, setLinkOpen] = useState(false);
  const [href, setHref] = useState("");
  const [linkError, setLinkError] = useState("");
  const linkInput = useRef<HTMLInputElement>(null);
  const selection = useRef<{ from: number; to: number } | null>(null);
  const onChangeRef = useRef(onChange);
  useEffect(() => { onChangeRef.current = onChange; }, [onChange]);

  const editor = useEditor({
    extensions: article ? articleExtensions : extensions,
    content: toRichText(value),
    immediatelyRender: false,
    editable: !disabled,
    editorProps: {
      attributes: {
        role: "textbox",
        "aria-multiline": "true",
        "aria-label": label,
        class: "min-h-36 max-h-96 overflow-y-auto px-3 py-3 text-base leading-relaxed text-ink outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-ink sm:text-[14px] [&_p+p]:mt-3 [&_h2]:mt-6 [&_h2]:text-xl [&_h2]:font-semibold [&_h3]:mt-5 [&_h3]:text-lg [&_h3]:font-semibold [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:list-decimal [&_ol]:pl-5 [&_li]:my-1 [&_a]:underline [&_a]:underline-offset-2 [&_code]:rounded-sm [&_code]:bg-paper [&_code]:px-1 [&_code]:font-mono [&_code]:text-[0.9em]",
      },
    },
    onUpdate: ({ editor: current }) => onChangeRef.current(toRichText(current.getJSON())),
  });
  const active = useEditorState({
    editor,
    selector: ({ editor: current }) => ({
      bold: current?.isActive("bold") ?? false,
      italic: current?.isActive("italic") ?? false,
      underline: current?.isActive("underline") ?? false,
      code: current?.isActive("code") ?? false,
      emphasis: current?.isActive("emphasis") ?? false,
      link: current?.isActive("link") ?? false,
      bulletList: current?.isActive("bulletList") ?? false,
      orderedList: current?.isActive("orderedList") ?? false,
      heading2: current?.isActive("heading", { level: 2 }) ?? false,
      heading3: current?.isActive("heading", { level: 3 }) ?? false,
    }),
  });

  useEffect(() => { editor?.setEditable(!disabled, false); }, [editor, disabled]);
  useEffect(() => {
    if (!editor) return;
    const next = toRichText(value);
    // Local keystrokes echo through the document. Replacing their contents
    // would reset the caret on every autosave; only apply external changes.
    if (JSON.stringify(toRichText(editor.getJSON())) !== JSON.stringify(next)) editor.commands.setContent(next, { emitUpdate: false });
  }, [editor, value]);
  useEffect(() => {
    if (!editor) return;
    editor.setOptions({ editorProps: { ...editor.options.editorProps, attributes: { ...editor.options.editorProps.attributes, "aria-label": label } } });
    setLinkOpen(false);
  }, [editor, label]);
  useEffect(() => { if (linkOpen) linkInput.current?.focus(); }, [linkOpen]);

  const openLink = () => {
    if (!editor) return;
    selection.current = { from: editor.state.selection.from, to: editor.state.selection.to };
    setHref(String(editor.getAttributes("link").href ?? ""));
    setLinkError("");
    setLinkOpen(true);
  };
  const applyLink = () => {
    if (!editor || disabled) return;
    const next = href.trim();
    if (next && !safeHref(next)) { setLinkError("Use an https:// address, a /page path, an #anchor, email, or phone link."); return; }
    let command = editor.chain().focus();
    if (selection.current) command = command.setTextSelection(selection.current);
    command = command.extendMarkRange("link");
    if (next) command.setLink({ href: next }).run();
    else command.unsetLink().run();
    setLinkOpen(false);
    setLinkError("");
  };
  const unavailable = disabled || !editor;

  return (
    <div className="min-w-0">
      <p id={`${id}-label`} className="mb-2 text-[12px] font-medium text-muted">{label}</p>
      <div className={`overflow-hidden rounded-md border border-rule bg-card ${disabled ? "opacity-60" : ""}`}>
        <div role="toolbar" aria-label={`${label} formatting`} className="flex flex-wrap gap-0.5 border-b border-rule p-1">
          <Tool title="Bold" active={active?.bold} disabled={unavailable} onClick={() => editor?.chain().focus().toggleBold().run()}><strong>B</strong></Tool>
          <Tool title="Italic" active={active?.italic} disabled={unavailable} onClick={() => editor?.chain().focus().toggleItalic().run()}><em>I</em></Tool>
          <Tool title="Underline" active={active?.underline} disabled={unavailable} onClick={() => editor?.chain().focus().toggleUnderline().run()}><span className="underline">U</span></Tool>
          <Tool title="Inline code" active={active?.code} disabled={unavailable} onClick={() => editor?.chain().focus().toggleCode().run()}><span className="font-mono">&lt;/&gt;</span></Tool>
          <Tool title="Muted text" active={active?.emphasis} disabled={unavailable} onClick={() => editor?.chain().focus().toggleMark("emphasis").run()}>Aa</Tool>
          <Tool title="Link" active={active?.link || linkOpen} disabled={unavailable} onClick={openLink}><svg width="16" height="16" viewBox="0 0 20 20" fill="none" aria-hidden="true"><path d="m8 12 4-4m-5 6-1 1a3.54 3.54 0 0 1-5-5l3-3a3.54 3.54 0 0 1 5 0m2 6a3.54 3.54 0 0 0 5 0l3-3a3.54 3.54 0 0 0-5-5l-1 1" transform="translate(1 -1) scale(.9)" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" /></svg></Tool>
          <Tool title="Bullet list" active={active?.bulletList} disabled={unavailable} onClick={() => editor?.chain().focus().toggleBulletList().run()}><svg width="17" height="17" viewBox="0 0 20 20" fill="none" aria-hidden="true"><path d="M7 5h10M7 10h10M7 15h10" stroke="currentColor" strokeWidth="1.4" /><circle cx="3" cy="5" r="1" fill="currentColor" /><circle cx="3" cy="10" r="1" fill="currentColor" /><circle cx="3" cy="15" r="1" fill="currentColor" /></svg></Tool>
          <Tool title="Numbered list" active={active?.orderedList} disabled={unavailable} onClick={() => editor?.chain().focus().toggleOrderedList().run()}><span className="font-mono">1.</span></Tool>
          {article && <><Tool title="Section heading" active={active?.heading2} disabled={unavailable} onClick={() => editor?.chain().focus().toggleHeading({ level: 2 }).run()}>H2</Tool><Tool title="Subheading" active={active?.heading3} disabled={unavailable} onClick={() => editor?.chain().focus().toggleHeading({ level: 3 }).run()}>H3</Tool></>}
        </div>
        {linkOpen ? (
          <div className="border-b border-rule bg-paper p-3">
            <label htmlFor={`${id}-link`} className="block text-[12px] font-medium text-muted">Link destination</label>
            <input ref={linkInput} id={`${id}-link`} type="text" inputMode="url" value={href} disabled={disabled} autoComplete="off" spellCheck={false} placeholder="https:// or /page" onChange={(event) => { setHref(event.target.value); setLinkError(""); }} onKeyDown={(event) => { if (event.key === "Enter") { event.preventDefault(); applyLink(); } else if (event.key === "Escape") { event.preventDefault(); setLinkOpen(false); editor?.commands.focus(); } }} className="mt-2 h-11 w-full rounded-md border border-rule bg-card px-3 text-base text-ink outline-none focus-visible:border-ink focus-visible:ring-1 focus-visible:ring-ink sm:text-[13px]" aria-invalid={!!linkError} aria-describedby={linkError ? `${id}-link-error` : undefined} />
            {linkError ? <p id={`${id}-link-error`} role="alert" className="mt-2 text-[12px] leading-5 text-fail">{linkError}</p> : null}
            <div className="mt-2 flex flex-wrap gap-2">
              <button type="button" onClick={applyLink} disabled={disabled} className="min-h-11 rounded-md bg-ink px-3 text-[13px] font-medium text-card focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ink disabled:opacity-40">{href.trim() ? "Apply link" : "Remove link"}</button>
              <button type="button" onClick={() => { setLinkOpen(false); editor?.commands.focus(); }} className="min-h-11 rounded-md px-3 text-[13px] text-muted hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ink">Cancel</button>
            </div>
          </div>
        ) : null}
        {editor ? <EditorContent editor={editor} /> : <div className="min-h-36 p-3 text-[13px] text-muted" role="status">Opening text editor…</div>}
      </div>
      <p className="mt-2 text-[12px] leading-5 text-dim">Select text to format it. Shift + Enter adds a line break.</p>
    </div>
  );
}
