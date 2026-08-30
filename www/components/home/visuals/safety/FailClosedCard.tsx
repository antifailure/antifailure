"use client";

import { u } from "../SafetyCards";

const INK = "#000000";
const BODY = "#61646b";
const FAINT = "#797d86";
const BRAND = "#33bf00";

const MESSAGES = [
  {
    name: "deploy-bot",
    initial: "D",
    tint: "#6b8cae",
    time: "8:55 PM",
    body: "twin-4c1 hit api.stripe.com:443",
    opacity: 0.28,
  },
  {
    name: "firewall",
    initial: "F",
    tint: "#5a8f6e",
    time: "8:56 PM",
    body: "No simulator. Egress denied. Deploy blocked.",
    opacity: 0.62,
  },
] as const;

const SIMULATORS = [
  { key: "payments", color: "#285D49" },
  { key: "email", color: "#33bf00" },
  { key: "sms", color: "#C4A035" },
  { key: "webhooks", color: "#C43D3D" },
  { key: "storage", color: "#61646b" },
] as const;

function PlusIcon({ size }: { size: string }) {
  return (
    <svg viewBox="0 0 16 16" style={{ width: size, height: size }} fill="none" aria-hidden>
      <path d="M8 3.4v9.2M3.4 8h9.2" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
    </svg>
  );
}

function EmojiIcon({ size }: { size: string }) {
  return (
    <svg viewBox="0 0 16 16" style={{ width: size, height: size }} fill="none" aria-hidden>
      <circle cx="8" cy="8" r="5" stroke="currentColor" strokeWidth="1.5" />
      <path
        d="M5.8 9.3c.55.85 1.35 1.25 2.2 1.25s1.65-.4 2.2-1.25"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
      />
      <circle cx="6.2" cy="6.6" r="0.8" fill="currentColor" />
      <circle cx="9.8" cy="6.6" r="0.8" fill="currentColor" />
    </svg>
  );
}

function AtIcon({ size }: { size: string }) {
  return (
    <svg viewBox="0 0 16 16" style={{ width: size, height: size }} fill="none" aria-hidden>
      <path d="M10.55 8a2.55 2.55 0 1 1-2.55-2.55" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
      <path
        d="M10.55 5.45V9.1a1.65 1.65 0 0 0 3.15-.75 5.7 5.7 0 1 0-2.05 4.4"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
      />
    </svg>
  );
}

function VideoIcon({ size }: { size: string }) {
  return (
    <svg viewBox="0 0 16 16" style={{ width: size, height: size }} fill="none" aria-hidden>
      <rect x="2.3" y="4.5" width="7.8" height="7" rx="1.6" stroke="currentColor" strokeWidth="1.5" />
      <path d="M10.1 7.15 13.5 5.5v5.1L10.1 8.9V7.15Z" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" />
    </svg>
  );
}

function MicIcon({ size }: { size: string }) {
  return (
    <svg viewBox="0 0 16 16" style={{ width: size, height: size }} fill="none" aria-hidden>
      <rect x="6.1" y="2.7" width="3.8" height="6.2" rx="1.9" stroke="currentColor" strokeWidth="1.5" />
      <path d="M4.5 8.15a3.5 3.5 0 0 0 7 0M8 11.7v1.6" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
    </svg>
  );
}

function SlashIcon({ size }: { size: string }) {
  return (
    <svg viewBox="0 0 16 16" style={{ width: size, height: size }} fill="none" aria-hidden>
      <rect x="2.6" y="2.6" width="10.8" height="10.8" rx="2.4" stroke="currentColor" strokeWidth="1.5" />
      <path d="M5 11 11 5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
    </svg>
  );
}

function PlaneIcon({ size }: { size: string }) {
  return (
    <svg viewBox="0 0 16 16" style={{ width: size, height: size }} fill="currentColor" aria-hidden>
      <path d="M2.2 7.55 13.7 3.2c.55-.22.9.28.62.78L10.1 13.4c-.22.5-.78.48-1.02-.04L7.4 9.55 3.05 8.4c-.58-.16-.58-.72.15-.85Z" />
    </svg>
  );
}

function ChevronIcon({ size }: { size: string }) {
  return (
    <svg viewBox="0 0 12 12" style={{ width: size, height: size }} fill="none" aria-hidden>
      <path d="M3 4.55 6 7.55 9 4.55" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function SimulatorGlyph({ kind, size }: { kind: (typeof SIMULATORS)[number]["key"]; size: string }) {
  const common = {
    style: { width: size, height: size },
    fill: "none" as const,
    "aria-hidden": true,
  };

  if (kind === "payments") {
    return (
      <svg viewBox="0 0 16 16" {...common}>
        <rect x="1.9" y="3.6" width="12.2" height="8.8" rx="1.9" stroke="currentColor" strokeWidth="1.4" />
        <path d="M2 6.6h12" stroke="currentColor" strokeWidth="1.4" />
        <path d="M4.6 9.9h2.6" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
      </svg>
    );
  }
  if (kind === "email") {
    return (
      <svg viewBox="0 0 16 16" {...common}>
        <rect x="1.9" y="3.5" width="12.2" height="9" rx="1.9" stroke="currentColor" strokeWidth="1.4" />
        <path d="m2.9 5.4 5.1 3.6 5.1-3.6" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    );
  }
  if (kind === "sms") {
    return (
      <svg viewBox="0 0 16 16" {...common}>
        <path
          d="M2.1 6.2A2.6 2.6 0 0 1 4.7 3.6h6.6a2.6 2.6 0 0 1 2.6 2.6v2.9a2.6 2.6 0 0 1-2.6 2.6H6.7L3.6 13.6v-2A2.6 2.6 0 0 1 2.1 9.1V6.2Z"
          stroke="currentColor"
          strokeWidth="1.4"
          strokeLinejoin="round"
        />
      </svg>
    );
  }
  if (kind === "webhooks") {
    return (
      <svg viewBox="0 0 16 16" {...common}>
        <path
          d="M8.9 1.9 3.5 8.8h3.9l-.3 5.3 5.4-6.9H8.6l.3-5.3Z"
          stroke="currentColor"
          strokeWidth="1.4"
          strokeLinejoin="round"
        />
      </svg>
    );
  }
  return (
    <svg viewBox="0 0 16 16" {...common}>
      <ellipse cx="8" cy="4.2" rx="5.3" ry="2.3" stroke="currentColor" strokeWidth="1.4" />
      <path d="M2.7 4.2v7.6c0 1.27 2.37 2.3 5.3 2.3s5.3-1.03 5.3-2.3V4.2" stroke="currentColor" strokeWidth="1.4" />
      <path d="M2.7 8c0 1.27 2.37 2.3 5.3 2.3s5.3-1.03 5.3-2.3" stroke="currentColor" strokeWidth="1.4" />
    </svg>
  );
}

function BrandMark({ size }: { size: string }) {
  return (
    <svg viewBox="0 0 18 18" style={{ width: size, height: size }} fill="none" aria-hidden>
      <path
        d="M1.8 6.4V1.8H6.4M11.6 1.8H16.2V6.4M16.2 11.6V16.2H11.6M6.4 16.2H1.8V11.6"
        stroke={BRAND}
        strokeWidth="2.1"
        strokeLinecap="square"
      />
    </svg>
  );
}

function ToolbarSlot({ children }: { children: React.ReactNode }) {
  return (
    <span className="flex shrink-0 items-center justify-center" style={{ width: u(15.5), height: u(15.5) }}>
      {children}
    </span>
  );
}

export function FailClosedCard() {
  const glyph = u(10.5);

  return (
    <div className="absolute inset-0 font-sans select-none" aria-hidden>
      {/* Thread: 26,0 234×166 r12 — distinct panel, does not bleed the card */}
      <div
        className="absolute overflow-hidden border border-black/[0.08] bg-white"
        style={{ left: u(26), top: u(0), width: u(234), height: u(166), borderRadius: u(12) }}
      >
        {MESSAGES.map((msg, i) => (
          <div
            className="absolute flex"
            key={msg.name}
            style={{ left: u(10.5), top: u(i === 0 ? 5.5 : 45.5), width: u(213), opacity: msg.opacity }}
          >
            <span
              className="flex shrink-0 items-center justify-center font-sans font-medium text-white"
              style={{
                width: u(18),
                height: u(18),
                marginTop: u(1),
                borderRadius: u(5),
                background: msg.tint,
                fontSize: u(7.5),
              }}
            >
              {msg.initial}
            </span>
            <div className="min-w-0" style={{ marginLeft: u(7.5) }}>
              <div className="flex items-baseline" style={{ gap: u(4), lineHeight: u(10.5) }}>
                <span className="font-sans font-semibold tracking-extra-tight" style={{ fontSize: u(8), color: INK }}>
                  {msg.name}
                </span>
                <span className="font-sans tracking-extra-tight" style={{ fontSize: u(7), color: FAINT }}>
                  {msg.time}
                </span>
              </div>
              <p
                className="font-sans tracking-extra-tight"
                style={{ fontSize: u(8), lineHeight: u(10.5), marginTop: u(0.5), color: BODY }}
              >
                {msg.body}
              </p>
            </div>
          </div>
        ))}

        <div
          className="absolute inset-x-0 top-0"
          style={{
            height: u(46),
            background: "linear-gradient(to bottom, #ffffff 0%, rgba(255,255,255,0.72) 45%, rgba(255,255,255,0) 100%)",
          }}
        />

        {/* Composer: 5.5,93 223×66 r10, docked inside the thread */}
        <div
          className="absolute border border-black/[0.08] bg-[#f7f7f5]"
          style={{
            left: u(5.5),
            top: u(93),
            width: u(223),
            height: u(66),
            borderRadius: u(10),
            paddingTop: u(10),
            paddingLeft: u(9.5),
            paddingRight: u(9),
          }}
        >
          <div className="flex items-center" style={{ gap: u(4), lineHeight: u(10.5) }}>
            <span
              className="font-sans font-medium tracking-extra-tight bg-[rgba(51,191,0,0.15)] text-[#33bf00]"
              style={{
                fontSize: u(8),
                lineHeight: u(10.5),
                borderRadius: u(3),
                paddingLeft: u(2.5),
                paddingRight: u(2.5),
              }}
            >
              @firewall
            </span>
            <span className="font-sans tracking-extra-tight" style={{ fontSize: u(8), color: INK }}>
              deny unknown destinations
            </span>
          </div>

          <div className="flex items-center" style={{ marginTop: u(20.5), height: u(15.5), color: FAINT }}>
            <span
              className="flex shrink-0 items-center justify-center rounded-full border border-black/[0.08] bg-white"
              style={{ width: u(15.5), height: u(15.5) }}
            >
              <PlusIcon size={u(7.5)} />
            </span>
            <span className="flex items-center" style={{ marginLeft: u(2.5) }}>
              <ToolbarSlot>
                <span className="font-sans font-semibold leading-none tracking-extra-tight" style={{ fontSize: u(7) }}>
                  Aa
                </span>
              </ToolbarSlot>
              <ToolbarSlot>
                <EmojiIcon size={glyph} />
              </ToolbarSlot>
              <ToolbarSlot>
                <AtIcon size={glyph} />
              </ToolbarSlot>
            </span>
            <span className="flex items-center" style={{ marginLeft: u(6.75) }}>
              <ToolbarSlot>
                <VideoIcon size={glyph} />
              </ToolbarSlot>
              <ToolbarSlot>
                <MicIcon size={glyph} />
              </ToolbarSlot>
            </span>
            <span className="flex items-center" style={{ marginLeft: u(7) }}>
              <ToolbarSlot>
                <SlashIcon size={glyph} />
              </ToolbarSlot>
            </span>

            <span
              className="ml-auto flex shrink-0 items-stretch overflow-hidden text-white"
              style={{ width: u(32.5), height: u(14), borderRadius: u(4), background: INK }}
            >
              <span className="flex flex-1 items-center justify-center">
                <PlaneIcon size={u(7.5)} />
              </span>
              <span className="w-px shrink-0 bg-white/25" />
              <span className="flex items-center justify-center" style={{ width: u(14) }}>
                <ChevronIcon size={u(6.5)} />
              </span>
            </span>
          </div>
        </div>
      </div>

      {/* Connector: 1px, centered under the thread */}
      <div className="absolute bg-black/[0.10]" style={{ left: u(143), top: u(166), width: u(1), height: u(12.5) }} />

      {/* Icon row: 36.5 circle + 178.5×36.5 capsule, sitting on the card */}
      <div
        className="absolute flex items-center justify-center rounded-full border border-black/[0.06] bg-[#f7f7f5]"
        style={{ left: u(32.5), top: u(178.5), width: u(36.5), height: u(36.5) }}
      >
        <BrandMark size={u(17)} />
      </div>

      <div
        className="absolute flex items-center rounded-full border border-black/[0.08] bg-white"
        style={{
          left: u(77),
          top: u(178.5),
          width: u(178.5),
          height: u(36.5),
          paddingLeft: u(12.5),
          paddingRight: u(12.5),
          gap: u(8.25),
        }}
      >
        {SIMULATORS.map((sim) => (
          <span
            className="flex shrink-0 items-center justify-center rounded-full"
            key={sim.key}
            style={{ width: u(24), height: u(24), background: `${sim.color}1F`, color: sim.color }}
          >
            <SimulatorGlyph kind={sim.key} size={u(12.5)} />
          </span>
        ))}
      </div>
    </div>
  );
}
