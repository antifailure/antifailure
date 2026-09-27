import { LogoMark } from "@/components/icons";

// The console's first six navigation items, including its actual icon geometry.
// Source: console/components/Shell.tsx and console/components/icons.tsx.
const NAV = [
  ["Environments", "M3.5 5.2 8 3l4.5 2.2v5.6L8 13l-4.5-2.2zM3.5 5.2 8 7.4l4.5-2.2M8 7.4V13"],
  ["Runs", "M2.5 8h3l1.6-3.4 2.4 6.8L11.2 8h2.3"],
  ["Load", "M2.6 4.6h4.4M2.6 11.4h3.4M2.6 8h6.8M9.4 6.2 11.2 8 9.4 9.8M13.6 4.4v7.2"],
  ["Masking", "M8 2.6 13 4.5v4c0 2.6-2.1 4.2-5 4.9-2.9-.7-5-2.3-5-4.9v-4z"],
  ["Network", "M8 2.4v11.2M2.4 8h11.2M8 2.4a7.4 7.4 0 0 1 0 11.2M8 2.4a7.4 7.4 0 0 0 0 11.2"],
  ["Audit", "M4 2.6h8v10.8H4zM6.2 5.6h3.6M6.2 8h3.6M6.2 10.4h2.2"],
] as const;

// Selected records from the owner's supplied console screenshot, 2026-09-26.
// Preserve IDs and states. Environment creation failure is not a test verdict.
const ENVIRONMENTS = [
  { id: "demo-orders-default-side-eff-e3aa26", branch: "default (side-effect baseline)", pr: "1", state: "Torn down" },
  { id: "demo-orders-default-27b977", branch: "default", pr: "1", state: "Torn down" },
  { id: "demo-orders-drop-the-per-mer-faeda1", branch: "drop-the-per-merchant-scope-on-order-reads", pr: null, state: "Failed" },
] as const;

function ProductMark() {
  return <div className="flex items-center gap-3"><LogoMark className="size-5 shrink-0" /><span className="text-[13px] font-semibold tracking-[.12em] text-white">ANTIFAILURE</span></div>;
}

/** A responsive adaptation of the real console, using recorded demo data. */
export function ConsolePreview() {
  return (
    <figure className="@container">
      <div className="overflow-hidden rounded-[6px] border border-stroke bg-white font-title shadow-[0_20px_60px_-36px_rgba(0,0,0,0.28)]">
        <div className="grid grid-cols-[180px_minmax(0,1fr)] @max-[800px]:grid-cols-[64px_minmax(0,1fr)] @max-[540px]:grid-cols-1">
          <div className="flex flex-col bg-gray-new-10 px-3 py-6 @max-[800px]:px-2 @max-[540px]:hidden" aria-hidden="true">
            <div className="px-2 @max-[800px]:hidden"><ProductMark /></div>
            <LogoMark className="mx-auto hidden size-6 @max-[800px]:block" />
            <ul className="mt-9 space-y-1">
              {NAV.map(([name, path], i) => (
                <li key={name} className={`flex h-11 items-center gap-3 rounded-[5px] px-3 text-sm @max-[800px]:justify-center @max-[800px]:px-0 ${i === 0 ? "bg-white/12 text-white" : "text-gray-new-80"}`}>
                  <svg viewBox="0 0 16 16" fill="none" className="size-4 shrink-0"><path d={path} stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" /></svg>
                  <span className="@max-[800px]:hidden">{name}</span>
                </li>
              ))}
            </ul>
          </div>

          <div className="min-w-0 bg-paper p-8 @max-[800px]:p-6 @max-[540px]:p-0">
            <div className="hidden bg-gray-new-10 px-5 py-5 @max-[540px]:block" aria-hidden="true"><ProductMark /></div>
            <div className="@max-[540px]:px-5 @max-[540px]:py-6">
              <h3 className="text-[28px] font-semibold leading-tight tracking-tighter text-gray-new-10 @max-[540px]:text-[25px]">Environments</h3>
              <p className="mt-2 max-w-[45ch] text-base leading-6 text-gray-new-40">An isolated copy of your app for each change.</p>

              <div className="mt-7 overflow-hidden rounded-[6px] border border-stroke bg-white @max-[540px]:mt-6 @max-[540px]:rounded-none @max-[540px]:border-x-0">
                <div className="border-b border-stroke px-5 py-4 @max-[540px]:px-0">
                  <p className="text-sm font-medium text-gray-new-10 @max-[540px]:text-base">antifailure/demo-orders</p>
                </div>
                <table className="w-full table-fixed text-left @max-[540px]:block">
                  <thead className="text-xs text-gray-new-40 @max-[540px]:sr-only">
                    <tr className="border-b border-stroke">
                      <th scope="col" className="w-[44%] px-5 py-3 font-medium">Environment</th>
                      <th scope="col" className="w-[36%] pr-4 py-3 font-medium">Branch</th>
                      <th scope="col" className="w-[20%] pr-4 py-3 font-medium">State</th>
                    </tr>
                  </thead>
                  <tbody className="@max-[540px]:block">
                    {ENVIRONMENTS.map((env) => (
                      <tr key={env.id} className="border-b border-stroke last:border-0 @max-[540px]:grid @max-[540px]:grid-cols-1 @max-[540px]:gap-3 @max-[540px]:py-5">
                        <th scope="row" className="break-words px-5 py-6 font-mono text-[13px] font-normal leading-6 text-gray-new-10 @max-[540px]:px-0 @max-[540px]:py-0 @max-[540px]:text-base">{env.id}</th>
                        <td className="break-words py-6 pr-4 text-sm leading-6 text-gray-new-20 @max-[540px]:p-0 @max-[540px]:text-base"><span className="hidden text-gray-new-40 @max-[540px]:inline">Branch: </span>{env.branch}{env.pr && <span className="ml-1.5 whitespace-nowrap text-gray-new-40">#{env.pr}</span>}</td>
                        <td className="py-6 pr-4 align-middle @max-[540px]:p-0"><span className={`inline-flex whitespace-nowrap rounded-[4px] px-2 py-1 text-xs font-medium uppercase tracking-[.05em] ${env.state === "Failed" ? "bg-danger-ink/10 text-danger-ink" : "bg-black/5 text-gray-new-40"} @max-[540px]:text-sm`}>{env.state}</span></td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          </div>
        </div>
      </div>
      <figcaption className="mt-4 text-sm leading-6 text-gray-new-40">Antifailure console, adapted for this preview. Selected demo environments.</figcaption>
    </figure>
  );
}
