import type { ReactNode } from "react";

export function H1({ children }: { children: ReactNode }) {
  return (
    <h1 className="font-title text-[36px] font-medium leading-dense tracking-tighter md:text-[42px]">
      {children}
    </h1>
  );
}

export function Lead({ children }: { children: ReactNode }) {
  return <p className="mt-4 text-[17px] leading-7 text-black/70">{children}</p>;
}

export function H2({ children }: { children: ReactNode }) {
  return (
    <h2 className="mt-12 text-[22px] font-medium tracking-extra-tight text-black">{children}</h2>
  );
}

export function P({ children }: { children: ReactNode }) {
  return <p className="mt-4 text-[15px] leading-7 text-black/70">{children}</p>;
}

export function Ul({ items }: { items: ReactNode[] }) {
  return (
    <ul className="mt-4 list-disc space-y-2 pl-5 text-[15px] leading-7 text-black/70">
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
    <div className="mt-6 border-l-2 border-[#33bf00] bg-[#33bf00]/8 px-4 py-3 text-[14px] leading-6 text-black/80">
      {label ? <div className="mb-1 text-[11px] font-medium tracking-[0.14em] text-[#33bf00]">{label}</div> : null}
      {children}
    </div>
  );
}

export function Pre({ children }: { children: ReactNode }) {
  return (
    <pre className="mt-5 overflow-x-auto rounded-md border border-black/10 bg-white p-4 font-mono text-[12.5px] leading-6 text-black/80">
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
    <div className="mt-5 overflow-x-auto rounded-md border border-black/10">
      <table className="w-full text-left text-[13.5px]">
        <thead className="bg-black/5 text-black/45">
          <tr>
            {headers.map((h) => (
              <th key={h} className="px-3 py-2 font-normal">
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="text-black/75">
          {rows.map((row) => (
            <tr key={row.join("|")} className="border-t border-black/8">
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
