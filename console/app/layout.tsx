import type { Metadata } from "next";
import { GeistMono } from "geist/font/mono";
import { GeistSans } from "geist/font/sans";
import "./globals.css";

export const metadata: Metadata = {
  /*
   * The screen's own name first, because that is the half a tab still shows
   * once it is one of six. Every route rendered the bare word "Antifailure",
   * so every tab, every history entry and every tab search result for somebody
   * watching a run across several tabs was the identical string.
   *
   * Each route carries its own name in a `layout.tsx` beside its `page.tsx`,
   * rather than in the page: every page in here is a client component, and a
   * client component cannot export metadata. A layout can, it is a server
   * component even under the client layout above it, and it is static, so it
   * survives `output: "export"` where a `document.title` written after
   * hydration would not be in the HTML at all.
   *
   * `default` is what the root and anything without a layout of its own get.
   */
  title: {
    default: "Antifailure",
    template: "%s · Antifailure",
  },
  description: "The Antifailure control plane.",
  // A console has nothing a search engine should hold. Every page behind it is
  // a tenant's data and every page in front of it is a sign-in form.
  robots: { index: false, follow: false },
  icons: { icon: "/icon.svg" },
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className={`${GeistSans.variable} ${GeistMono.variable}`}>
      <body className="font-sans antialiased">{children}</body>
    </html>
  );
}
