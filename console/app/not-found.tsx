import Link from "next/link";
import { LogoMark } from "@/components/icons";

export default function NotFound() {
  return (
    <main className="grid min-h-dvh place-items-center px-5 py-10">
      <div className="w-full max-w-[420px]">
        <LogoMark className="h-9 w-9" />
        <h1 className="mt-7 text-[28px] font-semibold leading-dense tracking-tighter text-ink">
          That page is not here
        </h1>
        <p className="mt-3 text-[13.5px] leading-6 text-muted">
          The address does not match anything in the console. It may have been a
          link from an older build.
        </p>
        <Link
          href="/environments"
          className="mt-6 inline-flex h-10 items-center rounded-[6px] bg-ink px-4 text-[13.5px] font-medium text-white hover:bg-[#2b2b2b]"
        >
          Go to environments
        </Link>
      </div>
    </main>
  );
}
