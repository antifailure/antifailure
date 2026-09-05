"use client";

import { type ReactNode, useEffect, useRef, useState } from "react";

import { cn } from "@/lib/cn";
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

function PlayIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-4" fill="none" aria-hidden>
      <path d="M9 7.4 17 12l-8 4.6V7.4Z" fill="currentColor" />
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

// The film's own three beats, in its order, so the list beside it describes
// what somebody is about to watch rather than a product summary that happens
// to sit next to a video. Every line is in SHOT-LIST.md for the cut this
// section plays: "Antifailure makes a copy of production", "The same size. The
// same shape. The same load.", "Your change runs there first", "Then the copy
// deletes itself. Nothing left behind."
function PauseIcon() {
  return (
    <svg viewBox="0 0 24 24" className="size-4" fill="none" aria-hidden>
      <path d="M9 6.5v11M15 6.5v11" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" />
    </svg>
  );
}

function RestartIcon() {
  return (
    <svg viewBox="0 0 20 20" className="size-4" fill="none" aria-hidden>
      <path d="M6.2 6.1a5.8 5.8 0 1 1-.7 6.7" stroke="currentColor" strokeWidth="1.45" strokeLinecap="round" />
      <path d="M6.2 3.9v2.9H3.3" stroke="currentColor" strokeWidth="1.45" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

// SectionLabel's arrow, in the neon token, which is this site's list mark.
function BeatMark() {
  return (
    <svg viewBox="0 0 12 12" className="mt-[7px] size-3 flex-none text-neon" fill="none" aria-hidden>
      <path
        d="M1.8 6h8M6.6 3.2 9.8 6 6.6 8.8"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

/**
 * One control under the film.
 *
 * The `outlined` theme and the 44 pixel height are Button's, and the mono type
 * at 13 is CopyCodeButton's, because those two are what every other control on
 * this page is made of. Square rather than a pill: the install button on this
 * same page is `rounded-none`, and four pills in a row under a rectangular
 * picture is the shape nobody chose.
 *
 * The label is a word and not only an icon. A glyph-only control needs a
 * tooltip nobody on a phone can open, and the pair of them here, restart and
 * play, are the same shape to anybody who has not seen the film start.
 */
function FilmControl({
  onClick,
  label,
  children,
}: {
  onClick: () => void;
  label: string;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      className="inline-flex min-h-11 cursor-pointer items-center justify-center gap-x-2 border border-black/40 bg-black/[0.02] px-4 font-mono text-[12px] font-medium tracking-extra-tight whitespace-nowrap text-black transition-colors duration-200 hover:border-black focus-visible:outline focus-visible:outline-2 focus-visible:outline-neon max-sm:min-h-10 max-sm:gap-x-1.5 max-sm:px-3 max-sm:text-[11px]"
      onClick={onClick}
    >
      {children}
      {label}
    </button>
  );
}

const DEMO_STEPS = [
  "A copy of production, same size and shape",
  "Your change runs there first",
  "Then the copy deletes itself",
];

export function HeroDemoVideo() {
  const frameRef = useRef<HTMLDivElement>(null);
  const videoRef = useRef<HTMLVideoElement>(null);
  const [muted, setMuted] = useState(true);
  const [fullscreen, setFullscreen] = useState(false);
  const [playing, setPlaying] = useState(false);

  useViewportVideoPlayback(videoRef);

  // WITHOUT THIS THE FILM HAS NO PLAY CONTROL AT ALL. There is no `controls`
  // attribute, because the browser's own chrome fights the three buttons in
  // the corner, and there is no `autoPlay`, because a loop the reader did not
  // ask for is what tools/motioncheck refuses. So every way of starting it was
  // implicit: scrolling to it, or guessing that the circular arrow labelled
  // restart would start something that had never played. Anybody who arrived
  // with reduced motion on, or who scrolled past and came back, or whose tab
  // was in the background when the section went by, was looking at a still
  // image with no way to play it that says so.
  //
  // Tracked from the element's own events rather than from the click handlers,
  // because the viewport observer starts and stops it too and a flag set only
  // where a person clicks would be wrong every time it did.
  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;

    const sync = () => setPlaying(!video.paused && !video.ended);
    sync();
    for (const event of ["play", "playing", "pause", "ended"]) {
      video.addEventListener(event, sync);
    }
    return () => {
      for (const event of ["play", "playing", "pause", "ended"]) {
        video.removeEventListener(event, sync);
      }
    };
  }, []);

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

  const togglePlay = () => {
    const video = videoRef.current;
    if (!video) return;

    if (video.paused || video.ended) {
      void video.play();
      return;
    }
    video.pause();
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
      {/* Packed to the top rather than spread. `justify-between` stretched this
          column to the film's height and pushed the beats to its foot, so they
          sat level with the bottom of the picture with a hand's width of
          nothing above them. They belong under the sentence they continue. */}
      <div className="flex max-w-[390px] flex-col max-xl:max-w-[720px]">
        <div>
          {/* NO EYEBROW. It said "THE FILM" in tracked uppercase sans over a
              heading that already says what this is, and this site does not
              set eyebrows that way anywhere else: SectionLabel and Heading
              both use font-mono at 11 to 12 pixels beside a mark. A label in
              a fourth style, saying a word the sentence under it repeats, is
              furniture. The heading carries the section.

              The type below is the design system's rather than this file's
              own. It was text-[38px]/1.03 at tracking -0.06em in flat black,
              three values that exist nowhere else in the tree; leading-dense
              and tracking-tighter are the tokens, and a black lead sentence
              over gray-new-40 continuation is how every other section head on
              this page reads. */}
          <h2 className="text-[34px] font-normal leading-dense tracking-tighter text-gray-new-40 max-lg:text-[28px] max-md:text-[26px]">
            <strong className="font-normal text-black-pure">A risky pull request, stopped before it merges.</strong>{" "}
            Eighty five seconds.
          </h2>
          <p className="mt-7 text-base tracking-extra-tight text-gray-new-40 max-md:mt-5">
            Antifailure makes a copy of production, the same size and the same
            shape and the same load, with every real name replaced. Your change
            runs there first, on every pull request, before it merges, whether a
            person wrote it or an agent did. Then the copy deletes itself.
          </p>
        </div>
        {/* The site's own list mark, which is SectionLabel's arrow in the neon
            token, rather than a two pixel disc in #668f5d. That colour is in
            no palette in this repository and was written here once. */}
        <ul className="mt-8 space-y-4 max-md:mt-7">
          {DEMO_STEPS.map((step) => (
            <li key={step} className="flex items-start gap-x-3 text-base tracking-extra-tight text-black">
              <BeatMark />
              <span>{step}</span>
            </li>
          ))}
        </ul>
        {/* IN THIS COLUMN, UNDER THE BEATS, and not under the picture.
            They were three translucent discs on the frame, then a row beneath
            it, which left a band of empty page under the film exactly as tall
            as the controls and put them a long way from the words they belong
            with. The film burns its captions along the bottom of the frame, so
            they cannot sit on it; this column is where a control that carries
            a word belongs, and the picture keeps its own edges. */}
        {/* TWO BY TWO, because this column is at most 330 pixels and four
            controls at 44 tall with mono labels want about 450. As a wrapping
            row they broke three and one, which is a ragged edge rather than a
            group. Between `xl` and `sm` the column is the full width of the
            page and a row fits, so it is a row there. */}
        <div className="mt-8 grid grid-cols-2 gap-2 max-md:mt-7 max-xl:flex max-xl:flex-wrap max-xl:items-center max-sm:grid max-sm:grid-cols-2">
          <FilmControl onClick={togglePlay} label={playing ? "Pause" : "Play"}>
            {playing ? <PauseIcon /> : <PlayIcon />}
          </FilmControl>
          <FilmControl onClick={restartVideo} label="Restart">
            <RestartIcon />
          </FilmControl>
          <FilmControl onClick={toggleVolume} label={muted ? "Sound on" : "Sound off"}>
            <VolumeIcon on={!muted} />
          </FilmControl>
          <FilmControl onClick={openFullscreen} label="Fullscreen">
            <FullscreenIcon />
          </FilmControl>
        </div>
      </div>
      <div className="min-w-0">
        <div
          ref={frameRef}
          className={cn(
            "relative overflow-hidden border border-black/10 bg-[#f3f2ec]",
            fullscreen && "flex h-screen items-center justify-center border-0 bg-black",
          )}
        >
          {/* NO autoPlay AND NO loop, and both are deliberate.
              A loop is the thing tools/motioncheck exists to refuse, and the
              rule it states has no carve out for a real event, so it has none
              for a film either: this plays once when the section is scrolled
              to, settles on its last frame, and stops, which is what the twin
              figure in the same page already does. autoPlay would download the
              whole file for every visitor including the ones who never scroll
              this far, so playback is started by the viewport observer instead
              and preload is none until then. The poster is what the section
              shows until that happens, so the slot is never empty. */}
          <video
            ref={videoRef}
            className={cn("relative block w-full bg-white object-contain", fullscreen ? "h-screen" : "aspect-video")}
            src="/home/launch-film.mp4"
            poster="/home/launch-film.jpg"
            muted={muted}
            playsInline
            preload="none"
            aria-label="The Antifailure launch film. A copy of production is built, the change runs against it on a pull request, a migration is caught holding an exclusive lock on 48 million rows, and the copy is destroyed."
          />

          {/* The one control that has to sit on the picture, because it is the
              picture that has to look pressable. It is the install button's
              treatment, which is this site's flat white block in mono, and not
              a frosted disc: there is no backdrop-blur anywhere else in this
              tree and a translucent black circle over a cream still is the
              first thing that reads as decoration somebody reached for. */}
          {!playing && (
            <button
              type="button"
              className="group absolute inset-0 z-10 grid place-items-center focus:outline-none"
              aria-label="Play the film"
              onClick={togglePlay}
            >
              <span className="inline-flex min-h-11 items-center gap-x-3 border border-black/40 bg-white px-5 font-mono text-[13px] font-medium tracking-extra-tight whitespace-nowrap text-black transition-colors duration-200 group-hover:border-black group-hover:bg-[#F6FDFA] group-focus-visible:outline group-focus-visible:outline-2 group-focus-visible:outline-neon max-sm:min-h-10 max-sm:px-4 max-sm:text-[11px]">
                <PlayIcon />
                PLAY THE FILM
              </span>
            </button>
          )}

          {/* Once it is running the picture pauses on a click, which is what a
              video does everywhere else. */}
          {playing && (
            <button
              type="button"
              className="absolute inset-0 z-0 cursor-default focus:outline-none"
              aria-label="Pause the film"
              onClick={togglePlay}
            />
          )}
        </div>

      </div>
    </div>
  );
}
