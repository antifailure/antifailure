"use client";

import { useState } from "react";
import { useChrome, type SheetId } from "./Chrome";
import { Chevron, DiscordIcon, GitHubIcon, Wordmark } from "./icons";

type MenuItem = {
  title: string;
  sub: string;
  href?: string;
  sheet?: SheetId;
};

const productItems: MenuItem[] = [
  { title: "Isolated Twin", sub: "Temporary copy of the application stack", href: "/#twins" },
  { title: "Safe State", sub: "Sanitized production-shaped Postgres", href: "/#cards" },
  { title: "Side-Effect Firewall", sub: "Simulators instead of real-world side effects", href: "/#firewall" },
  { title: "Workload Studio", sub: "Observed, deterministic, and Crowdi traffic", href: "/#cards" },
  { title: "Migration Safety", sub: "Locks, plans, rollback feasibility", href: "/#migration" },
  { title: "Safety Report", sub: "Pass, warning, or block on the PR", href: "/#dashboard" },
];

const solutionItems: MenuItem[] = [
  { title: "B2B SaaS", sub: "Daily deploys, migration anxiety", href: "/#migration" },
  { title: "Fintech infrastructure", sub: "Billing and ledger-safe twins", href: "/#migration" },
  { title: "E-commerce", sub: "Checkout under production-shaped load", href: "/#migration" },
  { title: "Marketplaces", sub: "Queues, workers, dual-writes", href: "/#migration" },
  { title: "Developer tools", sub: "Schema changes on large tables", href: "/#migration" },
];

const resourceItems: MenuItem[] = [
  { title: "Product brief", sub: "Thesis, wedge, and execution blueprint", sheet: "brief" },
  { title: "Killer demo", sub: "Risky defaulted column + duplicate billing", href: "/#dashboard" },
  { title: "Open source", sub: "Customer agent, adapters, cleanup", sheet: "community" },
  { title: "Security", sub: "Fail closed. Data stays in your boundary", sheet: "security" },
];

function Menu({ label, items }: { label: string; items: MenuItem[] }) {
  const [open, setOpen] = useState(false);
  const { openSheet } = useChrome();

  return (
    <div
      className="relative"
      onMouseEnter={() => setOpen(true)}
      onMouseLeave={() => setOpen(false)}
    >
      <button type="button" className="flex items-center gap-1 text-[13.5px] text-white/90 hover:text-white">
        {label}
        <Chevron className="h-2.5 w-2.5 text-white/70" />
      </button>
      {open && (
        <div className="absolute left-1/2 top-full z-50 mt-3 w-[320px] -translate-x-1/2 rounded-xl border border-white/10 bg-[#0b0b0b] p-2 shadow-2xl">
          {items.map((item) => {
            const className = "block w-full rounded-lg px-3 py-2.5 text-left hover:bg-white/5";
            const body = (
              <>
                <div className="text-[13px] text-white">{item.title}</div>
                <div className="text-[12px] text-[#a1a1aa]">{item.sub}</div>
              </>
            );
            if (item.sheet) {
              return (
                <button
                  key={item.title}
                  type="button"
                  className={className}
                  onClick={() => {
                    openSheet(item.sheet!);
                    setOpen(false);
                  }}
                >
                  {body}
                </button>
              );
            }
            return (
              <a
                key={item.title}
                href={item.href}
                className={className}
                onClick={() => setOpen(false)}
              >
                {body}
              </a>
            );
          })}
        </div>
      )}
    </div>
  );
}

export function SiteHeader() {
  const { openSheet } = useChrome();

  return (
    <header className="sticky top-0 z-50 bg-black">
      <div className="flex h-[58px] items-center justify-between px-5 lg:px-8">
        <Wordmark />

        <nav className="hidden items-center gap-7 md:flex">
          <Menu label="Product" items={productItems} />
          <Menu label="Solutions" items={solutionItems} />
          <a href="/docs" className="text-[13.5px] text-white/90 hover:text-white">
            Docs
          </a>
          <button
            type="button"
            className="text-[13.5px] text-white/90 hover:text-white"
            onClick={() => openSheet("pricing")}
          >
            Pricing
          </button>
          <Menu label="Resources" items={resourceItems} />
        </nav>

        <div className="flex items-center gap-4">
          <button
            type="button"
            className="hidden items-center gap-1.5 text-[13px] text-white/90 hover:text-white sm:flex"
            onClick={() => openSheet("community")}
          >
            <DiscordIcon />
            Discord
          </button>
          <a
            href="https://github.com/antifailure"
            target="_blank"
            rel="noreferrer"
            className="hidden items-center gap-1.5 text-[13px] text-white/90 hover:text-white sm:flex"
          >
            <GitHubIcon />
            GitHub
          </a>
          <a
            href="/signin"
            className="inline-flex h-8 items-center rounded-full border border-white px-3.5 text-[13px] text-white"
          >
            Log in
          </a>
          <a
            href="/signup"
            className="inline-flex h-8 items-center rounded-full bg-white px-3.5 text-[13px] font-medium text-black"
          >
            Sign up
          </a>
        </div>
      </div>
    </header>
  );
}
