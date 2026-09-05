"use client";

import { useEffect, useRef, useState } from "react";

import { cn } from "@/lib/cn";
import { VideoRestartButton } from "./VideoRestartButton";
import { useViewportVideoPlayback } from "./useViewportVideoPlayback";

function VolumeIcon({ on }: { on: boolean }) {
  return (
    <svg viewBox="0 0 20 20" className="size-4" fill="none" aria-hidden>
      <path
        d="M3.2 7.4v5.2h3.1l4.3 3.2V4.2L6.3 7.4H3.2Z"
        stroke="currentColor"
        strokeWidth="1.45"
        strokeLinejoin="round"
      />
      {on ? (
        <>
          <path d="M13.1 7.1a4.2 4.2 0 0 1 0 5.8" stroke="currentColor" strokeWidth="1.45" strokeLinecap="round" />
          <path d="M15.4 5.2a7 7 0 0 1 0 9.6" stroke="currentColor" strokeWidth="1.45" strokeLinecap="round" opacity="0.7" />
        </>
      ) : (
        <path d="M13.1 7.2 17 11.1M17 7.2l-3.9 3.9" stroke="currentColor" strokeWidth="1.45" strokeLinecap="round" />
      )}
    </svg>
  );
}

function FullscreenIcon() {
  return (
    <svg viewBox="0 0 20 20" className="size-4" fill="none" aria-hidden>
      <path
        d="M7.8 4.2H4.2v3.6M12.2 4.2h3.6v3.6M15.8 12.2v3.6h-3.6M4.2 12.2v3.6h3.6"
        stroke="currentColor"
        strokeWidth="1.45"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

const DEMO_STEPS = [
  "Risky migration detected",
  "Replay runs inside containment",
  "Report blocks unsafe release",
];

export function HeroDemoVideo() {
  const frameRef = useRef<HTMLDivElement>(null);
  const videoRef = useRef<HTMLVideoElement>(null);
  const [muted, setMuted] = useState(true);
  const [fullscreen, setFullscreen] = useState(false);

  useViewportVideoPlayback(videoRef);

  useEffect(() => {
    const syncFullscreenState = () => {
      setFullscreen(document.fullscreenElement === frameRef.current);
    };

    document.addEventListener("fullscreenchange", syncFullscreenState);
    return () => document.removeEventListener("fullscreenchange", syncFullscreenState);
  }, []);

  const toggleVolume = () => {
    const video = videoRef.current;
    if (!video) return;

    const nextMuted = !video.muted;
    video.muted = nextMuted;
    video.volume = nextMuted ? 0 : 1;
    setMuted(nextMuted);
    void video.play();
  };

  const restartVideo = () => {
    const video = videoRef.current;
    if (!video) return;

    video.currentTime = 0;
    void video.play();
  };

  const openFullscreen = () => {
    const frame = frameRef.current;
    if (!frame) return;

    if (document.fullscreenElement) {
      void document.exitFullscreen();
      return;
    }

    void frame.requestFullscreen();
  };

  return (
    <div className="relative mt-20 grid w-full grid-cols-[minmax(220px,330px)_minmax(0,1fr)] gap-x-16 max-xl:mt-16 max-xl:grid-cols-1 max-xl:gap-y-8 max-md:mt-12">
      <div className="flex h-full max-w-[390px] flex-col justify-between max-xl:max-w-[720px] max-xl:gap-y-8">
        <div>
          <p className="text-xs font-medium uppercase tracking-[0.3em] text-gray-new-50">
            Product walkthrough
          </p>
          <h2 className="mt-5 text-[38px] leading-[1.03] tracking-[-0.06em] text-black max-lg:text-[34px] max-md:text-[30px]">
            Watch a risky pull request get stopped before release.
          </h2>
          <p className="mt-7 text-[17px] leading-7 tracking-extra-tight text-gray-new-40 max-md:mt-5 max-md:text-base max-md:leading-6">
            Eighty eight seconds. Antifailure copies production with every real
            name replaced, runs the change against the copy on every pull
            request, and fails the check when a migration turns a query into a
            sequential scan over forty eight million rows.
          </p>
        </div>
        <ul className="space-y-4">
          {DEMO_STEPS.map((step) => (
            <li key={step} className="flex items-center gap-4 text-base tracking-extra-tight text-black">
              <span className="size-2 rounded-full bg-[#668f5d]" aria-hidden />
              <span>{step}</span>
            </li>
          ))}
        </ul>
      </div>
      <div
        ref={frameRef}
        className={cn(
          "group relative overflow-hidden bg-[#f3f2ec] shadow-[0_30px_100px_rgba(0,0,0,0.08)] transition-shadow duration-300",
          fullscreen && "flex h-screen items-center justify-center bg-black shadow-none",
        )}
      >
        <div
          className="pointer-events-none absolute inset-0 opacity-70"
          style={{
            background:
              "radial-gradient(circle at 16% 10%, rgba(102,143,93,0.12), transparent 30%), radial-gradient(circle at 88% 76%, rgba(180,165,116,0.12), transparent 34%)",
          }}
          aria-hidden
        />
        {/* NO autoPlay AND NO loop, and both are deliberate.
            A loop is the thing tools/motioncheck exists to refuse, and the
            rule it states has no carve out for a real event, so it has none
            for a film either: this plays once when the section is scrolled to,
            settles on its last frame, and stops, which is what the twin figure
            in the same page already does. autoPlay would download the whole
            file for every visitor including the ones who never scroll this
            far, so playback is started by the viewport observer instead and
            preload is none until then. The poster is what the section shows
            until that happens, so the slot is never an empty rectangle. */}
        <video
          ref={videoRef}
          className={cn("relative block w-full bg-white object-contain", fullscreen ? "h-screen" : "aspect-video")}
          src="/home/product-walkthrough.mp4"
          poster="/home/product-walkthrough.jpg"
          muted={muted}
          playsInline
          preload="none"
          aria-label="A product demo video showing Antifailure validating a deployment before release."
        />
        <div className="absolute right-4 bottom-4 z-10 flex items-center gap-2 max-sm:right-3 max-sm:bottom-3">
          <VideoRestartButton onClick={restartVideo} />
          <button
            type="button"
            className="grid size-10 place-items-center rounded-full bg-black/72 text-white shadow-[0_10px_28px_rgba(0,0,0,0.18)] backdrop-blur-md transition-colors duration-200 hover:bg-black focus:outline-none focus:ring-2 focus:ring-white/80"
            aria-label={muted ? "Turn video sound on" : "Mute video"}
            onClick={toggleVolume}
          >
            <VolumeIcon on={!muted} />
          </button>
          <button
            type="button"
            className="grid size-10 place-items-center rounded-full bg-black/72 text-white shadow-[0_10px_28px_rgba(0,0,0,0.18)] backdrop-blur-md transition-colors duration-200 hover:bg-black focus:outline-none focus:ring-2 focus:ring-white/80"
            aria-label="Open video fullscreen"
            onClick={openFullscreen}
          >
            <FullscreenIcon />
          </button>
        </div>
      </div>
    </div>
  );
}
