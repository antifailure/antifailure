export function LogoMark({ className = "h-[20px] w-[20px]" }: { className?: string }) {
  return (
    <svg viewBox="0 0 18 18" className={className} fill="none" aria-hidden>
      <path
        d="M1.8 6.4V1.8H6.4M11.6 1.8H16.2V6.4M16.2 11.6V16.2H11.6M6.4 16.2H1.8V11.6"
        stroke="#33bf00"
        strokeWidth="2.1"
        strokeLinecap="square"
      />
    </svg>
  );
}

export function Wordmark({ compact = false }: { compact?: boolean }) {
  return (
    <a href="/" className="flex items-center gap-2.5 shrink-0">
      <LogoMark />
      <span
        className="font-semibold uppercase tracking-[0.08em] text-black"
        style={{ fontSize: compact ? 13 : 14.5, letterSpacing: "0.12em" }}
      >
        Antifailure
      </span>
    </a>
  );
}

export function Chevron({ className = "h-2.5 w-2.5" }: { className?: string }) {
  return (
    <svg viewBox="0 0 10 10" className={className} fill="none" aria-hidden>
      <path d="M2 3.5L5 6.5L8 3.5" stroke="currentColor" strokeWidth="1.2" />
    </svg>
  );
}

export function BookIcon({ className = "h-4 w-4" }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={className} fill="none" aria-hidden>
      <path
        d="M4 4.8A1.8 1.8 0 0 1 5.8 3H10a2.5 2.5 0 0 1 2 1 2.5 2.5 0 0 1 2-1h4.2A1.8 1.8 0 0 1 20 4.8v12.4a1.8 1.8 0 0 1-1.8 1.8H14a2.5 2.5 0 0 0-2 1 2.5 2.5 0 0 0-2-1H5.8A1.8 1.8 0 0 1 4 17.2Z"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinejoin="round"
      />
      <path d="M12 5v14" stroke="currentColor" strokeWidth="1.6" />
    </svg>
  );
}

export function GitHubIcon({ className = "h-4 w-4" }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={className} fill="currentColor" aria-hidden>
      <path d="M12 .3a12 12 0 0 0-3.8 23.4c.6.1.8-.26.8-.58v-2.02c-3.34.72-4.04-1.61-4.04-1.61c-.55-1.39-1.34-1.76-1.34-1.76c-1.09-.75.08-.73.08-.73c1.2.08 1.84 1.24 1.84 1.24c1.07 1.83 2.81 1.3 3.5 1c.1-.78.42-1.3.76-1.6c-2.67-.3-5.47-1.33-5.47-5.93c0-1.31.47-2.38 1.24-3.22c-.13-.3-.54-1.52.12-3.18c0 0 1.01-.32 3.3 1.23a11.5 11.5 0 0 1 6 0c2.29-1.55 3.3-1.23 3.3-1.23c.66 1.66.25 2.88.12 3.18c.77.84 1.24 1.91 1.24 3.22c0 4.61-2.81 5.63-5.48 5.92c.43.37.81 1.1.81 2.22v3.29c0 .32.22.69.82.58A12 12 0 0 0 12 .3z" />
    </svg>
  );
}

export function XIcon({ className = "h-3.5 w-3.5" }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={className} fill="currentColor" aria-hidden>
      <path d="M18.244 2H21.5l-7.5 8.57L22.5 22h-6.56l-5.14-6.72L5.2 22H1.93l8.02-9.16L1.5 2h6.72l4.64 6.15L18.244 2zm-1.15 18h1.8L7.02 3.9H5.1L17.094 20z" />
    </svg>
  );
}

export function LinkedInIcon({ className = "h-3.5 w-3.5" }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={className} fill="currentColor" aria-hidden>
      <path d="M4.98 3.5C4.98 4.88 3.88 6 2.5 6S0 4.88 0 3.5 1.12 1 2.5 1s2.48 1.12 2.48 2.5zM.24 8.48h4.52V24H.24V8.48zM8.34 8.48h4.33v2.12h.06c.6-1.14 2.08-2.34 4.28-2.34c4.58 0 5.42 3.02 5.42 6.94V24h-4.52v-7.7c0-1.84-.03-4.2-2.56-4.2c-2.56 0-2.95 2-2.95 4.06V24H8.34V8.48z" />
    </svg>
  );
}

export function YouTubeIcon({ className = "h-3.5 w-3.5" }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={className} fill="currentColor" aria-hidden>
      <path d="M23.5 6.2a3 3 0 0 0-2.12-2.14C19.5 3.7 12 3.7 12 3.7s-7.5 0-9.38.36A3 3 0 0 0 .5 6.2 31.6 31.6 0 0 0 0 12a31.6 31.6 0 0 0 .5 5.8 3 3 0 0 0 2.12 2.14C4.5 20.3 12 20.3 12 20.3s7.5 0 9.38-.36A3 3 0 0 0 23.5 17.8 31.6 31.6 0 0 0 24 12a31.6 31.6 0 0 0-.5-5.8zM9.75 15.02V8.98L15.5 12l-5.75 3.02z" />
    </svg>
  );
}

export function CopyIcon({ className = "h-3.5 w-3.5" }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" className={className} fill="none" aria-hidden>
      <rect x="5.5" y="5.5" width="8" height="8" rx="1.2" stroke="currentColor" strokeWidth="1.2" />
      <path d="M3.5 10.5h-.8A.7.7 0 0 1 2 9.8V3.7A.7.7 0 0 1 2.7 3h6.1a.7.7 0 0 1 .7.7v.8" stroke="currentColor" strokeWidth="1.2" />
    </svg>
  );
}

export function DatabricksMark() {
  return (
    <svg viewBox="0 0 14 14" className="h-3.5 w-3.5" aria-hidden>
      <path d="M7 1.2 12.4 4.2 7 7.2 1.6 4.2 7 1.2Z" fill="#FF3621" />
      <path d="M7 7.6 12.4 10.6 7 13.6 1.6 10.6 7 7.6Z" fill="#FF3621" opacity="0.85" />
    </svg>
  );
}

export function RedTriangle() {
  return (
    <svg viewBox="0 0 8 8" className="h-2 w-2" aria-hidden>
      <path d="M1.2 1.4h5.6L4 6.6 1.2 1.4Z" fill="#FF3621" />
    </svg>
  );
}
