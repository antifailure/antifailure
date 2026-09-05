"use client";

import { type RefObject, useEffect } from "react";

export function useViewportVideoPlayback(videoRef: RefObject<HTMLVideoElement | null>) {
  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;

    let pausedByViewport = false;

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
