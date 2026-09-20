// The engine's own report, rendered.
//
// The rich half of what a run gathers never reaches the runs and verdicts
// tables: the findings and their fixes, the migration statements, the egress
// substitutions, the access probe readings, the load percentiles. It arrives
// as the markdown the engine already wrote for a human and lands whole in
// pr_generations.verdict.markdown, and this turns that one string into the
// page. The parse lives in reportblocks.ts so it can be tested without a DOM;
// this maps the blocks it returns onto elements styled like the rest of the
// console.

import { Fragment, type ReactNode } from "react";
import { parseReport, segments } from "@/lib/reportblocks";

const codeClass =
  "rounded bg-[color:var(--color-ink)]/[0.06] px-1 py-0.5 font-mono text-[12px] text-ink";
const linkClass =
  "text-ink underline decoration-rule-strong underline-offset-2 hover:decoration-ink";

function Inline({ text }: { text: string }): ReactNode {
  return (
    <>
      {segments(text).map((s, i) => {
        if (s.kind === "bold")
          return (
            <strong key={i} className="font-semibold text-ink">
              {s.text}
            </strong>
          );
        if (s.kind === "code")
          return (
            <code key={i} className={codeClass}>
              {s.text}
            </code>
          );
        if (s.kind === "link")
          return (
            <a key={i} href={s.href} className={linkClass} target="_blank" rel="noreferrer">
              {s.text}
            </a>
          );
        return <Fragment key={i}>{s.text}</Fragment>;
      })}
    </>
  );
}

export function ReportMarkdown({ source }: { source: string }): ReactNode {
  const blocks = parseReport(source);
  return (
    <div className="px-4 py-4">
      {blocks.map((b, i) => {
        if (b.kind === "heading") {
          const cls =
            b.level <= 3
              ? "mt-6 text-[14px] font-semibold text-ink first:mt-0"
              : "mt-5 text-[13px] font-semibold text-muted first:mt-0";
          return (
            <p key={i} className={cls}>
              <Inline text={b.text} />
            </p>
          );
        }
        if (b.kind === "table") {
          return (
            <div key={i} className="mt-4 overflow-x-auto first:mt-0">
              <table className="w-full border-collapse text-[13px]">
                <thead>
                  <tr className="border-b border-rule-strong text-left">
                    {b.header.map((h, c) => (
                      <th
                        key={c}
                        className="py-2 pr-4 text-[11px] font-semibold uppercase tracking-[0.06em] text-dim"
                      >
                        <Inline text={h} />
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {b.rows.map((r, ri) => (
                    <tr key={ri} className="border-b border-rule align-top last:border-0">
                      {r.map((cell, ci) => (
                        <td key={ci} className="py-2 pr-4 text-ink">
                          <Inline text={cell} />
                        </td>
                      ))}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          );
        }
        if (b.kind === "details") {
          return (
            <details
              key={i}
              className="mt-4 rounded-md border border-rule bg-card px-3 py-2 first:mt-0"
            >
              <summary className="cursor-pointer text-[13px] text-ink marker:text-dim">
                <Inline text={b.summary} />
              </summary>
              <div className="mt-2 space-y-1 font-mono text-[12px] text-muted">
                {b.lines.map((l, li) => (
                  <div key={li}>
                    <Inline text={l} />
                  </div>
                ))}
              </div>
            </details>
          );
        }
        if (b.kind === "sub") {
          return (
            <p key={i} className="mt-4 text-[11px] leading-5 text-dim first:mt-0">
              <Inline text={b.text} />
            </p>
          );
        }
        if (b.kind === "list") {
          return (
            <ul
              key={i}
              className="mt-3 list-disc space-y-1 pl-5 text-[13px] leading-6 text-ink first:mt-0"
            >
              {b.items.map((it, li) => (
                <li key={li}>
                  <Inline text={it} />
                </li>
              ))}
            </ul>
          );
        }
        return (
          <p key={i} className="mt-3 text-[13px] leading-6 text-ink first:mt-0">
            <Inline text={b.text} />
          </p>
        );
      })}
    </div>
  );
}
