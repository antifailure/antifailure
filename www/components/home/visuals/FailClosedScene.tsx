import { cn } from "@/lib/cn";

const COLUMNS = [
  {
    title: "Simulated",
    icon: "todo" as const,
    cards: [
      {
        id: "CHG-184",
        title: "POST /v1/charges $49.00",
        tags: [{ label: "Stripe", color: "#B0B3B8" }],
        who: { initial: "L", bg: "#E4E5E7" },
      },
      {
        id: "CHG-185",
        title: "Retry charge on checkout",
        tags: [
          { label: "Stripe", color: "#B0B3B8" },
          { label: "Clone-local", color: "#B0B3B8" },
        ],
        who: { initial: "D", bg: "#E4E5E7" },
      },
      {
        id: "CHG-190",
        title: "Duplicate charge attempt",
        tags: [{ label: "Stripe", color: "#B0B3B8" }],
        who: { initial: "A", bg: "#E4E5E7" },
      },
      {
        id: "INV-044",
        title: "Refund path on cancel",
        tags: [{ label: "Stripe", color: "#B0B3B8" }],
        who: { initial: "L", bg: "#E4E5E7" },
      },
    ],
  },
  {
    title: "Captured",
    icon: "progress" as const,
    cards: [
      {
        id: "MAIL-91",
        title: "Order #4182 receipt",
        tags: [{ label: "Email", color: "#B0B3B8" }],
        who: { initial: "D", bg: "#E4E5E7" },
      },
      {
        id: "WH-220",
        title: "slack.hooks · store only",
        tags: [
          { label: "Webhook", color: "#B0B3B8" },
          { label: "Preview", color: "#B0B3B8" },
        ],
        who: { initial: "A", bg: "#E4E5E7" },
      },
      {
        id: "MAIL-92",
        title: "Password reset never sent",
        tags: [{ label: "Email", color: "#B0B3B8" }],
        who: { initial: "L", bg: "#E4E5E7" },
      },
    ],
  },
  {
    title: "Blocked",
    icon: "done" as const,
    cards: [
      {
        id: "DNS-018",
        title: "api.prod.internal",
        tags: [{ label: "Production", color: "#2F8A5F" }],
        who: { initial: "A", bg: "#E4E5E7" },
      },
      {
        id: "TCP-443",
        title: "18.4.2.9 · ip-bypass",
        tags: [{ label: "Unknown", color: "#2F8A5F" }],
        who: { initial: "L", bg: "#E4E5E7" },
      },
      {
        id: "WH-441",
        title: "hooks.prod.internal",
        tags: [{ label: "Webhook", color: "#2F8A5F" }],
        who: { initial: "D", bg: "#E4E5E7" },
      },
    ],
  },
] as const;

const MESSAGES = [
  {
    name: "lena",
    initial: "L",
    bg: "#E4E5E7",
    time: "8:55 PM",
    body: "Twin posted POST /v1/charges $49.00. Simulated against clone-local Stripe. Not live.",
  },
  {
    name: "didier",
    initial: "D",
    bg: "#E4E5E7",
    time: "8:55 PM",
    body: "Yes, SendGrid /v3/mail/send is captured. MIME stored. Never delivered.",
  },
  {
    name: "andreas",
    initial: "A",
    bg: "#E4E5E7",
    time: "8:56 PM",
    body: "hooks.prod.internal is unresolved. Unknown destination, denied. Fail closed.",
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

function ChannelHash() {
  return (
    <svg viewBox="0 0 16 16" className="size-[15px]" fill="none" aria-hidden>
      <path
        d="M6.2 2.2 4.9 13.8M11.1 2.2 9.8 13.8M2.6 5.9h11M2.2 10.1h11"
        stroke="#8A8F98"
        strokeWidth="1.5"
        strokeLinecap="round"
      />
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

/**
 * The phone reading of the same truth.
 *
 * A three-column board with a chat window floating over it is a shape that only
 * works at desk width. Squeezed onto a phone the columns disappeared behind the
 * card and the section lost its evidence. The board's rows are the evidence, so
 * on a phone they become what they are: a ledger of attempted effects, each one
 * with what the firewall did about it.
 */
const LEDGER = COLUMNS.flatMap((col) =>
  col.cards.slice(0, col.title === "Blocked" ? 3 : 2).map((card) => ({
    id: card.id,
    title: card.title,
    tag: card.tags[0],
    disposition: col.title.toLowerCase(),
    icon: col.icon,
  })),
);

function EgressLedger() {
  return (
    <div className="overflow-hidden rounded-[12px] border border-black/[0.08] bg-white">
      <div className="flex items-center justify-between border-b border-black/[0.06] px-3.5 py-2.5">
        <span className="font-mono text-[10px] font-medium uppercase tracking-[0.14em] text-[#8A8F98]">
          Egress ledger
        </span>
        <span className="text-[12px] tabular-nums tracking-tight text-[#8A8F98]">
          {COLUMNS.reduce((n, col) => n + col.cards.length, 0)} attempts
        </span>
      </div>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 border-b border-black/[0.06] bg-[#FAFAFB] px-3.5 py-2">
        {COLUMNS.map((col) => (
          <span key={col.title} className="flex items-center gap-1.5 text-[12px] tracking-tight text-[#6B6F76]">
            <ColIcon kind={col.icon} />
            {col.title}
            <span className="tabular-nums text-[#9B9EA5]">{col.cards.length}</span>
          </span>
        ))}
      </div>
      <ul>
        {LEDGER.map((row) => (
          <li
            key={row.id}
            className="flex items-start justify-between gap-3 border-b border-black/[0.05] px-3.5 py-2.5 last:border-b-0"
          >
            <span className="min-w-0">
              <span className="block text-[13px] leading-snug tracking-tight text-[#1A1A1A]">{row.title}</span>
              <span className="mt-1 flex items-center gap-1.5">
                <span className="font-mono text-[10px] tabular-nums tracking-tight text-[#9B9EA5]">{row.id}</span>
                <Tag color={row.tag.color} label={row.tag.label} />
              </span>
            </span>
            <span
              className={cn(
                "mt-0.5 shrink-0 font-mono text-[10px] uppercase tracking-[0.1em]",
                row.disposition === "blocked" ? "text-[#2F8A5F]" : "text-[#9B9EA5]",
              )}
            >
              {row.disposition}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

export function FailClosedScene() {
  return (
    <div data-scene="fail-closed" className="pointer-events-none relative w-full select-none" aria-hidden>
      <div className="hidden max-xl:mt-1 max-xl:flex max-xl:flex-col max-xl:gap-4">
        <EgressLedger />
        <SlackThread mobile />
      </div>
      <div className="relative aspect-[1024/477] w-full overflow-hidden max-xl:hidden">
        <div className="absolute inset-0 bg-[#EFEFF1]">
          {/* THE THIRD COLUMN NEVER FIT, AT ANY WIDTH. Three columns pinned
              to 248 pixels with a 12 pixel gap need 768, and this area is 536
              at 1280, 635 at 1440 and 734 even at 1920, so "Blocked" hung 232,
              133 and 34 pixels past the stage and was clipped mid word. The
              one disposition the heading of this section actually promises was
              the one nobody could read, on every screen there is. The columns
              share the row now instead of demanding a width it never has. */}
          <div className="absolute inset-y-0 left-[36%] right-0 flex gap-3 pt-[11%] pr-5 pb-[8%] opacity-[0.82]">
            {COLUMNS.map((col) => (
              <div key={col.title} className="flex min-w-0 flex-1 flex-col">
                <div className="mb-2.5 flex h-7 items-center gap-1.5 px-0.5">
                  <ColIcon kind={col.icon} />
                  <span className="text-[13px] font-medium tracking-tight text-[#24262B]">{col.title}</span>
                  <span className="text-[13px] tabular-nums text-[#9B9EA5]">{col.cards.length}</span>
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
                          className="inline-flex size-[18px] shrink-0 items-center justify-center rounded-full text-[8px] font-medium text-[#6B6F76]"
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
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            ))}
          </div>
        </div>

        <div
          className="pointer-events-none absolute inset-0 max-xl:hidden"
          style={{
            background: [
              // The stop at 78 percent sat on top of the third column, so the
              // one disposition the heading actually promises, Blocked, was
              // the one the reader could not make out. It fades past the
              // content now rather than over it.
              "linear-gradient(to right, #f7f7f5 0%, #f7f7f5 3%, transparent 9%, transparent 92%, rgba(247,247,245,0.55) 97%, #f7f7f5 100%)",
              "linear-gradient(to bottom, #f7f7f5 0%, rgba(247,247,245,0.92) 8%, transparent 18%, transparent 72%, rgba(247,247,245,0.7) 88%, #f7f7f5 100%)",
            ].join(", "),
          }}
        />

        <SlackThread />
      </div>
    </div>
  );
}

/**
 * The thread that reads the ledger back in human words.
 *
 * On a wide screen it floats over the board. On a phone it sits under the
 * ledger as its own block, at content height, because a card pinned to 78% of
 * a fixed-height stage cut its own last message in half.
 */
function SlackThread({ mobile }: { mobile?: boolean }) {
  return (
    <div
      className={cn(
        "flex flex-col overflow-hidden rounded-[12px] border border-black/[0.08] bg-white",
        mobile
          ? "w-full"
          : "absolute top-[11.1%] bottom-[7.5%] left-[4.9%] z-10 w-[32.5%] min-w-[280px] max-w-[340px] shadow-[0_16px_48px_rgba(0,0,0,0.10)]",
      )}
    >
          <div className="flex h-11 shrink-0 items-center justify-between border-b border-black/[0.06] px-3.5">
            <div className="flex items-center gap-2">
              <ChannelHash />
              <span className="text-[13px] font-semibold tracking-tight text-[#1A1A1A]">Checkout</span>
              <span className="text-[13px] tracking-tight text-[#8A8F98]">#egress</span>
            </div>
          </div>

          <div className="relative min-h-0 flex-1 overflow-hidden">
            <div className="space-y-5 px-3.5 py-4 max-md:space-y-3.5 max-md:py-3">
              {MESSAGES.map((msg) => (
                <div key={msg.name} className="flex gap-2.5">
                  <span
                    className="mt-0.5 inline-flex size-7 shrink-0 items-center justify-center rounded-[5px] text-[11px] font-medium text-[#6B6F76]"
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
            <div className="pointer-events-none absolute inset-x-0 bottom-0 h-10 bg-gradient-to-t from-white to-transparent max-xl:hidden" />
          </div>

          <div className="px-3 pb-3">
            <div className="rounded-[10px] border border-black/[0.08] bg-[#FAFAFB] px-3 pt-3.5 pb-2">
              <div className="flex flex-wrap items-center gap-1.5 text-[13px] leading-5 text-[#3C3F44]">
                <span className="rounded-[5px] bg-[#33bf00]/[0.14] px-1.5 py-px font-medium text-[#2F8A5F]">@firewall</span>
                <span>deny unknown destinations</span>
              </div>
              {/* NO COMPOSER TOOLBAR. It carried a plus, a type control,
                  an emoji, an at sign, a camera, a microphone and a slash
                  command, then a send button with its own dropdown chevron.
                  Nine controls, none of which do anything, in a chat client
                  this company does not make. The one line above is the whole
                  point of the panel: somebody names the rule, and the ledger
                  behind it obeys. Everything else was set dressing that made
                  the figure read as a screenshot of another product. */}
            </div>
          </div>
    </div>
  );
}
