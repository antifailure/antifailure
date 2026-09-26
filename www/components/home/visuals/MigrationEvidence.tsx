// Recorded orders-app rehearsal. MCP and the CLI share RunInsights.
const LOCKS = [["orders", "10.5s"], ["orders_pkey", "5.0s"], ["orders_customer_id_idx", "2.2s"]];

/** A schematic of the rehearsal, rather than an invented application screen. */
function RehearsalTable({ compact = false }: { compact?: boolean }) {
  return (
    <g>
      <path d="M12 12H272V222H12Z" fill="var(--color-sage-2)" stroke="var(--color-forest-line)" strokeOpacity=".25" />
      <path d="M6 6H266V216H6Z" fill="var(--color-sage)" stroke="var(--color-forest-line)" strokeOpacity=".4" />
      <path d="M0 0H260V210H0Z" fill="var(--color-forest)" />
      <text x="20" y="32" fill="var(--color-sage)" fontSize={compact ? 25 : 17} className="font-mono">orders</text>
      <path d="M0 48H260" stroke="var(--color-forest-line)" />
      {!compact && <><text x="18" y="70" fill="var(--color-sage-2)" fontSize="12" className="font-mono">id</text><text x="104" y="70" fill="var(--color-sage-2)" fontSize="12" className="font-mono">total_cents</text></>}
      {[88, 116, 144, 172].map((y, index) => (
        <g key={y}>
          <rect x="12" y={y - 8} width="232" height="25" fill="var(--color-forest-line)" fillOpacity=".45" />
          <path d={`M18 ${y + 18}H240`} stroke="var(--color-forest-line)" />
          <path d={`M18 ${y + 5}h${[33, 26, 39, 31][index]}`} stroke="var(--color-sage-2)" strokeWidth="3" />
          <rect x="102" y={y - 4} width="136" height="17" fill="var(--color-sage-2)" />
          <rect x="178" y={y - 4} width="60" height="17" fill="var(--color-green-52)" />
        </g>
      ))}
      <path d="M99 79H243V195H99" fill="none" stroke="var(--color-green-52)" />
    </g>
  );
}

function Lock({ x, y }: { x: number; y: number }) {
  return (
    <g transform={`translate(${x} ${y})`} fill="none" stroke="var(--color-ochre-ink)" strokeWidth="1.7" strokeLinejoin="round">
      <path d="M7 16V10a9 9 0 0 1 18 0v6" />
      <rect x="1" y="16" width="30" height="25" rx="2" fill="var(--color-sage)" />
      <path d="M16 26v7" />
    </g>
  );
}

function DesktopRehearsal() {
  return (
    <svg viewBox="0 0 900 370" fill="none" aria-hidden className="hidden w-full lg:block">
      <g stroke="var(--color-forest-line)" strokeOpacity=".14">
        <path d="M40 84H860M40 326H860" />
        <path d="M360 36V340" strokeDasharray="2 6" />
      </g>
      <text x="40" y="57" fill="var(--color-forest)" fontSize="18">Your agent’s migration</text>
      <text x="396" y="57" fill="var(--color-forest)" fontSize="18">The whole table is rewritten.</text>
      <g className="font-mono" fontSize="17">
        <text x="40" y="132" fill="var(--color-gray-new-40)">orders.total_cents</text>
        <text x="40" y="177" fill="var(--color-gray-new-40)">integer</text>
        <path d="M122 171H154M148 165L154 171L148 177" stroke="var(--color-forest-line)" strokeWidth="1.5" />
        <text x="170" y="177" fill="var(--color-forest)">bigint</text>
      </g>
      <path d="M40 203H232" stroke="var(--color-forest-line)" strokeOpacity=".3" />
      <text x="40" y="237" fill="var(--color-gray-new-40)" fontSize="16">One column changes.</text>
      <path d="M252 178H374M367 171L374 178L367 185" stroke="var(--color-positive-ink)" strokeWidth="1.5" />
      <text x="312" y="158" textAnchor="middle" fill="var(--color-positive-ink)" fontSize="13" className="font-mono">MCP</text>
      <g transform="translate(396 100)"><RehearsalTable /></g>
      <path d="M718 171V269" stroke="var(--color-ochre-ink)" strokeWidth="2" />
      <Lock x={702} y={124} />
      <g stroke="var(--color-forest-line)" strokeWidth="1.5">
        <path d="M840 202H736M743 196L736 202L743 208" />
        <path d="M840 260H736M743 254L736 260L743 266" />
      </g>
      <text x="840" y="187" textAnchor="end" fill="var(--color-forest)" fontSize="16">Reads</text>
      <text x="840" y="245" textAnchor="end" fill="var(--color-forest)" fontSize="16">Writes</text>
      <text x="718" y="298" textAnchor="middle" fill="var(--color-ochre-ink)" fontSize="15">Blocked</text>
      <text x="40" y="352" fill="var(--color-gray-new-40)" fontSize="14">The change stays on the twin. The findings go back to your agent.</text>
    </svg>
  );
}

function MobileRehearsal() {
  return (
    <svg viewBox="0 0 320 406" fill="none" aria-hidden className="mx-auto block w-full max-w-[420px] lg:hidden">
      <text x="24" y="34" fill="var(--color-forest)" fontSize="20">Your agent’s migration</text>
      <text x="24" y="66" fill="var(--color-gray-new-40)" fontSize="19" className="font-mono">orders.total_cents</text>
      <text x="24" y="95" fill="var(--color-gray-new-40)" fontSize="19" className="font-mono">integer</text>
      <path d="M112 89H148M142 83L148 89L142 95" stroke="var(--color-forest-line)" strokeWidth="1.5" />
      <text x="165" y="95" fill="var(--color-forest)" fontSize="19" className="font-mono">bigint</text>
      <path d="M48 113V151M42 145L48 151L54 145" stroke="var(--color-positive-ink)" strokeWidth="1.5" />
      <text x="64" y="139" fill="var(--color-positive-ink)" fontSize="19" className="font-mono">MCP rehearsal</text>
      <g transform="translate(24 165) scale(.78)"><RehearsalTable compact /></g>
      <Lock x={260} y={181} />
      <path d="M276 229V320" stroke="var(--color-ochre-ink)" strokeWidth="2" />
      <path d="M302 256H286M292 250L286 256L292 262M302 298H286M292 292L286 298L292 304" stroke="var(--color-forest-line)" strokeWidth="1.5" />
      <text x="24" y="370" fill="var(--color-forest)" fontSize="19">The whole table is rewritten.</text>
      <text x="24" y="394" fill="var(--color-gray-new-40)" fontSize="19">Reads and writes must wait.</text>
    </svg>
  );
}

export function MigrationEvidence() {
  return (
    <figure aria-label="A one-column migration rehearsed through MCP reveals a table rewrite and a lock that blocks reads and writes.">
      <div className="overflow-hidden border-y border-forest-line/20 bg-sage max-lg:py-4">
        <DesktopRehearsal />
        <MobileRehearsal />
      </div>
      <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-3 border-b border-stroke py-5">
        <p className="flex flex-wrap items-center gap-x-3 gap-y-1 text-base text-gray-new-10"><span>Table rewrite detected</span><span aria-hidden className="text-gray-new-80 max-sm:hidden">/</span><span className="whitespace-nowrap text-ochre-ink">≥ 10.5s table lock</span></p>
        <details className="group text-sm text-gray-new-40">
          <summary className="min-h-11 cursor-pointer py-3 underline decoration-black/25 underline-offset-4 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-black">View recorded evidence</summary>
          <dl className="mt-2 space-y-2 border-t border-stroke py-3 font-mono text-xs">
            {LOCKS.map(([name, time]) => <div key={name} className="flex justify-between gap-5"><dt>{name}</dt><dd>≥ {time}</dd></div>)}
          </dl>
        </details>
      </div>
      <figcaption className="mt-3 text-xs leading-6 text-gray-new-40">Measured on the demo database. Lock times are sampled lower bounds.</figcaption>
    </figure>
  );
}
