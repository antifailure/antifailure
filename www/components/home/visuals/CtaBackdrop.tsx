/**
 * The backdrop behind the closing call to action.
 *
 * What was here before rendered nothing. Every layer of the old component
 * carried `hidden` with no breakpoint prefix alongside `max-md:hidden`, so the
 * grid, the glow and the resource nodes were switched off at every width, and
 * the only survivor was an absolutely positioned block of journal text that
 * landed on top of the paragraph. The section read as a black void with two
 * lines of stray monospace floating in it.
 *
 * This replaces it with a drawn background rather than an animated one. It is
 * a single inline SVG: no image request, no WebGL context to lose, no rAF loop
 * running while somebody reads, and nothing that moves on its own. It scales to
 * any viewport because everything is expressed in the viewBox and sliced.
 *
 * The picture is the last thing the product does. An environment is torn down,
 * so the resource blocks thin out from left to right and the grid dissolves
 * with them, and the green horizon sits low and cools as it rises. It is the
 * same light as the hero aurora, further away.
 */
export function CtaBackdrop({ className = "" }: { className?: string }) {
  return (
    <svg
      className={className}
      viewBox="0 0 1920 944"
      preserveAspectRatio="xMidYMid slice"
      aria-hidden
      focusable="false"
    >
      <defs>
        {/* The ground the whole panel sits on. Slightly lifted at the bottom so
            the footer's white edge has something to meet. */}
        <linearGradient id="af-cta-base" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor="#0d0e0f" />
          <stop offset="62%" stopColor="#131415" />
          <stop offset="100%" stopColor="#191b1c" />
        </linearGradient>

        {/* The horizon. Low, wide, and weak enough that white text over it
            stays well above 4.5:1 everywhere it can land. */}
        <radialGradient id="af-cta-horizon" cx="0.30" cy="0.92" r="1.05">
          <stop offset="0%" stopColor="#33bf00" stopOpacity="0.34" />
          <stop offset="34%" stopColor="#2aa000" stopOpacity="0.16" />
          <stop offset="66%" stopColor="#1d6b12" stopOpacity="0.06" />
          <stop offset="100%" stopColor="#0d0e0f" stopOpacity="0" />
        </radialGradient>

        <radialGradient id="af-cta-counter" cx="0.88" cy="0.12" r="0.6">
          <stop offset="0%" stopColor="#7fe3c4" stopOpacity="0.07" />
          <stop offset="100%" stopColor="#0d0e0f" stopOpacity="0" />
        </radialGradient>

        {/* The grid fades out toward the top and the right: the further from
            the horizon, the less there is left standing. */}
        <linearGradient id="af-cta-gridfade" x1="0" y1="1" x2="1" y2="0">
          <stop offset="0%" stopColor="#ffffff" stopOpacity="0.15" />
          <stop offset="45%" stopColor="#ffffff" stopOpacity="0.06" />
          <stop offset="100%" stopColor="#ffffff" stopOpacity="0" />
        </linearGradient>
        <mask id="af-cta-gridmask">
          <rect width="1920" height="944" fill="url(#af-cta-gridfade)" />
        </mask>

        <pattern id="af-cta-grid" width="72" height="72" patternUnits="userSpaceOnUse">
          <path d="M72 0H0v72" fill="none" stroke="#ffffff" strokeWidth="1" />
        </pattern>

        {/* Blocks nearest the horizon are still whole; the ones further out have
            already gone. The mask does the thinning so the shapes stay simple. */}
        <linearGradient id="af-cta-blockfade" x1="0" y1="0" x2="1" y2="0">
          <stop offset="0%" stopColor="#ffffff" stopOpacity="0.9" />
          <stop offset="52%" stopColor="#ffffff" stopOpacity="0.35" />
          <stop offset="88%" stopColor="#ffffff" stopOpacity="0" />
        </linearGradient>
        <mask id="af-cta-blockmask">
          <rect width="1920" height="944" fill="url(#af-cta-blockfade)" />
        </mask>

        <filter id="af-cta-soften" x="-20%" y="-20%" width="140%" height="140%">
          <feGaussianBlur stdDeviation="26" />
        </filter>
      </defs>

      <rect width="1920" height="944" fill="url(#af-cta-base)" />
      <rect width="1920" height="944" fill="url(#af-cta-grid)" mask="url(#af-cta-gridmask)" />
      <rect width="1920" height="944" fill="url(#af-cta-counter)" />
      <rect width="1920" height="944" fill="url(#af-cta-horizon)" />

      {/* The teardown, read left to right. Whole, then partial, then gone. */}
      <g mask="url(#af-cta-blockmask)" fill="none" stroke="#9ff58a" strokeWidth="1.5">
        <g opacity="0.5">
          <rect x="118" y="742" width="164" height="96" rx="3" />
          <rect x="150" y="700" width="100" height="30" rx="3" opacity="0.6" />
          <rect x="316" y="768" width="132" height="70" rx="3" />
          <rect x="482" y="726" width="118" height="112" rx="3" opacity="0.75" />
          <rect x="636" y="784" width="150" height="54" rx="3" opacity="0.55" />
          <rect x="822" y="748" width="96" height="90" rx="3" opacity="0.45" />
          <rect x="952" y="796" width="128" height="42" rx="3" opacity="0.3" />
          <rect x="1116" y="764" width="88" height="74" rx="3" opacity="0.22" />
          <rect x="1240" y="808" width="104" height="30" rx="3" opacity="0.14" />
        </g>
        {/* A few struck through, the way the journal marks what it destroyed. */}
        <g opacity="0.32" strokeWidth="1.25">
          <path d="M118 790h164" />
          <path d="M316 803h132" />
          <path d="M482 782h118" opacity="0.7" />
        </g>
      </g>

      {/* The line everything stands on, brightest where the glow is. */}
      <g>
        <path d="M0 838h1920" stroke="#33bf00" strokeOpacity="0.20" strokeWidth="1.5" />
        <path d="M0 838h980" stroke="#7ce34f" strokeOpacity="0.30" strokeWidth="1.5" />
      </g>

      {/* One soft bloom sitting on the horizon so the edge is not a hard rule. */}
      <ellipse
        cx="470"
        cy="852"
        rx="440"
        ry="54"
        fill="#33bf00"
        fillOpacity="0.14"
        filter="url(#af-cta-soften)"
      />
    </svg>
  );
}
