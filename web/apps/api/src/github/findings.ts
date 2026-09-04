import type { ReportCounts } from './states.ts'

/** Policy findings and incomplete load are part of the verdict, not just prose. */
export function countPolicyAndLoad(run: Record<string, unknown>, counts: ReportCounts): void {
  if (run.Findings != null && !Array.isArray(run.Findings)) counts.unverified += 1
  for (const item of Array.isArray(run.Findings) ? run.Findings : []) {
    const finding = item as { Level?: unknown } | null
    switch (finding?.Level) {
      case 'fail':
        counts.failed += 1
        break
      case 'warn':
      case 'ignore':
        break
      default:
        counts.unverified += 1
    }
  }
  if (run.Load == null) return
  if (typeof run.Load !== 'object' || Array.isArray(run.Load)) {
    counts.unverified += 1
    return
  }
  const load = run.Load as { Unavailable?: unknown; Sent?: unknown }
  if (typeof load.Unavailable === 'string' && load.Unavailable !== '') {
    counts.blocked += 1
  } else if (typeof load.Sent !== 'number' || !Number.isFinite(load.Sent) || load.Sent <= 0) {
    counts.unverified += 1
  }
}
