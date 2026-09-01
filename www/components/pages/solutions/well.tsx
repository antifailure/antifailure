import type { ReactNode } from "react";
import { Button } from "@/components/layout/Button";
import { Breadcrumbs } from "@/components/layout/Breadcrumbs";
import { Container } from "@/components/layout/Container";
import { SectionLabel } from "@/components/layout/SectionLabel";
import { PageJsonLd } from "@/lib/jsonld";
import { cn } from "@/lib/cn";

function SageWell({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "relative flex min-h-[520px] flex-col justify-center overflow-hidden rounded-[32px] bg-sage px-6 py-8 max-md:min-h-[360px] max-md:px-4 max-md:py-5 md:px-10 md:py-12",
        className,
      )}
    >
      {children}
    </div>
  );
}

function FloatWindow({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div
      className={cn(
        "rounded-[16px] bg-white shadow-[0_24px_64px_rgba(0,0,0,0.10),0_2px_8px_rgba(0,0,0,0.04)]",
        className,
      )}
    >
      {children}
    </div>
  );
}

function ArrowList({ items }: { items: { title: string; body?: string }[] }) {
  return (
    <ul className="mt-8 space-y-3.5">
      {items.map((item) => (
        <li key={item.title} className="flex gap-3">
          <span className="mt-px shrink-0 text-[15px] leading-6 text-[#33bf00]" aria-hidden>
            →
          </span>
          <p className="text-[16px] leading-6 text-gray-new-40">
            {item.body ? (
              <>
                <span className="font-medium text-black">{item.title}. </span>
                {item.body}
              </>
            ) : (
              item.title
            )}
          </p>
        </li>
      ))}
    </ul>
  );
}

function FeatureCopy({
  kicker,
  title,
  items,
}: {
  kicker: string;
  title: string;
  items: { title: string; body?: string }[];
}) {
  return (
    <>
      <SectionLabel>{kicker}</SectionLabel>
      <h2 className="mt-4 max-w-[520px] text-[36px] font-medium leading-[1.15] tracking-tighter text-black max-md:text-[28px]">
        {title}
      </h2>
      <ArrowList items={items} />
    </>
  );
}

export function FeatureRow({
  kicker,
  title,
  items,
  visual,
  reverse,
  stack,
}: {
  kicker: string;
  title: string;
  items: { title: string; body?: string }[];
  visual: ReactNode;
  reverse?: boolean;
  stack?: boolean;
}) {
  return (
    <section className="py-24 safe-paddings max-md:py-14">
      <Container size="1600" className="page-measure">
        {stack ? (
          <>
            <div className="max-w-[640px]">
              <FeatureCopy kicker={kicker} title={title} items={items} />
            </div>
            <div className="mt-14 max-md:mt-10">{visual}</div>
          </>
        ) : (
          <div
            className={cn(
              "grid grid-cols-12 items-center gap-x-16 gap-y-12 max-xl:grid-cols-1",
              reverse && "[&>*:first-child]:max-xl:order-2",
            )}
          >
            <div className={cn("col-span-5 max-xl:col-span-1", reverse && "xl:col-start-8 xl:order-2")}>
              <FeatureCopy kicker={kicker} title={title} items={items} />
            </div>
            <div className={cn("col-span-7 max-xl:col-span-1", reverse && "xl:col-start-1 xl:row-start-1")}>
              {visual}
            </div>
          </div>
        )}
      </Container>
    </section>
  );
}

function HeroCopy({
  eyebrow,
  title,
  paragraphs,
}: {
  eyebrow: string;
  title: string;
  paragraphs: string[];
}) {
  return (
    <>
      <SectionLabel>{eyebrow}</SectionLabel>
      <h1 className="mt-5 max-w-[520px] text-[44px] font-medium leading-[1.15] tracking-tighter text-black max-lg:text-[32px]">
        {title}
      </h1>
      <div className="mt-8 max-w-[480px]">
        {paragraphs.map((p) => (
          <p key={p} className="mt-5 text-[17px] leading-7 text-gray-new-40 first:mt-0">
            {p}
          </p>
        ))}
      </div>
      <div className="mt-8">
        <Button href="/signup" theme="filled">
          Get started
        </Button>
      </div>
    </>
  );
}

export function SplitHero({
  path,
  eyebrow,
  title,
  paragraphs,
  visual,
  flip,
  stack,
}: {
  path: string;
  eyebrow: string;
  title: string;
  paragraphs: string[];
  visual: ReactNode;
  flip?: boolean;
  stack?: boolean;
}) {
  return (
    <section className="pt-24 pb-12 safe-paddings max-lg:pt-16 max-md:pt-12 max-md:pb-8">
      <Container size="1600" className="page-measure">
        <PageJsonLd path={path} />
        <Breadcrumbs path={path} />
        {stack ? (
          <>
            <div className="max-w-[640px]">
              <HeroCopy eyebrow={eyebrow} title={title} paragraphs={paragraphs} />
            </div>
            <div className="mt-14 max-md:mt-10">{visual}</div>
          </>
        ) : (
          <div className="grid grid-cols-12 items-center gap-x-16 gap-y-12 max-xl:grid-cols-1">
            <div className={cn("col-span-5 max-xl:col-span-1", flip && "xl:col-start-8 xl:order-2")}>
              <HeroCopy eyebrow={eyebrow} title={title} paragraphs={paragraphs} />
            </div>
            <div className={cn("col-span-7 max-xl:col-span-1", flip && "xl:col-start-1 xl:row-start-1")}>
              {visual}
            </div>
          </div>
        )}
      </Container>
    </section>
  );
}

function Check({ on }: { on?: boolean }) {
  return (
    <span
      className={cn(
        "mt-0.5 inline-flex size-4 shrink-0 items-center justify-center rounded-[4px] border",
        on ? "border-[#33bf00] bg-[#33bf00] text-white" : "border-black/20 bg-white",
      )}
    >
      {on ? (
        <svg viewBox="0 0 12 12" className="size-2.5" fill="none" aria-hidden>
          <path d="M2.5 6.2 5 8.7 9.5 3.5" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
        </svg>
      ) : null}
    </span>
  );
}

function TonePill({
  tone,
  children,
  className,
}: {
  tone: "PASS" | "WARN" | "BLOCK";
  children: ReactNode;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-[10px] font-medium whitespace-nowrap",
        tone === "PASS" && "bg-[#E4F1EB] text-[#285D49]",
        tone === "WARN" && "bg-[#f7f7f5] text-black/70",
        tone === "BLOCK" && "bg-black text-white",
        className,
      )}
    >
      <span
        className={cn(
          "size-1.5 rounded-full",
          tone === "PASS" && "bg-[#33bf00]",
          tone === "WARN" && "bg-black/35",
          tone === "BLOCK" && "bg-white",
        )}
      />
      {children}
    </span>
  );
}

type NotebookRow = {
  id: string;
  label: string;
  status: string;
  tone: "PASS" | "WARN" | "BLOCK";
  bar: number;
  kind?: string;
};

/**
 * The notebook body is either rows or a scene, never both.
 *
 * It was both on two pages, and `children` wins in the render, so five rows of
 * written data on each were dead: edited, reviewed, and never drawn. A union
 * rather than two optional props, so passing both stops compiling instead of
 * quietly discarding one of them.
 */
type NotebookBody =
  | { rows: NotebookRow[]; children?: never }
  | { children: ReactNode; rows?: never };

export function Notebook({
  tab,
  rail,
  rows,
  overlay,
  overlaySide = "left",
  children,
}: {
  tab: string;
  rail: string;
  overlay: { title: string; checks: string[] };
  overlaySide?: "left" | "right";
} & NotebookBody) {
  return (
    <SageWell>
      <div className="relative min-h-[440px] max-md:min-h-0">
        <FloatWindow
          className={cn(
            "overflow-hidden max-md:ml-0 max-md:mr-0",
            overlaySide === "left" ? "ml-[22%]" : "mr-[22%]",
          )}
        >
          <div className="flex items-end gap-1 border-b border-black/[0.06] bg-[#f7f7f5] px-3 pt-3">
            <div className="flex items-center gap-2 rounded-t-[8px] bg-[#CAE6D9] px-3 py-2 text-[12px] font-medium text-[#285D49]">
              {tab}
              {/* Window chrome, not a control. Unmarked it announced a bare
                  "times" inside the figure and measured 1.8:1, which is a
                  contrast failure only because it was posing as text. */}
              <span className="text-[#285D49]/40" aria-hidden>
                ×
              </span>
            </div>
            <div className="mb-2 ml-auto mr-2 border-b-2 border-[#33bf00] pb-1 text-[11px] font-medium uppercase tracking-[0.12em] text-black">
              {rail}
            </div>
          </div>
          <div className="flex min-h-[300px]">
            <div className="hidden w-[52px] shrink-0 flex-col items-center gap-3 border-r border-black/[0.06] bg-[#f7f7f5] py-4 sm:flex">
              <span className="text-[9px] font-medium uppercase tracking-[0.14em] text-black/35 [writing-mode:vertical-rl]">
                {rail}
              </span>
              <span className="mt-2 size-1.5 rounded-full bg-[#33bf00]" aria-hidden />
              <span className="size-1.5 rounded-full bg-black/15" aria-hidden />
              <span className="size-1.5 rounded-full bg-black/15" aria-hidden />
            </div>
            <div className="min-w-0 flex-1">
              {children ? (
                // No fixed height. At 340px the scene's last row was sliced
                // through the middle of its glyphs on every width, which reads
                // as a rendering fault rather than as a crop.
                <div className="overflow-hidden bg-[#f7f7f5]">{children}</div>
              ) : (
                <div className="p-3">
                  <div className="mb-2 flex items-center justify-between px-1 text-[10px] text-black/35">
                    <span>{rows?.length ?? 0} rows · referential subset</span>
                    <span className="hidden sm:inline">Filter · Sort</span>
                  </div>
                  <div className="overflow-hidden rounded-[10px] border border-black/[0.06]">
                    <div className="grid grid-cols-[16px_72px_1fr_auto_64px] items-center gap-2 bg-[#f7f7f5] px-3 py-1.5 text-[9px] font-medium uppercase tracking-[0.12em] text-black/35 max-sm:grid-cols-[16px_1fr_auto]">
                      <span className="size-3 rounded-[3px] border border-black/20 bg-white" aria-hidden />
                      <span className="max-sm:hidden">Id</span>
                      <span>Entity</span>
                      <span>Policy</span>
                      <span className="max-sm:hidden">Cover</span>
                    </div>
                    {rows?.map((row, i) => (
                      <div
                        key={row.id}
                        className={cn(
                          "grid grid-cols-[16px_72px_1fr_auto_64px] items-center gap-2 px-3 py-2.5 max-sm:grid-cols-[16px_1fr_auto]",
                          i % 2 === 0 ? "bg-[#f7f7f5]" : "bg-white",
                        )}
                      >
                        <span
                          className={cn(
                            "size-3 rounded-[3px] border",
                            i < (rows?.length ?? 0) - 1
                              ? "border-[#33bf00] bg-[#33bf00]"
                              : "border-black/20 bg-white",
                          )}
                          aria-hidden
                        />
                        <span className="truncate font-mono text-[11px] text-black/40 max-sm:hidden">
                          {row.id}
                        </span>
                        <span className="min-w-0 truncate text-[13px] text-black">
                          {row.label}
                          {row.kind ? (
                            <span className="ml-2 hidden font-mono text-[10px] text-black/30 sm:inline">
                              {row.kind}
                            </span>
                          ) : null}
                        </span>
                        <TonePill tone={row.tone}>{row.status}</TonePill>
                        <span className="hidden h-1.5 overflow-hidden rounded-full bg-black/[0.06] sm:block">
                          <span className="block h-full rounded-full bg-[#CAE6D9]" style={{ width: `${row.bar}%` }} />
                        </span>
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </div>
          </div>
        </FloatWindow>
        <div
          className={cn(
            "absolute top-[16%] z-10 w-[min(100%,292px)] rounded-[12px] bg-white p-4 shadow-[0_16px_48px_rgba(0,0,0,0.14)] max-md:static max-md:mt-4 max-md:w-full",
            overlaySide === "left" ? "left-0" : "right-0",
          )}
        >
          <div className="text-[13px] font-semibold tracking-tight text-black">{overlay.title}</div>
          <ul className="mt-3 space-y-2.5">
            {overlay.checks.map((check, i) => (
              <li key={check} className="flex gap-2 text-[12px] leading-4 text-gray-new-40">
                <Check on={i < overlay.checks.length - 1} />
                {check}
              </li>
            ))}
          </ul>
        </div>
      </div>
    </SageWell>
  );
}

export function DashChart({
  title,
  popup,
  bars,
  popupSide = "right",
}: {
  title: string;
  popup: { title: string; rows: [string, string][] };
  bars: number[];
  popupSide?: "left" | "right";
}) {
  const ticks = [100, 75, 50, 25, 0];
  return (
    <SageWell>
      <FloatWindow
        className={cn(
          "relative p-5 max-md:ml-0 max-md:mr-0",
          popupSide === "right" ? "ml-[6%] mr-[-12px]" : "mr-[6%] ml-[-12px]",
        )}
      >
        <div className="flex items-baseline justify-between gap-3">
          <div className="text-[13px] font-medium text-black">{title}</div>
          <span className="font-mono text-[10px] uppercase tracking-[0.12em] text-black/35">twin · live</span>
        </div>
        <div className="relative mt-5 h-44">
          <div className="absolute inset-y-0 left-0 flex w-8 flex-col justify-between pr-2 text-right font-mono text-[9px] text-black/30">
            {ticks.map((t) => (
              <span key={t}>{t}</span>
            ))}
          </div>
          <div className="absolute inset-0 left-8">
            {ticks.map((t) => (
              <div
                key={t}
                className="absolute right-0 left-0 border-t border-black/[0.05]"
                style={{ top: `${100 - t}%` }}
              />
            ))}
            <div className="absolute inset-x-0 bottom-0 flex h-full items-end gap-1.5 px-1">
              {bars.map((h, i) => (
                <div key={i} className="relative flex h-full flex-1 items-end">
                  <div
                    className="w-full rounded-t-[5px]"
                    style={{
                      height: `${h}%`,
                      background: i === bars.length - 2 ? "#33bf00" : "#CAE6D9",
                    }}
                  />
                </div>
              ))}
            </div>
            <svg
              className="pointer-events-none absolute inset-0 h-full w-full"
              viewBox="0 0 100 100"
              preserveAspectRatio="none"
              aria-hidden
            >
              <polyline
                fill="none"
                stroke="#285D49"
                strokeWidth="1.2"
                vectorEffect="non-scaling-stroke"
                points={bars
                  .map((h, i) => `${((i + 0.5) / bars.length) * 100},${100 - h}`)
                  .join(" ")}
              />
            </svg>
          </div>
        </div>
        <div className="mt-3 flex justify-between pl-8 font-mono text-[9px] text-black/30">
          <span>t0</span>
          <span>peak</span>
          <span>now</span>
        </div>
      </FloatWindow>
      <div
        className={cn(
          // 34% rather than 18%: at 18% the card's left edge cut through the
          // middle of the chart's own header row, leaving half of "twin · live"
          // showing, which reads as broken text rather than as depth.
          "absolute top-[34%] z-10 w-[220px] rounded-[12px] bg-white p-4 shadow-[0_16px_48px_rgba(0,0,0,0.14)] max-md:static max-md:mt-4 max-md:w-full max-md:right-auto max-md:left-auto",
          popupSide === "right" ? "right-[6%]" : "left-[6%]",
        )}
      >
        <div className="text-[12px] font-semibold tracking-tight text-black">{popup.title}</div>
        <dl className="mt-3 space-y-2">
          {popup.rows.map(([k, v]) => (
            <div key={k} className="flex justify-between gap-3 text-[12px]">
              <dt className="text-gray-new-40">{k}</dt>
              <dd className="font-medium text-black">{v}</dd>
            </div>
          ))}
        </dl>
      </div>
    </SageWell>
  );
}

export function CircularMap({
  tabs,
  active,
  rings,
  shift = "right",
}: {
  tabs: string[];
  active: string;
  rings: { label: string; r: number }[];
  shift?: "left" | "right" | "center";
}) {
  const ticks = Array.from({ length: 36 }, (_, i) => i);
  return (
    <SageWell>
      <FloatWindow
        className={cn(
          "p-5 max-md:ml-0 max-md:mr-0",
          shift === "right" && "ml-[8%] mr-[-8px]",
          shift === "left" && "mr-[8%] ml-[-8px]",
          shift === "center" && "mx-auto",
        )}
      >
        <div className="flex gap-5 border-b border-black/[0.06] text-[11px] font-medium uppercase tracking-[0.12em]">
          {tabs.map((tab) => (
            <span
              key={tab}
              className={cn(
                "pb-3",
                tab === active ? "border-b-2 border-[#33bf00] text-black" : "text-black/35",
              )}
            >
              {tab}
            </span>
          ))}
        </div>
        <div className="relative mx-auto mt-5 aspect-square max-h-[340px] w-full max-w-[340px]">
          <svg viewBox="0 0 200 200" className="size-full" aria-hidden>
            {ticks.map((i) => {
              const a = (i / 36) * Math.PI * 2;
              const inner = i % 3 === 0 ? 88 : 90;
              return (
                <line
                  key={i}
                  x1={100 + Math.cos(a) * inner}
                  y1={100 + Math.sin(a) * inner}
                  x2={100 + Math.cos(a) * 94}
                  y2={100 + Math.sin(a) * 94}
                  stroke="#285D49"
                  strokeOpacity={i % 3 === 0 ? 0.35 : 0.12}
                  strokeWidth="1"
                />
              );
            })}
            <circle cx="100" cy="100" r="78" fill="none" stroke="#E4F1EB" strokeWidth="14" />
            <circle cx="100" cy="100" r="58" fill="none" stroke="#CAE6D9" strokeWidth="12" />
            <circle
              cx="100"
              cy="100"
              r="40"
              fill="none"
              stroke="#33bf00"
              strokeWidth="8"
              strokeDasharray="70 18"
              strokeLinecap="round"
            />
            <circle cx="100" cy="100" r="22" fill="#f7f7f5" stroke="black" strokeOpacity="0.08" />
            <circle cx="100" cy="100" r="6" fill="#285D49" />
            <path
              d="M100 22 A78 78 0 0 1 168 70"
              fill="none"
              stroke="#33bf00"
              strokeWidth="4"
              strokeLinecap="round"
            />
          </svg>
          {rings.map((ring, i) => {
            const a = (i / Math.max(rings.length, 1)) * Math.PI * 1.7 - 0.55;
            return (
              <span
                key={ring.label}
                className="absolute rounded-full border border-black/[0.06] bg-white px-2 py-0.5 text-[10px] font-medium text-black shadow-sm"
                style={{
                  left: `${50 + Math.cos(a) * ring.r}%`,
                  top: `${50 + Math.sin(a) * ring.r}%`,
                  transform: "translate(-50%, -50%)",
                }}
              >
                {ring.label}
              </span>
            );
          })}
        </div>
      </FloatWindow>
    </SageWell>
  );
}

const AVATAR = ["#285D49", "#33bf00", "#1a1a1a", "#5a8f6e"];

export function TaskTable({
  heading,
  rows,
  shift = "right",
}: {
  heading: string;
  rows: { task: string; status: string; tone: "PASS" | "WARN" | "BLOCK"; who: string; date: string }[];
  shift?: "left" | "right";
}) {
  return (
    <SageWell>
      <FloatWindow
        className={cn(
          "overflow-hidden max-md:ml-0 max-md:mr-0",
          shift === "right" ? "ml-[4%] mr-[-10px]" : "mr-[4%] ml-[-10px]",
        )}
      >
        <div className="flex items-center justify-between px-5 py-4">
          <div className="text-[13px] font-medium text-black">{heading}</div>
          <span className="font-mono text-[10px] uppercase tracking-[0.12em] text-black/35">
            {rows.length} tasks
          </span>
        </div>
        <div className="grid grid-cols-[1.5fr_0.9fr_0.7fr_0.7fr] gap-2 border-y border-black/[0.06] bg-[#f7f7f5] px-5 py-2 text-[10px] font-medium uppercase tracking-[0.12em] text-black/45 max-md:grid-cols-2">
          <span>Task</span>
          <span>Status</span>
          <span className="max-md:hidden">Assignee</span>
          <span className="max-md:hidden">Finish</span>
        </div>
        {rows.map((row, i) => (
          <div
            key={row.task}
            className={cn(
              "grid grid-cols-[1.5fr_0.9fr_0.7fr_0.7fr] items-center gap-2 border-b border-black/[0.06] px-5 py-3 last:border-0 max-md:grid-cols-2",
              i === 1 && "bg-[#f7f7f5]",
            )}
          >
            <span className="truncate text-[13px] text-black">{row.task}</span>
            <TonePill tone={row.tone} className="justify-self-start">
              {row.status}
            </TonePill>
            <span className="flex items-center gap-2 max-md:hidden">
              <span
                className="inline-flex size-6 items-center justify-center rounded-full text-[10px] font-medium text-white"
                style={{ background: AVATAR[i % AVATAR.length] }}
              >
                {row.who}
              </span>
            </span>
            <span className="font-mono text-[11px] text-black/45 max-md:hidden">{row.date}</span>
          </div>
        ))}
      </FloatWindow>
    </SageWell>
  );
}
