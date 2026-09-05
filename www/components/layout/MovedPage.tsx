import type { Metadata } from "next";
import Link from "next/link";

/**
 * A page that has moved, for a host that serves the 301 and a client that may
 * not get one.
 *
 * The production host redirects these in public/staticwebapp.config.json, so
 * almost nobody reaches the markup below. It exists anyway for two reasons.
 * The build is `output: "export"`, which refuses next.config redirects because
 * there is no server to evaluate them, so the app itself has no other way to
 * answer these paths. And a preview or a local `next start` serves the static
 * files without the host config, where a missing page is a 404 on a URL that
 * is in somebody's bookmarks.
 *
 * Deletion was the alternative and it is worse. Five of these six paths were
 * in the sitemap and are indexed, so removing them turns real inbound links
 * into 404s and throws away whatever they had earned. A 301 keeps the link and
 * lands the reader on the page that answers what they came for.
 */
export function movedMetadata(to: string, title: string): Metadata {
  return {
    // `absolute`, for the same reason pageMetadata uses it: the root layout
    // appends the site name to any bare string, and these titles are built by
    // pageTitle and already carry it. Without this the six moved paths shipped
    // <title>Load · Antifailure · Antifailure</title>, which is what a
    // 301 hop shows in a browser tab for the moment it is on screen.
    title: { absolute: title },
    robots: { index: false, follow: true },
    alternates: { canonical: to },
  };
}

export function MovedPage({ to, label }: { to: string; label: string }) {
  return (
    <>
      <script
        // eslint-disable-next-line react/no-danger
        dangerouslySetInnerHTML={{ __html: `location.replace(${JSON.stringify(to)});` }}
      />
      <main id="main" tabIndex={-1} className="flex min-h-svh items-center justify-center px-6">
        <p className="text-[15px] tracking-extra-tight text-gray-new-40">
          This page moved to{" "}
          <Link prefetch={false} href={to} className="text-black underline decoration-black/20 underline-offset-4">
            {label}
          </Link>
          .
        </p>
      </main>
    </>
  );
}
