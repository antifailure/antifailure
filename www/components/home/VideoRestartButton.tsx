function RestartIcon() {
  return (
    <svg viewBox="0 0 20 20" className="size-4" fill="none" aria-hidden>
      <path
        d="M6.2 6.1a5.8 5.8 0 1 1-.7 6.7"
        stroke="currentColor"
        strokeWidth="1.45"
        strokeLinecap="round"
      />
      <path
        d="M6.2 3.9v2.9H3.3"
        stroke="currentColor"
        strokeWidth="1.45"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

export function VideoRestartButton({ onClick }: { onClick: () => void }) {
  return (
    <button
      type="button"
      className="grid size-10 place-items-center rounded-full bg-black/72 text-white shadow-[0_10px_28px_rgba(0,0,0,0.18)] backdrop-blur-md transition-colors duration-200 hover:bg-black focus:outline-none focus:ring-2 focus:ring-white/80"
      aria-label="Restart video"
      onClick={onClick}
    >
      <RestartIcon />
    </button>
  );
}
