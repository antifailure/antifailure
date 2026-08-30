import type { Metadata, Viewport } from "next";
import { GeistMono } from "geist/font/mono";
import { GeistSans } from "geist/font/sans";
import "./globals.css";

// Both faces come from a package rather than from a font service. That is not
// a preference: this application is built inside a sandbox with no route to
// the internet, so a build step that fetches a stylesheet from a CDN is a
// build that fails there and nowhere else.

export const metadata: Metadata = {
  title: {
    default: "Antifailure",
    template: "%s — Antifailure",
  },
  description:
    "Environments, runs, network policy, and the audit log for one organization.",
  robots: { index: false, follow: false },
};

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  themeColor: "#f7f7f5",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className={`${GeistSans.variable} ${GeistMono.variable}`}>
      <body className="font-sans antialiased">{children}</body>
    </html>
  );
}
