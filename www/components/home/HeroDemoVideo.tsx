"use client";

import { useRef, useState } from "react";

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

export function HeroDemoVideo() {
  const frameRef = useRef<HTMLDivElement>(null);
  const videoRef = useRef<HTMLVideoElement>(null);
  const [muted, setMuted] = useState(true);

  const toggleVolume = () => {
    const video = videoRef.current;
    if (!video) return;

    const nextMuted = !video.muted;
    video.muted = nextMuted;
    video.volume = nextMuted ? 0 : 1;
    setMuted(nextMuted);
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
    <div className="relative left-1/2 mt-20 w-screen max-w-[1500px] -translate-x-1/2 px-8 max-xl:mt-16 max-md:mt-12 max-md:px-5">
      <div
        ref={frameRef}
        className="group relative overflow-hidden rounded-[24px] bg-[#f3f2ec] shadow-[0_30px_100px_rgba(0,0,0,0.08)] transition-shadow duration-300 max-md:rounded-[18px] [&:fullscreen]:rounded-none [&:fullscreen]:bg-black [&:fullscreen]:p-0"
      >
        <div
          className="pointer-events-none absolute inset-0 opacity-70"
          style={{
            background:
              "radial-gradient(circle at 16% 10%, rgba(102,143,93,0.12), transparent 30%), radial-gradient(circle at 88% 76%, rgba(180,165,116,0.12), transparent 34%)",
          }}
          aria-hidden
        />
        <video
          ref={videoRef}
          className="relative block aspect-video w-full rounded-[24px] bg-white object-cover max-md:rounded-[18px] [&:fullscreen]:h-screen [&:fullscreen]:rounded-none [&:fullscreen]:object-contain"
          src="/home/option-4.mp4"
          autoPlay
          muted={muted}
          loop
          playsInline
          preload="metadata"
          aria-label="A product demo video showing Antifailure validating a deployment before release."
        />
        <div className="absolute right-4 bottom-4 z-10 flex items-center gap-2 max-sm:right-3 max-sm:bottom-3">
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
