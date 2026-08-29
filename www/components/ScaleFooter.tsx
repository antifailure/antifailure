"use client";

import type { ReactNode } from "react";
import { Wordmark } from "./icons";
import { useChrome, type SheetId } from "./Chrome";

export function ScaleFooter() {
  const { openSheet } = useChrome();

  return (
    <footer className="bg-[#f7f7f5] px-8 pb-16 pt-16 lg:px-16">
      <div className="grid gap-12 lg:grid-cols-[minmax(260px,1.45fr)_repeat(4,minmax(0,0.9fr))] lg:gap-10">
        <div>
          <Wordmark />
          <p className="mt-3 max-w-[280px] text-[13.5px] leading-5 text-black/45">
            Pre-production deployment safety.
          </p>
          <div
            id="community"
            className="mt-14 flex items-center gap-2 text-[13.5px] text-black"
          >
            <span className="h-[7px] w-[7px] rounded-full bg-[#33bf00]" />
            Design-partner waitlist open.
          </div>
          <p className="mt-6 max-w-[300px] text-[11px] leading-[1.55] text-black/35">
            © Antifailure 2026. All rights reserved. Pre-production deployment safety is a
            product category, not a guarantee that every production incident is predicted. The
            engine is open source; the enterprise edition is separately licensed.
          </p>
          <div className="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-[12px] text-black/40">
            <FooterAction onClick={() => openSheet("privacy")}>Privacy Notice</FooterAction>
            <FooterAction onClick={() => openSheet("terms")}>Terms of Use</FooterAction>
            <FooterAction onClick={() => openSheet("platform-terms")}>Platform Terms</FooterAction>
            <FooterAction onClick={() => openSheet("data-boundary")}>Data boundary</FooterAction>
          </div>
        </div>

        <FooterCol
          title="Company"
          links={[
            { label: "About", sheet: "brief" },
            { label: "Contact Sales", href: "/signup" },
            { label: "Security", sheet: "security" },
            { label: "Design partners", sheet: "pricing" },
            { label: "Trust model", href: "/#trust" },
          ]}
          onSheet={openSheet}
        />

        <FooterCol
          title="Resources"
          links={[
            { label: "Docs", href: "/docs" },
            { label: "Database providers", href: "/docs/providers/overview" },
            { label: "Migration safety", href: "/#migration" },
            { label: "Isolated twins", href: "/#twins" },
            { label: "Side-Effect Firewall", href: "/#firewall" },
            { label: "Safety report", href: "/#dashboard" },
            { label: "Pricing", sheet: "pricing" },
          ]}
          onSheet={openSheet}
        />

        <div>
          <ColTitle>Community</ColTitle>
          <ul className="mt-4 space-y-3 text-[13.5px] text-black">
            <CommunityItem icon={<WaitlistIcon />} href="/signup">
              Sign up
            </CommunityItem>
            <CommunityItem icon={<BriefIcon />} onClick={() => openSheet("brief")}>
              Product brief
            </CommunityItem>
            <CommunityItem icon={<SourceIcon />} onClick={() => openSheet("community")}>
              Open-source plan
            </CommunityItem>
            <CommunityItem icon={<DocsIcon />} href="/docs">
              Docs
            </CommunityItem>
            <CommunityItem icon={<SalesIcon />} href="/signup">
              Contact sales
            </CommunityItem>
          </ul>
        </div>

        <div>
          <ColTitle>Trust</ColTitle>
          <ul className="mt-4 space-y-3 text-[13.5px]">
            <TrustRow label="Data plane" status="Customer-hosted" href="/#trust" />
            <TrustRow label="Egress" status="Fail closed" href="/#firewall" />
            <TrustRow label="Snapshots" status="In-boundary" sheet="data-boundary" onSheet={openSheet} />
            <TrustRow label="Cleanup" status="Required" href="/#twins" />
            <TrustRow label="Verdict" status="Pass / warn / block" href="/#dashboard" />
            <li>
              <button
                type="button"
                className="text-black hover:text-black/70"
                onClick={() => openSheet("security")}
              >
                Security
              </button>
            </li>
          </ul>
        </div>
      </div>
    </footer>
  );
}

function ColTitle({ children }: { children: string }) {
  return (
    <div className="text-[11px] font-medium uppercase tracking-[0.16em] text-black/40">
      {children}
    </div>
  );
}

function FooterAction({
  children,
  onClick,
}: {
  children: string;
  onClick: () => void;
}) {
  return (
    <button type="button" className="hover:text-black/70" onClick={onClick}>
      {children}
    </button>
  );
}

type FooterLink = {
  label: string;
  href?: string;
  sheet?: SheetId;
};

function FooterCol({
  title,
  links,
  onSheet,
}: {
  title: string;
  links: FooterLink[];
  onSheet: (id: SheetId) => void;
}) {
  return (
    <div>
      <ColTitle>{title}</ColTitle>
      <ul className="mt-4 space-y-3 text-[13.5px] text-black">
        {links.map((l) => (
          <li key={l.label}>
            {l.href ? (
              <a href={l.href} className="hover:text-black/70">
                {l.label}
              </a>
            ) : (
              <button
                type="button"
                className="text-left hover:text-black/70"
                onClick={() => {
                  if (l.sheet) onSheet(l.sheet);
                }}
              >
                {l.label}
              </button>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}

function TrustRow({
  label,
  status,
  href,
  sheet,
  onSheet,
}: {
  label: string;
  status: string;
  href?: string;
  sheet?: SheetId;
  onSheet?: (id: SheetId) => void;
}) {
  const body = (
    <>
      <span className="text-black">{label}</span>
      <span className="shrink-0 text-black/40">{status}</span>
    </>
  );
  const cls = "flex w-full items-baseline justify-between gap-3 text-left hover:text-black/70";
  if (href) {
    return (
      <li>
        <a href={href} className={cls}>
          {body}
        </a>
      </li>
    );
  }
  return (
    <li>
      <button type="button" className={cls} onClick={() => sheet && onSheet?.(sheet)}>
        {body}
      </button>
    </li>
  );
}

function CommunityItem({
  icon,
  children,
  onClick,
  href,
}: {
  icon: ReactNode;
  children: string;
  onClick?: () => void;
  href?: string;
}) {
  const className = "flex items-center gap-2.5 hover:text-black/70";
  return (
    <li>
      {href ? (
        <a href={href} className={className}>
          {icon}
          {children}
        </a>
      ) : (
        <button type="button" className={className} onClick={onClick}>
          {icon}
          {children}
        </button>
      )}
    </li>
  );
}

function WaitlistIcon() {
  return (
    <svg viewBox="0 0 16 16" className="h-3.5 w-3.5 shrink-0" fill="none" aria-hidden>
      <rect x="1.5" y="3.5" width="13" height="9" rx="1.2" stroke="currentColor" strokeWidth="1.2" />
      <path d="M2 4.2 8 9.2 14 4.2" stroke="currentColor" strokeWidth="1.2" />
    </svg>
  );
}

function BriefIcon() {
  return (
    <svg viewBox="0 0 16 16" className="h-3.5 w-3.5 shrink-0" fill="none" aria-hidden>
      <rect x="3.5" y="2" width="9" height="12" rx="1" stroke="currentColor" strokeWidth="1.2" />
      <path d="M6 5.5h4M6 8h4M6 10.5h2.5" stroke="currentColor" strokeWidth="1.2" />
    </svg>
  );
}

function SourceIcon() {
  return (
    <svg viewBox="0 0 16 16" className="h-3.5 w-3.5 shrink-0" fill="none" aria-hidden>
      <path d="M6 4.5 2.8 8 6 11.5M10 4.5 13.2 8 10 11.5" stroke="currentColor" strokeWidth="1.2" />
    </svg>
  );
}

function DocsIcon() {
  return (
    <svg viewBox="0 0 16 16" className="h-3.5 w-3.5 shrink-0" fill="none" aria-hidden>
      <path d="M3.5 3.2h5.2L12.5 7v6.3H3.5V3.2Z" stroke="currentColor" strokeWidth="1.2" />
      <path d="M8.6 3.4V7h3.6" stroke="currentColor" strokeWidth="1.2" />
    </svg>
  );
}

function SalesIcon() {
  return (
    <svg viewBox="0 0 16 16" className="h-3.5 w-3.5 shrink-0" fill="none" aria-hidden>
      <circle cx="8" cy="5.2" r="2.2" stroke="currentColor" strokeWidth="1.2" />
      <path d="M3.4 13c.7-2.2 2.4-3.3 4.6-3.3s3.9 1.1 4.6 3.3" stroke="currentColor" strokeWidth="1.2" />
    </svg>
  );
}
