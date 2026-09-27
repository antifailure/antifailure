"use client";

import { useEffect, useRef } from "react";

export type SheetId = "privacy";

export function ContentSheet({
  open,
  onClose,
}: {
  open: boolean;
  id: SheetId;
  onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    const element = dialog.current;
    if (!element) return;
    if (open && !element.open) element.showModal();
    if (!open && element.open) element.close();
  }, [open]);

  return (
    <dialog
      ref={dialog}
      aria-labelledby="privacy-summary-title"
      onCancel={onClose}
      onClose={onClose}
      onClick={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
      className="fixed inset-0 m-auto max-h-[calc(100dvh-40px)] w-[calc(100%-40px)] max-w-[520px] overflow-y-auto rounded-[12px] border border-stroke bg-white p-0 text-gray-new-10 shadow-lg backdrop:bg-black/50"
    >
      <div className="p-7 max-sm:p-5" onClick={(event) => event.stopPropagation()}>
        <h2 id="privacy-summary-title" className="text-[28px] leading-tight tracking-tighter">Your privacy</h2>
        <p className="mt-4 text-base leading-7 text-gray-new-40">Signing in uses your GitHub profile to identify you and create a session.</p>
        <ul className="mt-5 space-y-4 text-base leading-7 text-gray-new-40">
          <li>Account records include your GitHub ID, name, login, avatar, and verified email. Session records include your IP address and browser.</li>
          <li>When enabled, website analytics include page counts and PostHog session replay. Typed values are masked before recording.</li>
          <li>Global Privacy Control, Do Not Track, and the measurement switch on the privacy page disable website measurement.</li>
          <li>Production snapshots and raw test data stay in your infrastructure by default.</li>
        </ul>
        <a href="/privacy" className="mt-6 inline-flex min-h-11 items-center text-base font-medium underline decoration-black/25 underline-offset-4 focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-black">Read the full privacy notice →</a>
        <button type="button" onClick={onClose} className="mt-4 flex min-h-11 w-full items-center justify-center rounded-full bg-gray-new-10 px-5 text-base text-white hover:bg-gray-new-20 focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-black">Close</button>
      </div>
    </dialog>
  );
}
