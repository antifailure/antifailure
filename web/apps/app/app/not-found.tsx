import type { Metadata } from "next";
import { LinkButton } from "../components/ui";
import { LogoMark } from "../components/Chrome";

export const metadata: Metadata = { title: "Not here" };

export default function NotFound() {
  return (
    <div className="mesh-grid flex min-h-dvh flex-col items-center justify-center px-4 py-10">
      <div className="w-full max-w-[420px]">
        <div className="mb-6 flex items-center gap-2.5">
          <LogoMark className="h-[22px] w-[22px]" />
          <span
            className="font-semibold uppercase text-ink"
            style={{ fontSize: 13.5, letterSpacing: "0.12em" }}
          >
            Antifailure
          </span>
        </div>
        <div className="rounded-xl border border-hair bg-surface p-5 sm:p-6">
          <h1 className="text-[20px] font-semibold leading-[1.15] tracking-tighter text-ink">
            Nothing here by that name
          </h1>
          <p className="mt-2 text-[13px] leading-[1.6] text-muted">
            Either it never existed, or it belongs to another organization. This application
            answers the same way for both, deliberately: telling them apart would be a way to ask
            whether somebody else has an environment by that name.
          </p>
          <p className="mt-2 text-[13px] leading-[1.6] text-muted">
            An environment that has been torn down is gone rather than hidden, and a link to one
            from an old pull request comment lands here.
          </p>
          <div className="mt-5">
            <LinkButton href="/" variant="primary">
              Every environment
            </LinkButton>
          </div>
        </div>
      </div>
    </div>
  );
}
