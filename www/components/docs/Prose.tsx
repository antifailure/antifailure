import type { ReactNode } from "react";

export function H1({ children }: { children: ReactNode }) {
  return (
    <h1 className="text-[36px] font-semibold leading-[1.15] tracking-[-0.035em] md:text-[42px]">
      {children}
    </h1>
  );
}

export function Lead({ children }: { children: ReactNode }) {
  return <p className="mt-4 text-[17px] leading-7 text-white/70">{children}</p>;
}

export function H2({ children }: { children: ReactNode }) {
  return (
    <h2 className="mt-12 text-[22px] font-semibold tracking-[-0.02em] text-white">{children}</h2>
  );
}

export function P({ children }: { children: ReactNode }) {
  return <p className="mt-4 text-[15px] leading-7 text-white/75">{children}</p>;
}

export function Ul({ items }: { items: ReactNode[] }) {
  return (
    <ul className="mt-4 list-disc space-y-2 pl-5 text-[15px] leading-7 text-white/75">
      {items.map((item, i) => (
        <li key={i}>{item}</li>
      ))}
    </ul>
  );
}

export function Callout({
  label,
  children,
}: {
  label?: string;
  children: ReactNode;
}) {
  return (
    <div className="mt-6 border-l-2 border-[#33bf00] bg-[#33bf00]/8 px-4 py-3 text-[14px] leading-6 text-white/80">
      {label ? <div className="mb-1 text-[11px] font-medium tracking-[0.14em] text-[#33bf00]">{label}</div> : null}
      {children}
    </div>
  );
}

export function Pre({ children }: { children: ReactNode }) {
  return (
    <pre className="mt-5 overflow-x-auto rounded-md border border-white/10 bg-[#0c0c0c] p-4 font-mono text-[12.5px] leading-6 text-white/80">
      {children}
    </pre>
  );
}

export function Table({
  headers,
  rows,
}: {
  headers: string[];
  rows: string[][];
}) {
  return (
    <div className="mt-5 overflow-x-auto rounded-md border border-white/10">
      <table className="w-full text-left text-[13.5px]">
        <thead className="bg-white/5 text-white/45">
          <tr>
            {headers.map((h) => (
              <th key={h} className="px-3 py-2 font-normal">
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="text-white/75">
          {rows.map((row) => (
            <tr key={row.join("|")} className="border-t border-white/8">
              {row.map((cell, i) => (
                <td key={i} className="px-3 py-2 align-top leading-6">
                  {cell}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
