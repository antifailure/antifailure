"use client";

import { Shell } from "@/components/Shell";
import { SessionProvider } from "@/components/session";

/**
 * The chrome, mounted once for every page in this group.
 *
 * It was inside each page first, and that was wrong in a way only a browser
 * shows: a layout is preserved across a client-side navigation and a page is
 * not, so every click on the sidebar unmounted the shell, refetched the
 * session, and blanked the whole window -- navigation rail included -- until
 * it came back. It looked like the application crashed on every navigation.
 *
 * /device is deliberately NOT in this group. It is reached from a terminal by
 * somebody who may not be signed in and may have no organization, and wrapping
 * it in the chrome would put a navigation rail around a consent screen.
 */
export default function AppLayout({ children }: { children: React.ReactNode }) {
  return (
    <SessionProvider>
      <Shell>{children}</Shell>
    </SessionProvider>
  );
}
