export const RAIL = [
  { id: "pr", href: "#from-pr", label: "From your PR" },
  { id: "migration", href: "#migration", label: "Migration Safety" },
  { id: "twins", href: "#twins", label: "Isolated Twins" },
  { id: "firewall", href: "#firewall", label: "Side-Effect Firewall" },
  { id: "gate", href: "#dashboard", label: "Release Gate" },
] as const;

export function FeatureRail({
  active,
  light = false,
}: {
  active: (typeof RAIL)[number]["id"];
  light?: boolean;
}) {
  return (
    <aside className="hidden w-[200px] shrink-0 pt-2 lg:block">
      <ul className="space-y-[11px]">
        {RAIL.map((item) => {
          const isActive = item.id === active;
          return (
            <li key={item.id}>
              <a
                href={item.href}
                className={`flex items-center gap-2.5 text-[13.5px] ${
                  isActive
                    ? light
                      ? "text-black"
                      : "text-white"
                    : light
                      ? "text-black/50"
                      : "text-white/35"
                }`}
              >
                <span
                  className={`h-1.5 w-1.5 rounded-full ${
                    isActive ? (light ? "bg-black" : "bg-white") : "bg-transparent"
                  }`}
                />
                {item.label}
              </a>
            </li>
          );
        })}
      </ul>
    </aside>
  );
}

export function ScrollHandle({ light = false }: { light?: boolean }) {
  return (
    <div className="flex justify-center py-3">
      <div
        className={`h-1 w-24 rounded-full ${light ? "bg-black/20" : "bg-white/15"}`}
      />
    </div>
  );
}
