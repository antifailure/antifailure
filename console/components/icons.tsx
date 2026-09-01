/**
 * The brand mark, byte for byte the one the marketing site and the favicon
 * use. Copied rather than imported because the two applications are separate
 * builds with separate lockfiles, and a shared package for one SVG would be a
 * workspace to maintain forever. If it changes, it changes in both.
 */
export function LogoMark({ className = "h-5 w-5" }: { className?: string }) {
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

export function GitHubMark({ className = "h-[18px] w-[18px]" }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 16 16" fill="currentColor" aria-hidden>
      <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27s1.36.09 2 .27c1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8Z" />
    </svg>
  );
}

/** One stroke width, one grid, one size, for every icon in the navigation. */
function stroke(d: string) {
  return function Icon({ className = "h-4 w-4" }: { className?: string }) {
    return (
      <svg viewBox="0 0 16 16" className={className} fill="none" aria-hidden>
        <path d={d} stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    );
  };
}

export const IconEnvironments = stroke("M3.5 5.2 8 3l4.5 2.2v5.6L8 13l-4.5-2.2zM3.5 5.2 8 7.4l4.5-2.2M8 7.4V13");
export const IconRuns = stroke("M2.5 8h3l1.6-3.4 2.4 6.8L11.2 8h2.3");
export const IconWorkloads = stroke("M2.6 4.6h4.4M2.6 11.4h3.4M2.6 8h6.8M9.4 6.2 11.2 8 9.4 9.8M13.6 4.4v7.2");
export const IconMasking = stroke("M8 2.6 13 4.5v4c0 2.6-2.1 4.2-5 4.9-2.9-.7-5-2.3-5-4.9v-4z");
export const IconNetwork = stroke("M8 2.4v11.2M2.4 8h11.2M8 2.4a7.4 7.4 0 0 1 0 11.2M8 2.4a7.4 7.4 0 0 0 0 11.2");
export const IconAudit = stroke("M4 2.6h8v10.8H4zM6.2 5.6h3.6M6.2 8h3.6M6.2 10.4h2.2");
export const IconMembers = stroke("M6 7.6a2 2 0 1 0 0-4 2 2 0 0 0 0 4ZM2.6 13c0-2 1.5-3.2 3.4-3.2S9.4 11 9.4 13M10.6 4a2 2 0 0 1 0 3.6M11.4 9.9c1.3.3 2 1.4 2 3.1");
export const IconKeys = stroke("M10.4 2.6a3.4 3.4 0 1 1-3.2 4.5L2.6 11.7v1.7h1.7v-1.3h1.3v-1.3h1.3l1.3-1.3");
export const IconPlan = stroke("M2.6 12.6V7.4M6.2 12.6V3.4M9.8 12.6V9M13.4 12.6V5.6");
export const IconSignOut = stroke("M6.4 3.2H3.2v9.6h3.2M9.6 10.4 12.8 8 9.6 5.6M12.8 8H6.4");
