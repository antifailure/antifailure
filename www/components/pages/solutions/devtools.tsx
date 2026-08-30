import { CodePanel, PageHero, PageSection, PageShell, RelatedGrid } from "@/components/pages/kit";
import { AFTER_HEADING, FeatureList, Lead, MigrationHero, Note, SectionHeading, Split } from "./visuals";

export function DevtoolsPage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Solutions · Developer tools"
        title="Schema changes on large tables."
        lead="The flagship wedge, felt first by teams whose users notice p99 immediately. Measure lock duration, blocked statements, and whether old instances can still read the new schema."
        framed={false}
        visual={<MigrationHero tab={1} />}
      />
      <PageSection tone="white">
        <Split
          reverse
          visual={
            <CodePanel label="plan regression · events">{`baseline   Index Scan  events_created_at_idx   12ms
candidate  Seq Scan    events                  410ms

rows           12,403,881
lock           ACCESS SHARE  1.1s
pool waited    84 connections
old app        cannot decode events.v2 payload`}
            </CodePanel>
          }
        >
          <SectionHeading title="<strong>Users notice p99 immediately.</strong> Large tables plus frequent schema change." />
          <Lead>
            The first supported stack should be exceptional. A broad compatibility list with unreliable
            connectors would destroy trust. Start with Postgres volume, plans, and pools — then expand.
          </Lead>
        </Split>
        <div className={AFTER_HEADING}>
          <FeatureList
            items={[
              { title: "Large tables", body: "Exclusive locks and rewrites that never show up on a laptop database." },
              { title: "Query plans", body: "Plan regressions under production-shaped volume." },
              { title: "Pools", body: "Connection-pool exhaustion during migrate-and-serve." },
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="sage">
        <Note label="Narrow adapters, complete stack">
          Exceptional Postgres instrumentation first. Publish what the twin reproduced. Do not pretend
          unsupported components are cloned.
        </Note>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/product/migrations", title: "Migration Safety", description: "Locks, plans, pools, rollback." },
          { href: "/product/fidelity", title: "Fidelity Graph", description: "What the twin actually reproduced." },
          { href: "/solutions", title: "All solutions", description: "Teams and jobs." },
        ]}
      />
    </PageShell>
  );
}
