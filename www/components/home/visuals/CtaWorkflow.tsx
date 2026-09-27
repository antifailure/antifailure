/** The agent sends a change to a twin and receives evidence through MCP. */
export function CtaWorkflow() {
  return (
    <div className="mx-auto w-full max-w-[560px]" aria-hidden>
      <svg viewBox="0 0 500 310" className="h-auto w-full" fill="none">
        <path d="M18 28H482M18 282H482" stroke="var(--color-forest-line)" className="max-md:hidden" />
        <path d="M18 12H482M18 282H482" stroke="var(--color-forest-line)" className="hidden max-md:block" />
        <text x="95" y="66" textAnchor="middle" className="fill-gray-new-80 font-mono text-[13px] max-md:text-[29px]">Your agent</text>
        <text x="376" y="66" textAnchor="middle" className="fill-gray-new-80 font-mono text-[13px] max-md:hidden">Production twin</text>
        <text x="376" y="48" textAnchor="middle" className="hidden fill-gray-new-80 font-mono text-[29px] max-md:block"><tspan x="376">Production</tspan><tspan x="376" dy="30">twin</tspan></text>

        <path d="M33 89H157V226H33Z" stroke="var(--color-gray-new-80)" strokeWidth="1.2" />
        <path d="M48 112H110M48 128H140M48 160H84M48 176H127M48 192H105" stroke="var(--color-gray-new-80)" strokeWidth="3" />
        <path d="M48 144H127" stroke="var(--color-green-52)" strokeWidth="3" />

        <path d="M183 158H293M285 150L293 158L285 166" stroke="var(--color-green-52)" strokeWidth="1.5" />
        <text x="238" y="138" textAnchor="middle" className="fill-gray-new-80 font-mono text-[12px] max-md:text-[29px]">MCP</text>

        <path d="M319 109V91H350M402 91H433V109M433 202V226H402M350 226H319V202" stroke="var(--color-green-52)" strokeWidth="3" />
        <path d="M338 125H414M338 158H414M338 191H414" stroke="var(--color-forest-line)" strokeWidth="1.2" />
        <rect x="342" y="114" width="66" height="18" fill="var(--color-forest-line)" />
        <rect x="342" y="148" width="66" height="18" fill="var(--color-forest-line)" />
        <rect x="342" y="182" width="66" height="18" fill="var(--color-forest-line)" />
        <path d="M342 132H408M342 166H408M342 200H408" stroke="var(--color-green-52)" strokeWidth="1.4" />

        <path d="M376 237V260H93V237M87 244L93 237L99 244" stroke="var(--color-gray-new-80)" strokeWidth="1.2" />
        <rect x="176" y="247" width="118" height="24" fill="var(--color-forest)" className="max-md:hidden" />
        <rect x="119" y="239" width="232" height="40" fill="var(--color-forest)" className="hidden max-md:block" />
        <text x="235" y="263" textAnchor="middle" className="fill-gray-new-90 font-mono text-[12px] max-md:text-[29px]">Findings back</text>
      </svg>
    </div>
  );
}
