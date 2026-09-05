import { cn } from "@/lib/cn";

/**
 * The attempted-effect ledger, which is the only thing this section can
 * honestly show.
 *
 * What it replaced was a pixel copy of two other companies' products: Slack's
 * four colour hash mark drawn path by path, a channel header, three invented
 * teammates in coloured initial circles, a composer with seven decorative
 * icons that meant nothing, a send button in #5E6AD2 which is Linear's indigo,
 * and behind it a three column board of cards fading out under a gradient that
 * happened to erase the "Blocked" column, the one the heading is about. The
 * page above it draws Antifailure's own artifacts. This drew somebody else's
 * chrome and put a trademark on the marketing site to do it.
 *
 * The tags were the same problem one level down. Colour was cycled by category
 * rather than by meaning, so `Webhook` was green under Captured and red under
 * Blocked: the same word, the same product, two colours, encoding a
 * disposition the column already stated.
 *
 * So: one table, one artifact, one code path at every width. The disposition
 * is stated once per group instead of once per row, because the reason really
 * is a property of the group. Green is reserved for blocked, which is the
 * outcome the section is selling, and nothing else on the figure is coloured.
 */

const GROUPS = [
  {
    title: "Simulated",
    state: "empty" as const,
    reason: "answered by the clone-local simulator",
    rows: [
      { at: "20:55:41", id: "CHG-184", attempt: "POST /v1/charges $49.00", via: "Stripe" },
      { at: "20:55:44", id: "CHG-185", attempt: "Retry charge on checkout", via: "Stripe" },
      { at: "20:56:02", id: "CHG-190", attempt: "Duplicate charge attempt", via: "Stripe" },
      { at: "20:56:19", id: "INV-044", attempt: "Refund path on cancel", via: "Stripe" },
    ],
  },
  {
    title: "Captured",
    state: "half" as const,
    reason: "stored whole, never delivered",
    rows: [
      { at: "20:56:31", id: "MAIL-91", attempt: "Order #4182 receipt", via: "Email" },
      { at: "20:56:40", id: "WH-220", attempt: "hooks.slack.com", via: "Webhook" },
      { at: "20:57:03", id: "MAIL-92", attempt: "Password reset for a masked address", via: "Email" },
    ],
  },
  {
    title: "Blocked",
    state: "full" as const,
    reason: "no rule for the destination, denied",
    rows: [
      { at: "20:57:11", id: "DNS-018", attempt: "api.prod.internal", via: "Production" },
      { at: "20:57:18", id: "TCP-443", attempt: "18.4.2.9 on 443", via: "ip-bypass" },
      { at: "20:57:26", id: "WH-441", attempt: "hooks.prod.internal", via: "Webhook" },
    ],
  },
] as const;

const ATTEMPTS = GROUPS.reduce((n, group) => n + group.rows.length, 0);

/**
 * Three states of one glyph, not three icons.
 *
 * The fill says how far the effect got: nothing left the twin, it left and was
 * held, or it was refused at the boundary. Only the last is green, because the
 * heading of this section is about the last.
 */
function StateGlyph({ state }: { state: "empty" | "half" | "full" }) {
  if (state === "full") {
    return (
      <svg viewBox="0 0 14 14" className="size-3.5 shrink-0" fill="none" aria-hidden>
        <circle cx="7" cy="7" r="5.4" fill="#2F8A5F" />
        <path
          d="M4.6 7.1 6.3 8.8 9.5 5.4"
          stroke="#fff"
          strokeWidth="1.5"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
    );
  }
  return (
    <svg viewBox="0 0 14 14" className="size-3.5 shrink-0" fill="none" aria-hidden>
      <circle cx="7" cy="7" r="5.4" stroke="#9B9EA5" strokeWidth="1.4" />
      {state === "half" ? <path d="M7 1.6A5.4 5.4 0 0 1 7 12.4Z" fill="#9B9EA5" /> : null}
    </svg>
  );
}

export function FailClosedScene() {
  return (
    <div
      data-scene="fail-closed"
      className="pointer-events-none w-full overflow-hidden rounded-[12px] border border-black/[0.08] bg-white select-none"
      aria-hidden
    >
      <div className="flex items-baseline justify-between gap-4 border-b border-black/[0.08] px-5 py-3.5 max-md:px-3.5 max-md:py-3">
        <span className="font-mono text-[11px] font-medium uppercase tracking-[0.14em] text-[#8A8F98] max-md:text-[10px]">
          Egress ledger
        </span>
        {/* The count is the rows, and it says so by being derived from them.
            The board this replaced carried 8, 4 and 4 in its column headers
            over 4, 3 and 3 cards, so the figure disagreed with itself by six. */}
        <span className="text-[13px] tabular-nums tracking-tight text-[#6B6F76] max-md:text-[12px]">
          {ATTEMPTS} attempts, none reached production
        </span>
      </div>

      {GROUPS.map((group) => (
        <section key={group.title}>
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-black/[0.06] bg-[#FAFAFB] px-5 py-2.5 max-md:px-3.5 max-md:py-2">
            <span className="flex items-center gap-2">
              <StateGlyph state={group.state} />
              <span
                className={cn(
                  "font-mono text-[11px] font-medium uppercase tracking-[0.12em] max-md:text-[10px]",
                  group.state === "full" ? "text-[#2F8A5F]" : "text-[#4A4E55]",
                )}
              >
                {group.title}
              </span>
              <span className="font-mono text-[11px] tabular-nums text-[#9B9EA5] max-md:text-[10px]">
                {group.rows.length}
              </span>
            </span>
            <span className="text-[13px] tracking-tight text-[#6B6F76] max-md:text-[12px]">
              {group.reason}
            </span>
          </div>

          <ul>
            {group.rows.map((row) => (
              <li
                key={row.id}
                className="flex items-baseline gap-4 border-b border-black/[0.05] px-5 py-2.5 last:border-b-0 max-md:gap-3 max-md:px-3.5 max-md:py-2"
              >
                {/* The clock is the column that makes this a ledger rather
                    than three fields stretched over 1400 pixels. It is hidden
                    on a phone, where the row has no room for a fourth thing
                    and the order of the rows already carries the sequence. */}
                <span className="w-[64px] shrink-0 font-mono text-[11px] tabular-nums tracking-tight text-[#B0B3B8] max-md:hidden">
                  {row.at}
                </span>
                <span className="w-[74px] shrink-0 font-mono text-[11px] tabular-nums tracking-tight text-[#9B9EA5] max-md:w-[62px] max-md:text-[10px]">
                  {row.id}
                </span>
                <span className="min-w-0 flex-1 truncate text-[13px] tracking-tight text-[#1A1A1A] max-md:text-[12px]">
                  {row.attempt}
                </span>
                <span className="shrink-0 font-mono text-[11px] tracking-tight text-[#6B6F76] max-md:text-[10px]">
                  {row.via}
                </span>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}
