import { Fragment, type ReactNode } from "react";
import { safeHref, type RichTextDocument, type RichTextNode } from "@antifailure/website";

/** Source headings historically use only strong/br. Convert that authored
 * subset into the same safe tree as the editor, without an HTML execution path. */
export function sourceRichText(source: string): RichTextDocument {
  const content: Array<{ type: "text"; text: string; marks?: [{ type: "emphasis" }] } | { type: "hardBreak" }> = [];
  let strong = false;
  for (const token of source.split(/(<\/?strong>|<br\s*\/?\s*>)/i)) {
    if (/^<strong>$/i.test(token)) strong = true;
    else if (/^<\/strong>$/i.test(token)) strong = false;
    else if (/^<br/i.test(token)) content.push({ type: "hardBreak" });
    else if (token) content.push({ type: "text", text: token, ...(strong ? { marks: [{ type: "emphasis" as const }] } : {}) });
  }
  return { type: "doc", content: [{ type: "paragraph", content }] };
}

export function richTextPlain(value: RichTextDocument): string {
  const read = (node: RichTextNode): string => node.type === "text" ? node.text : node.type === "hardBreak" ? "\n" : (node.content ?? []).map(read).join(node.type === "paragraph" ? "" : "\n");
  return value.content.map(read).join("\n");
}

export function RichText({ value, inline = false }: { value: RichTextDocument; inline?: boolean }) {
  function render(node: RichTextNode, index: number): ReactNode {
    if (node.type === "hardBreak") return <br key={index} />;
    if (node.type === "text") {
      let text: ReactNode = node.text;
      for (const [markIndex, mark] of (node.marks ?? []).entries()) {
        if (mark.type === "bold") text = <b key={markIndex}>{text}</b>;
        else if (mark.type === "emphasis") text = <strong key={markIndex}>{text}</strong>;
        else if (mark.type === "italic") text = <em key={markIndex}>{text}</em>;
        else if (mark.type === "underline") text = <u key={markIndex}>{text}</u>;
        else if (mark.type === "code") text = <code key={markIndex}>{text}</code>;
        else if (mark.type === "link" && safeHref(mark.attrs.href)) text = <a key={markIndex} href={mark.attrs.href}>{text}</a>;
      }
      return <Fragment key={index}>{text}</Fragment>;
    }
    const children = (node.content ?? []).map(render);
    if (node.type === "paragraph") return inline ? <Fragment key={index}>{index > 0 && <br />}{children}</Fragment> : <p key={index}>{children}</p>;
    if (node.type === "listItem") return <li key={index}>{children}</li>;
    return node.type === "orderedList" ? <ol key={index}>{children}</ol> : <ul key={index}>{children}</ul>;
  }
  return <>{value.content.map(render)}</>;
}
