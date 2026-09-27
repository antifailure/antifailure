function Application({ x, y, small = false }: { x: number; y: number; small?: boolean }) {
  return (
    <g transform={`translate(${x} ${y}) scale(${small ? 0.72 : 1})`}>
      <path d="M12 0H176V132H12Z" fill="var(--color-sage-2)" stroke="var(--color-forest-line)" strokeOpacity=".35" />
      <path d="M0 12H164V144H0Z" fill="var(--color-sage)" stroke="var(--color-forest)" strokeWidth="1.5" />
      <path d="M20 38H76M20 61H123M34 84H137M34 107H91" stroke="var(--color-forest-line)" strokeWidth="3" />
      <path d="M20 84H26M20 107H26M20 126H76" stroke="var(--color-positive-ink)" strokeWidth="3" />
    </g>
  );
}

function Payment({ x, y }: { x: number; y: number }) {
  return (
    <g transform={`translate(${x} ${y})`} fill="var(--color-sage)">
      <path d="M8 0H90V53H8Z" fill="var(--color-sage-2)" stroke="var(--color-forest-line)" strokeOpacity=".4" />
      <path d="M0 9H82V62H0Z" stroke="var(--color-forest)" strokeWidth="1.5" />
      <path d="M0 25H82" stroke="var(--color-forest)" strokeWidth="7" />
      <path d="M12 47H34M57 47H68" stroke="var(--color-forest-line)" strokeWidth="2" />
      <circle cx="82" cy="60" r="13" fill="var(--color-forest)" />
      <path d="M76 60L80 64L87 56" stroke="var(--color-sage)" strokeWidth="1.8" />
    </g>
  );
}

function Inbox({ x, y }: { x: number; y: number }) {
  return (
    <g transform={`translate(${x} ${y})`} fill="var(--color-sage)">
      <path d="M11 0H78V47H11Z" fill="var(--color-sage-2)" stroke="var(--color-forest-line)" strokeOpacity=".4" />
      <path d="M5 8H72V55H5Z" stroke="var(--color-forest)" strokeWidth="1.5" />
      <path d="M5 8L38.5 33L72 8" stroke="var(--color-forest)" strokeWidth="1.5" />
      <path d="M0 40H20L26 49H51L57 40H82V66H0Z" fill="var(--color-sage-2)" stroke="var(--color-forest)" strokeWidth="1.5" />
      <path d="M28 57H54" stroke="var(--color-forest-line)" strokeWidth="1.5" />
    </g>
  );
}

function DesktopDiagram() {
  return (
    <svg viewBox="0 0 1040 430" fill="none" className="hidden w-full @min-[720px]:block" aria-hidden="true">
      <path d="M36 65H1004V386H36Z" stroke="var(--color-forest-line)" strokeOpacity=".35" />
      <text x="36" y="38" className="fill-forest font-mono text-[18px]">Your production twin</text>
      <text x="90" y="126" className="fill-forest text-[23px]">Your app</text>
      <Application x={90} y={162} />
      <g stroke="var(--color-forest-line)" strokeWidth="1.5">
        <path d="M266 234H293V147H440M293 234H440M293 234V321H440" />
        <path d="M432 141L440 147L432 153M432 228L440 234L432 240M432 315L440 321L432 327" />
      </g>
      <text x="312" y="126" className="fill-forest text-[18px]">Payment</text>
      <text x="312" y="213" className="fill-forest text-[18px]">Email</text>
      <text x="312" y="300" className="fill-forest text-[18px]">Unknown host</text>
      <path d="M455 112V348" stroke="var(--color-forest)" strokeWidth="12" />
      <text x="455" y="97" textAnchor="middle" className="fill-forest font-mono text-[18px]">Antifailure</text>
      <g stroke="var(--color-positive-ink)" strokeWidth="1.8">
        <path d="M467 147H566M558 141L566 147L558 153M467 234H566M558 228L566 234L558 240" />
      </g>
      <Payment x={591} y={110} />
      <Inbox x={591} y={202} />
      <text x="722" y="141" className="fill-forest text-[25px]">Payment simulated</text>
      <text x="722" y="168" className="fill-positive-ink text-[18px]">No real charge</text>
      <text x="722" y="231" className="fill-forest text-[25px]">Email captured</text>
      <text x="722" y="258" className="fill-positive-ink text-[18px]">Ready in your test inbox</text>
      <circle cx="455" cy="321" r="12" fill="var(--color-sage)" stroke="var(--color-danger-ink)" strokeWidth="1.5" />
      <path d="M450 316L460 326M460 316L450 326" stroke="var(--color-danger-ink)" strokeWidth="1.8" />
      <text x="495" y="328" className="fill-danger-ink text-[20px]">Blocked</text>
    </svg>
  );
}

function MobileDiagram() {
  return (
    <svg viewBox="0 0 360 560" fill="none" className="mx-auto block w-full max-w-[440px] @min-[720px]:hidden" aria-hidden="true">
      <text x="24" y="37" className="fill-forest font-mono text-[21px]">Your production twin</text>
      <path d="M24 61H336V536H24Z" stroke="var(--color-forest-line)" strokeOpacity=".35" />
      <text x="180" y="93" textAnchor="middle" className="fill-forest text-[21px]">Your app</text>
      <Application x={117} y={109} small />
      <path d="M180 213V244M174 236L180 244L186 236" stroke="var(--color-forest-line)" strokeWidth="1.5" />
      <path d="M65 253H295" stroke="var(--color-forest)" strokeWidth="9" />
      <text x="180" y="283" textAnchor="middle" className="fill-forest font-mono text-[21px]">Antifailure</text>
      <g stroke="var(--color-positive-ink)" strokeWidth="1.7">
        <path d="M97 262V300M91 292L97 300L103 292M260 262V300M254 292L260 300L266 292" />
      </g>
      <Payment x={53} y={314} />
      <Inbox x={219} y={310} />
      <text x="98" y="412" textAnchor="middle" className="fill-forest text-[21px]">Payment</text>
      <text x="98" y="439" textAnchor="middle" className="fill-forest text-[21px]">simulated</text>
      <text x="260" y="412" textAnchor="middle" className="fill-forest text-[21px]">Email</text>
      <text x="260" y="439" textAnchor="middle" className="fill-forest text-[21px]">captured</text>
      <path d="M66 469H294" stroke="var(--color-forest-line)" strokeOpacity=".25" />
      <circle cx="70" cy="498" r="10" stroke="var(--color-danger-ink)" strokeWidth="1.5" />
      <path d="M66 494L74 502M74 494L66 502" stroke="var(--color-danger-ink)" strokeWidth="1.5" />
      <text x="91" y="505" className="fill-danger-ink text-[21px]">Unknown hosts blocked</text>
    </svg>
  );
}

/** A routing diagram, not a screenshot of the product. */
export function FailClosedScene() {
  return (
    <figure>
      <div
        className="@container overflow-hidden bg-sage"
        role="img"
        aria-label="Antifailure keeps test payments and emails inside the production twin. Payment calls receive a simulated response, emails arrive in a test inbox, and unknown destinations are blocked. Real customers receive no charges or messages in this example."
      >
        <DesktopDiagram />
        <MobileDiagram />
      </div>
      <figcaption className="mt-4 text-sm leading-6 text-gray-new-40 max-md:text-base">
        Example policy: payments simulated, emails captured, unknown hosts blocked.
      </figcaption>
    </figure>
  );
}
