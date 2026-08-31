import { CodePanel, PageHero, PageSection, PageShell, RelatedGrid } from "@/components/pages/kit";
import { AFTER_HEADING, FeatureList, Lead, MigrationHero, Note, SectionHeading, Split } from "./visuals";

export function DevtoolsPage() {
  return (
    <PageShell>
      <PageHero
        path="/solutions/devtools"
        eyebrow="Solutions · Developer tools"
        title="Schema changes on large tables."
        lead="These teams feel it first, because their users notice p99 the same day. Measure the strongest lock held per table, how long it was held, whether another session was left waiting on it, and how the query plans moved."
        framed={false}
        visual={<MigrationHero tab={1} />}
      />
      <PageSection tone="white">
        <Split
          reverse
          visual={
            <CodePanel label="af insights · events">{`before   Index Scan  events_created_at_idx   12ms
after    Seq Scan    events                  410ms

rows      12,403,881
lock      ACCESS EXCLUSIVE  events  4.2s
blocked   another session was seen waiting on it
rewrite   events rewritten in full`}
            </CodePanel>
          }
        >
          <SectionHeading title="<strong>Users notice p99 immediately.</strong> Large tables plus frequent schema change." />
          <Lead>
            The first supported stack should be exceptional. A broad compatibility list with unreliable
            connectors would destroy trust. Start with Postgres volume, locks and plans, then expand.
          </Lead>
        </Split>
        <div className={AFTER_HEADING}>
          <FeatureList
            items={[
              { title: "Large tables", body: "Exclusive locks and rewrites that never show up on a laptop database." },
              { title: "Query plans", body: "Plan regressions under production-shaped volume." },
              { title: "Statements", body: "Per-statement duration, so the slow one in a batch is named." },
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="sage">
        <Note label="Narrow adapters, complete stack">
          Exceptional Postgres instrumentation first. Say what the run could not measure. Do not
          pretend unsupported components are cloned.
        </Note>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/product/migrations", title: "Migration Safety", description: "Locks, rewrites, plans, lint." },
          { href: "/product/load", title: "Load", description: "Production's own route mix against the branch." },
          { href: "/solutions", title: "All solutions", description: "Teams and jobs." },
        ]}
      />
    </PageShell>
  );
}
