import Link from "next/link";
import { Lede, Standalone } from "@/components/ui";

export default function NotFound() {
  return (
    <Standalone title="That page is not here">
      <Lede>
        The address does not match anything in the console. It may have been a
        link from an older build.
      </Lede>
      <div className="mt-6">
        <Link
          href="/environments"
          className="inline-flex h-11 items-center justify-center rounded-md bg-ink px-4 text-[14px] font-medium text-white transition-colors hover:bg-[#2b2b2b]"
        >
          Go to environments
        </Link>
      </div>
    </Standalone>
  );
}
