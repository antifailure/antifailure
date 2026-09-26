const POLICIES = [
  { service: "Payments", host: "api.stripe.com", mode: "Mock", detail: "A local simulator handles the payment flow.", tone: "text-emerald-800" },
  { service: "Email", host: "Configured mail provider", mode: "Capture", detail: "Messages arrive in the test inbox.", tone: "text-emerald-800" },
  { service: "Unknown destination", host: "No matching host policy", mode: "Block", detail: "The request is refused.", tone: "text-red-800" },
];

export function FailClosedScene() {
  return (
    <figure className="overflow-hidden border border-stroke bg-white">
      <div className="grid grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)] max-md:grid-cols-1">
        <div className="flex flex-col justify-center border-r border-stroke bg-sage p-8 max-md:border-r-0 max-md:border-b max-md:p-6">
          <p className="font-mono text-xs text-gray-new-40">Inside the twin</p>
          <h3 className="mt-4 text-[30px] leading-tight tracking-tighter text-gray-new-10">Your application makes the call.</h3>
          <p className="mt-5 text-base leading-7 text-gray-new-40">The gateway applies the policy you set for that host.</p>
          <div className="mt-8 border-t border-black/15 pt-5 font-mono text-sm text-gray-new-20">Application → Gateway → Policy</div>
        </div>
        <ul className="divide-y divide-stroke px-7 max-md:px-6">
          {POLICIES.map((item) => (
            <li key={item.service} className="py-6">
              <div className="flex items-baseline justify-between gap-4">
                <h4 className="text-lg tracking-extra-tight text-gray-new-10">{item.service}</h4>
                <span className={`font-mono text-xs font-medium ${item.tone}`}>{item.mode}</span>
              </div>
              <p className="mt-2 font-mono text-xs text-gray-new-40">{item.host}</p>
              <p className="mt-3 text-base leading-7 text-gray-new-40">{item.detail}</p>
            </li>
          ))}
        </ul>
      </div>
      <figcaption className="border-t border-stroke px-6 py-4 text-sm leading-6 text-gray-new-40">Example host policies. Configure destinations and modes in <code>antifailure.yaml</code>.</figcaption>
    </figure>
  );
}
