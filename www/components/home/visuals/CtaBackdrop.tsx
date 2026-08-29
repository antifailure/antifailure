/**
 * The backdrop behind the closing call to action.
 *
 * The panel is green at the top and eases into the footer's black so the two
 * surfaces read as one piece. The wash is a CSS gradient with a long ease —
 * SVG stop interpolation bands on a falloff this long, and a two-stop fade
 * would show a seam against the footer.
 *
 * The drawing on top is the same teardown as before: a grid and resource
 * blocks that thin out as they fall, dissolving into the black rather than
 * sitting on a hard horizon.
 */
const WASH = [
  "linear-gradient(180deg",
  "#1c6414 0%",
  "#1a5c12 10%",
  "#174e10 22%",
  "#133e0c 36%",
  "#0e2c09 50%",
  "#091d06 62%",
  "#051204 74%",
  "#020901 84%",
  "#000400 92%",
  "#000000 100%)",
].join(",");

const BLOOM =
  "radial-gradient(100% 52% at 26% 0%, rgba(51,191,0,0.40) 0%, rgba(51,191,0,0.16) 34%, rgba(51,191,0,0.04) 58%, transparent 72%)";

const ART_FADE =
  "linear-gradient(180deg, #000 0%, #000 28%, rgba(0,0,0,0.78) 52%, rgba(0,0,0,0.35) 72%, transparent 90%)";

const VEIL = [
  "linear-gradient(180deg",
  "transparent 0%",
  "rgba(0,0,0,0.03) 12%",
  "rgba(0,0,0,0.10) 26%",
  "rgba(0,0,0,0.22) 40%",
  "rgba(0,0,0,0.40) 54%",
  "rgba(0,0,0,0.62) 68%",
  "rgba(0,0,0,0.82) 82%",
  "rgba(0,0,0,0.94) 92%",
  "#000000 100%)",
].join(",");

export function CtaBackdrop({ className = "" }: { className?: string }) {
  return (
    <div className={className} aria-hidden>
      <div className="absolute inset-0" style={{ background: WASH }} />
      <div className="absolute inset-0" style={{ background: BLOOM }} />

      <svg
        className="absolute inset-0 h-full w-full"
        viewBox="0 0 1920 944"
        preserveAspectRatio="xMidYMid slice"
        focusable="false"
        style={{
          maskImage: ART_FADE,
          WebkitMaskImage: ART_FADE,
        }}
      >
        <defs>
          <linearGradient id="af-cta-gridfade" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="#ffffff" stopOpacity="0.16" />
            <stop offset="48%" stopColor="#ffffff" stopOpacity="0.07" />
            <stop offset="100%" stopColor="#ffffff" stopOpacity="0" />
          </linearGradient>
          <mask id="af-cta-gridmask">
            <rect width="1920" height="944" fill="url(#af-cta-gridfade)" />
          </mask>

          <pattern id="af-cta-grid" width="72" height="72" patternUnits="userSpaceOnUse">
            <path d="M72 0H0v72" fill="none" stroke="#ffffff" strokeWidth="1" />
          </pattern>

          <linearGradient id="af-cta-blockfade" x1="0" y1="0" x2="1" y2="0.35">
            <stop offset="0%" stopColor="#ffffff" stopOpacity="0.9" />
            <stop offset="52%" stopColor="#ffffff" stopOpacity="0.35" />
            <stop offset="88%" stopColor="#ffffff" stopOpacity="0" />
          </linearGradient>
          <mask id="af-cta-blockmask">
            <rect width="1920" height="944" fill="url(#af-cta-blockfade)" />
          </mask>
        </defs>

        <rect width="1920" height="944" fill="url(#af-cta-grid)" mask="url(#af-cta-gridmask)" />

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
          <g opacity="0.32" strokeWidth="1.25">
            <path d="M118 790h164" />
            <path d="M316 803h132" />
            <path d="M482 782h118" opacity="0.7" />
          </g>
        </g>
      </svg>

      {/* Pixel-anchored, not percentage-anchored: a short panel would otherwise
          still be green at the bottom and show a seam against the footer. */}
      <div
        className="absolute inset-x-0 bottom-0 h-[min(200px,48%)]"
        style={{ background: VEIL }}
      />
    </div>
  );
}
