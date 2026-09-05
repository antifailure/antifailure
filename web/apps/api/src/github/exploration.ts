/** A configured experiment needs observed pages and a trace for every goal.
 * An older engine omits this field; that is not a configured experiment. */
export function explorationIncomplete(value: unknown): boolean {
  if (value === null || value === undefined) return false
  if (typeof value !== 'object') return true
  const report = value as Record<string, unknown>
  if (report.Unavailable) return true
  if (
    !Array.isArray(report.Declared) || report.Declared.length === 0 ||
    !Array.isArray(report.Results)
  ) return true
  const seen = new Set<string>()
  for (const item of report.Results) {
    if (!item || typeof item !== 'object') return true
    const result = item as Record<string, unknown>
    const outcome = result.outcome as { verdict?: unknown } | null
    const evidence = result.evidence as { trace?: unknown } | null
    if (
      typeof result.name !== 'string' || seen.has(result.name) ||
      outcome?.verdict !== 'pass' || !Array.isArray(result.visited) ||
      result.visited.length === 0 || typeof evidence?.trace !== 'string' || !evidence.trace
    ) return true
    seen.add(result.name)
  }
  return report.Declared.some(name => typeof name !== 'string' || !seen.has(name)) ||
    seen.size !== report.Declared.length
}
