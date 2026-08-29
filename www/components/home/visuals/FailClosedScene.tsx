const COLUMNS = [
  {
    title: "Simulated",
    count: 8,
    icon: "todo" as const,
    cards: [
      {
        id: "CHG-184",
        title: "POST /v1/charges $49.00",
        tags: [{ label: "Stripe", color: "#eb5757" }],
        who: { initial: "L", bg: "#c17a5a" },
      },
      {
        id: "CHG-185",
        title: "Retry charge on checkout",
        tags: [
          { label: "Stripe", color: "#eb5757" },
          { label: "Clone-local", color: "#4cb782" },
        ],
        who: { initial: "D", bg: "#6b8cae" },
      },
      {
        id: "CHG-190",
        title: "Duplicate charge attempt",
        tags: [{ label: "Stripe", color: "#eb5757" }],
        who: { initial: "A", bg: "#5a8f6e" },
      },
      {
        id: "INV-044",
        title: "Refund path on cancel",
        tags: [{ label: "Stripe", color: "#eb5757" }],
        who: { initial: "L", bg: "#c17a5a" },
      },
    ],
  },
  {
    title: "Captured",
    count: 4,
    icon: "progress" as const,
    cards: [
      {
        id: "MAIL-91",
        title: "Order #4182 receipt",
        tags: [{ label: "Email", color: "#5e9ed6" }],
        who: { initial: "D", bg: "#6b8cae" },
      },
      {
        id: "WH-220",
        title: "slack.hooks · store only",
        tags: [
          { label: "Webhook", color: "#4cb782" },
          { label: "Preview", color: "#8a8f98" },
        ],
        who: { initial: "A", bg: "#5a8f6e" },
        meta: "61039",
      },
      {
        id: "MAIL-92",
        title: "Password reset never sent",
        tags: [{ label: "Email", color: "#5e9ed6" }],
        who: { initial: "L", bg: "#c17a5a" },
      },
    ],
  },
  {
    title: "Blocked",
    count: 4,
    icon: "done" as const,
    cards: [
      {
        id: "DNS-018",
        title: "api.prod.internal",
        tags: [{ label: "Production", color: "#eb5757" }],
        who: { initial: "A", bg: "#5a8f6e" },
      },
      {
        id: "TCP-443",
        title: "18.4.2.9 · ip-bypass",
        tags: [{ label: "Unknown", color: "#8a8f98" }],
        who: { initial: "L", bg: "#c17a5a" },
      },
      {
        id: "WH-441",
        title: "hooks.prod.internal",
        tags: [{ label: "Webhook", color: "#eb5757" }],
        who: { initial: "D", bg: "#6b8cae" },
      },
    ],
  },
] as const;

const MESSAGES = [
  {
    name: "lena",
    initial: "L",
    bg: "#c17a5a",
    time: "8:55 PM",
    body: "Twin posted POST /v1/charges $49.00. Simulated against clone-local Stripe. Not live.",
  },
  {
    name: "didier",
    initial: "D",
    bg: "#6b8cae",
    time: "8:55 PM",
    body: "Yea — SendGrid /v3/mail/send is captured. MIME stored. Never delivered.",
  },
  {
    name: "andreas",
    initial: "A",
    bg: "#5a8f6e",
    time: "8:56 PM",
    body: "hooks.prod.internal is unresolved. Unknown destination — denied. Fail closed.",
  },
] as const;

function ColIcon({ kind }: { kind: "todo" | "progress" | "done" }) {
  if (kind === "todo") {
    return (
      <svg viewBox="0 0 14 14" className="size-3.5" fill="none" aria-hidden>
        <circle cx="7" cy="7" r="5.15" stroke="#9B9EA5" strokeWidth="1.4" />
      </svg>
    );
  }
  if (kind === "progress") {
    return (
      <svg viewBox="0 0 14 14" className="size-3.5" aria-hidden>
        <circle cx="7" cy="7" r="5.15" fill="none" stroke="#F2C94C" strokeWidth="1.4" />
        <path d="M7 1.85 A5.15 5.15 0 0 1 12.15 7 H7 Z" fill="#F2C94C" />
      </svg>
    );
  }
  return (
    <svg viewBox="0 0 14 14" className="size-3.5" fill="none" aria-hidden>
      <circle cx="7" cy="7" r="5.15" fill="#4CB782" />
      <path d="M4.6 7.1 6.3 8.8 9.5 5.4" stroke="#fff" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function SlackHash() {
  return (
    <svg viewBox="0 0 24 24" className="size-[18px]" aria-hidden>
      <path fill="#E01E5A" d="M6.313 15.165a2.527 2.527 0 0 1 2.521-2.52 2.527 2.527 0 0 1 2.521 2.52v6.313A2.528 2.528 0 0 1 8.834 24a2.528 2.528 0 0 1-2.521-2.522z" />
      <path fill="#E01E5A" d="M5.042 15.165a2.528 2.528 0 0 1-2.52 2.523A2.528 2.528 0 0 1 0 15.165a2.527 2.527 0 0 1 2.522-2.52h2.52z" />
      <path fill="#36C5F0" d="M8.834 6.313a2.528 2.528 0 0 1 2.521 2.521 2.528 2.528 0 0 1-2.521 2.521H2.522A2.528 2.528 0 0 1 0 8.834a2.528 2.528 0 0 1 2.522-2.521z" />
      <path fill="#36C5F0" d="M8.834 5.042a2.528 2.528 0 0 1-2.521-2.52A2.528 2.528 0 0 1 8.834 0a2.528 2.528 0 0 1 2.521 2.522v2.52z" />
      <path fill="#2EB67D" d="M17.688 8.834a2.528 2.528 0 0 1-2.523 2.521 2.527 2.527 0 0 1-2.52-2.521V2.522A2.527 2.527 0 0 1 15.165 0a2.528 2.528 0 0 1 2.523 2.522z" />
      <path fill="#2EB67D" d="M18.956 8.834a2.528 2.528 0 0 1 2.522-2.521A2.528 2.528 0 0 1 24 8.834a2.528 2.528 0 0 1-2.522 2.521h-2.522z" />
      <path fill="#ECB22E" d="M15.165 17.688a2.527 2.527 0 0 1-2.52-2.523 2.526 2.526 0 0 1 2.52-2.52h6.313A2.527 2.527 0 0 1 24 15.165a2.528 2.528 0 0 1-2.522 2.523z" />
      <path fill="#ECB22E" d="M15.165 18.956a2.528 2.528 0 0 1 2.523 2.522A2.528 2.528 0 0 1 15.165 24a2.527 2.527 0 0 1-2.52-2.522v-2.522z" />
    </svg>
  );
}

function DotsIcon({ className = "size-4" }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" className={className} fill="currentColor" aria-hidden>
      <circle cx="8" cy="3.25" r="1.15" />
      <circle cx="8" cy="8" r="1.15" />
      <circle cx="8" cy="12.75" r="1.15" />
    </svg>
  );
}

function PlusIcon({ className = "size-3.5" }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" className={className} fill="none" aria-hidden>
      <path d="M8 3.4v9.2M3.4 8h9.2" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
    </svg>
  );
}

function TypeIcon() {
  return (
    <svg viewBox="0 0 16 16" className="size-4" fill="none" aria-hidden>
      <path d="M3.2 4.2h9.6M8 4.2v7.8M5.6 12h4.8" stroke="currentColor" strokeWidth="1.35" strokeLinecap="round" />
    </svg>
  );
}

function EmojiIcon() {
  return (
    <svg viewBox="0 0 16 16" className="size-4" fill="none" aria-hidden>
      <circle cx="8" cy="8" r="5" stroke="currentColor" strokeWidth="1.35" />
      <path d="M5.8 9.3c.55.85 1.35 1.25 2.2 1.25s1.65-.4 2.2-1.25" stroke="currentColor" strokeWidth="1.35" strokeLinecap="round" />
      <circle cx="6.2" cy="6.6" r="0.7" fill="currentColor" />
      <circle cx="9.8" cy="6.6" r="0.7" fill="currentColor" />
    </svg>
  );
}

function AtIcon() {
  return (
    <svg viewBox="0 0 16 16" className="size-4" fill="none" aria-hidden>
      <path d="M10.55 8a2.55 2.55 0 1 1-2.55-2.55" stroke="currentColor" strokeWidth="1.35" strokeLinecap="round" />
      <path d="M10.55 5.45V9.1a1.65 1.65 0 0 0 3.15-.75 5.7 5.7 0 1 0-2.05 4.4" stroke="currentColor" strokeWidth="1.35" strokeLinecap="round" />
    </svg>
  );
}

function VideoIcon() {
  return (
    <svg viewBox="0 0 16 16" className="size-4" fill="none" aria-hidden>
      <rect x="2.3" y="4.5" width="7.8" height="7" rx="1.5" stroke="currentColor" strokeWidth="1.35" />
      <path d="M10.1 7.15 13.5 5.5v5.1L10.1 8.9V7.15Z" stroke="currentColor" strokeWidth="1.35" strokeLinejoin="round" />
    </svg>
  );
}

function MicIcon() {
  return (
    <svg viewBox="0 0 16 16" className="size-4" fill="none" aria-hidden>
      <rect x="6.1" y="2.7" width="3.8" height="6.2" rx="1.9" stroke="currentColor" strokeWidth="1.35" />
      <path d="M4.5 8.15a3.5 3.5 0 0 0 7 0M8 11.7v1.6" stroke="currentColor" strokeWidth="1.35" strokeLinecap="round" />
    </svg>
  );
}

function SlashIcon() {
  return (
    <svg viewBox="0 0 16 16" className="size-4" fill="none" aria-hidden>
      <rect x="2.6" y="2.6" width="10.8" height="10.8" rx="2.2" stroke="currentColor" strokeWidth="1.35" />
      <path d="M5 11 11 5" stroke="currentColor" strokeWidth="1.35" strokeLinecap="round" />
    </svg>
  );
}

function PlaneIcon() {
  return (
    <svg viewBox="0 0 16 16" className="size-3.5" fill="currentColor" aria-hidden>
      <path d="M2.2 7.55 13.7 3.2c.55-.22.9.28.62.78L10.1 13.4c-.22.5-.78.48-1.02-.04L7.4 9.55 3.05 8.4c-.58-.16-.58-.72.15-.85Z" />
    </svg>
  );
}

function ChevronIcon() {
  return (
    <svg viewBox="0 0 12 12" className="size-3" fill="none" aria-hidden>
      <path d="M3 4.55 6 7.55 9 4.55" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function SignalIcon() {
  return (
    <svg viewBox="0 0 12 12" className="size-3" fill="currentColor" aria-hidden>
      <rect x="1" y="7.4" width="2" height="3.1" rx="0.4" />
      <rect x="5" y="4.8" width="2" height="5.7" rx="0.4" opacity="0.55" />
      <rect x="9" y="2.4" width="2" height="8.1" rx="0.4" opacity="0.28" />
    </svg>
  );
}

function Tag({ color, label }: { color: string; label: string }) {
  return (
    <span className="inline-flex items-center gap-1 rounded-[5px] bg-[#F4F4F6] px-1.5 py-[3px] text-[11px] leading-none text-[#6B6F76]">
      <span className="size-[6px] rounded-full" style={{ background: color }} />
      {label}
    </span>
  );
}

export function FailClosedScene() {
  return (
    <div data-scene="fail-closed" className="pointer-events-none relative w-full select-none" aria-hidden>
      <div className="relative aspect-[1024/477] w-full overflow-hidden max-md:aspect-auto max-md:min-h-[540px]">
        <div className="absolute inset-0 bg-[#EFEFF1]">
          <div className="absolute inset-y-0 left-[38%] right-0 flex gap-3 pt-[11%] pr-6 pb-[8%] opacity-[0.82] max-md:left-3 max-md:pt-10 max-md:opacity-90">
            {COLUMNS.map((col) => (
              <div key={col.title} className="flex w-[248px] shrink-0 flex-col">
                <div className="mb-2.5 flex h-7 items-center gap-1.5 px-0.5">
                  <ColIcon kind={col.icon} />
                  <span className="text-[13px] font-medium tracking-tight text-[#24262B]">{col.title}</span>
                  <span className="text-[13px] tabular-nums text-[#9B9EA5]">{col.count}</span>
                  <span className="ml-auto flex items-center gap-1 text-[#B0B3B8]">
                    <PlusIcon className="size-3.5" />
                    <DotsIcon className="size-3.5" />
                  </span>
                </div>
                <div className="flex flex-col gap-2">
                  {col.cards.map((card) => (
                    <div
                      key={card.id}
                      className="rounded-[10px] border border-black/[0.06] bg-white px-3 py-2.5 shadow-[0_1px_0_rgba(0,0,0,0.03)]"
                    >
                      <div className="flex items-center justify-between gap-2">
                        <span className="text-[12px] tabular-nums tracking-tight text-[#8A8F98]">{card.id}</span>
                        <span
                          className="inline-flex size-[18px] shrink-0 items-center justify-center rounded-full text-[8px] font-medium text-white"
                          style={{ background: card.who.bg }}
                        >
                          {card.who.initial}
                        </span>
                      </div>
                      <div className="mt-1 text-[13px] leading-snug tracking-tight text-[#1A1A1A]">{card.title}</div>
                      <div className="mt-2 flex items-center gap-1.5">
                        <span className="text-[#C0C3C8]">
                          <SignalIcon />
                        </span>
                        {card.tags.map((tag) => (
                          <Tag key={tag.label} color={tag.color} label={tag.label} />
                        ))}
                        {"meta" in card && card.meta ? (
                          <span className="ml-auto text-[11px] tabular-nums text-[#9B9EA5]">↕ {card.meta}</span>
                        ) : null}
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            ))}
          </div>
        </div>

        <div
          className="pointer-events-none absolute inset-0"
          style={{
            background: [
              "linear-gradient(to right, #f7f7f5 0%, #f7f7f5 4%, transparent 10%, transparent 62%, rgba(247,247,245,0.45) 78%, #f7f7f5 100%)",
              "linear-gradient(to bottom, #f7f7f5 0%, rgba(247,247,245,0.92) 8%, transparent 18%, transparent 72%, rgba(247,247,245,0.7) 88%, #f7f7f5 100%)",
            ].join(", "),
          }}
        />

        <div className="absolute top-[11.1%] bottom-[7.5%] left-[4.9%] z-10 flex w-[32.5%] min-w-[280px] max-w-[340px] flex-col overflow-hidden rounded-[12px] border border-black/[0.08] bg-white shadow-[0_16px_48px_rgba(0,0,0,0.10)] max-sm:left-3 max-md:top-auto max-md:h-[78%] max-md:w-[calc(100%-24px)] max-md:min-w-0 max-md:max-w-none">
          <div className="flex h-11 shrink-0 items-center justify-between border-b border-black/[0.06] px-3.5">
            <div className="flex items-center gap-2">
              <SlackHash />
              <span className="text-[13px] font-semibold tracking-tight text-[#1A1A1A]">Thread</span>
              <span className="text-[13px] tracking-tight text-[#8A8F98]">#egress</span>
            </div>
            <span className="text-[#B0B3B8]">
              <DotsIcon />
            </span>
          </div>

          <div className="relative min-h-0 flex-1 overflow-hidden">
            <div className="space-y-5 px-3.5 py-4 max-md:space-y-3.5 max-md:py-3">
              {MESSAGES.map((msg) => (
                <div key={msg.name} className="flex gap-2.5">
                  <span
                    className="mt-0.5 inline-flex size-7 shrink-0 items-center justify-center rounded-[5px] text-[11px] font-medium text-white"
                    style={{ background: msg.bg }}
                  >
                    {msg.initial}
                  </span>
                  <div className="min-w-0">
                    <div className="flex items-baseline gap-2">
                      <span className="text-[13px] font-semibold tracking-tight text-[#1A1A1A]">{msg.name}</span>
                      <span className="text-[12px] text-[#9B9EA5]">{msg.time}</span>
                    </div>
                    <p className="mt-0.5 text-[13px] leading-[1.45] tracking-tight text-[#3C3F44]">{msg.body}</p>
                  </div>
                </div>
              ))}
            </div>
            <div className="pointer-events-none absolute inset-x-0 bottom-0 h-10 bg-gradient-to-t from-white to-transparent" />
          </div>

          <div className="px-3 pb-3">
            <div className="rounded-[10px] border border-black/[0.08] bg-[#FAFAFB] px-3 pt-3.5 pb-2">
              <div className="flex flex-wrap items-center gap-1.5 text-[13px] leading-5 text-[#3C3F44]">
                <span className="rounded-[5px] bg-[#5E6AD2]/18 px-1.5 py-px font-medium text-[#5E6AD2]">@firewall</span>
                <span>deny unknown destinations</span>
              </div>
              <div className="mt-3 flex items-center justify-between gap-2">
                <div className="flex items-center gap-0.5 text-[#9B9EA5]">
                  <span className="mr-0.5 flex size-7 items-center justify-center rounded-full border border-black/[0.08] bg-white">
                    <PlusIcon className="size-3.5" />
                  </span>
                  <span className="flex size-7 items-center justify-center">
                    <TypeIcon />
                  </span>
                  <span className="flex size-7 items-center justify-center">
                    <EmojiIcon />
                  </span>
                  <span className="flex size-7 items-center justify-center">
                    <AtIcon />
                  </span>
                  <span className="hidden size-7 items-center justify-center sm:flex">
                    <VideoIcon />
                  </span>
                  <span className="hidden size-7 items-center justify-center sm:flex">
                    <MicIcon />
                  </span>
                  <span className="mx-1 h-4 w-px bg-black/[0.1]" />
                  <span className="flex size-7 items-center justify-center">
                    <SlashIcon />
                  </span>
                </div>
                <span className="inline-flex h-7 items-stretch overflow-hidden rounded-[7px] bg-[#5E6AD2] text-white">
                  <span className="flex items-center px-2">
                    <PlaneIcon />
                  </span>
                  <span className="w-px bg-white/25" />
                  <span className="flex w-[22px] items-center justify-center">
                    <ChevronIcon />
                  </span>
                </span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
