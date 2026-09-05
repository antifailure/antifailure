"use client";

import { type RefObject, useEffect } from "react";

export function useViewportVideoPlayback(videoRef: RefObject<HTMLVideoElement | null>) {
  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;

    // TRUE, NOT FALSE, AND THIS IS THE WHOLE BUG THE `autoPlay` ATTRIBUTE WAS
    // HIDING. The flag means "this is stopped and the viewport is allowed to
    // start it". Starting at false made the first intersection a no-op, so the
    // observer could only ever RESUME a video that had already played and then
    // scrolled away. Nothing here could begin playback, and the element's own
    // `autoPlay` was what made the section look like it worked, at the cost of
    // downloading the file for every visitor including the ones who never
    // scroll to it.
    let pausedByViewport = true;

    // Reduced motion is honoured by never starting it. The poster stays, the
    // restart button still works, so the film is reachable by anybody who
    // wants it and is not played at anybody who has asked for less movement.
    const reducedMotion =
      typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (reducedMotion) return;

    const playFromCurrentTime = () => {
      pausedByViewport = false;
      void video.play().catch(() => {
        pausedByViewport = true;
      });
    };

    const pauseInPlace = () => {
      pausedByViewport = true;
      video.pause();
    };

    const isInViewport = () => {
      const rect = video.getBoundingClientRect();
      return rect.bottom > 0 && rect.right > 0 && rect.top < window.innerHeight && rect.left < window.innerWidth;
    };

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (!entry) return;

        if (entry.isIntersecting && entry.intersectionRatio >= 0.18) {
          if (!document.hidden && pausedByViewport) {
            playFromCurrentTime();
          }
          return;
        }

        pauseInPlace();
      },
      { threshold: [0, 0.18, 0.5, 1] },
    );

    const handleVisibilityChange = () => {
      if (document.hidden) {
        pauseInPlace();
        return;
      }

      if (pausedByViewport && isInViewport()) {
        playFromCurrentTime();
      }
    };

    observer.observe(video);
    document.addEventListener("visibilitychange", handleVisibilityChange);

    return () => {
      observer.disconnect();
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [videoRef]);
}
