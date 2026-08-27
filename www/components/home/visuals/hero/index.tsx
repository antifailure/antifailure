"use client";

import { FirewallFilm } from "./FirewallFilm";
import { MigrationFilm } from "./MigrationFilm";
import { StateFilm } from "./StateFilm";
import { type FilmProps } from "./clock";
import { TwinFilm } from "./TwinFilm";
import { WorkloadFilm } from "./WorkloadFilm";

export type FilmKind = "twin" | "state" | "firewall" | "workload" | "migration";
export type { FilmProps };

export function MiniFilm({
  kind,
  active,
  hovered,
}: { kind: FilmKind } & FilmProps) {
  if (kind === "twin") return <TwinFilm active={active} hovered={hovered} />;
  if (kind === "state") return <StateFilm active={active} hovered={hovered} />;
  if (kind === "firewall") return <FirewallFilm active={active} hovered={hovered} />;
  if (kind === "workload") return <WorkloadFilm active={active} hovered={hovered} />;
  return <MigrationFilm active={active} hovered={hovered} />;
}
