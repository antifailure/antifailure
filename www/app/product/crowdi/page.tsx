import type { Metadata } from "next";
import Link from "next/link";

export const metadata: Metadata = {
  title: "Exploratory users — Antifailure",
  robots: { index: false, follow: true },
  alternates: { canonical: "/product/exploratory-users" },
};

export default function LegacySlugRedirectPage() {
  return (
    <>
      <script
        dangerouslySetInnerHTML={{
          __html: `location.replace("/product/exploratory-users");`,
        }}
      />
      <main id="main" tabIndex={-1} className="flex min-h-svh items-center justify-center px-6">
        <p className="text-[15px] tracking-extra-tight text-gray-new-40">
          This page moved to{" "}
          <Link
            href="/product/exploratory-users"
            className="text-black underline decoration-black/20 underline-offset-4"
          >
            exploratory users
          </Link>
          .
        </p>
      </main>
    </>
  );
}
