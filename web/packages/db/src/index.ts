// The control plane's database, and the only supported way to reach it.
//
// drizzle's `sql` tag is re-exported rather than left to callers, and that is
// not a convenience. A package that imports drizzle-orm itself gets a second
// physical copy of it, and drizzle's SQL type carries private fields, so the
// two copies are nominally different types: `db.execute(sql\`...\`)` then fails
// to compile with a message about `shouldInlineParams` that says nothing about
// the real cause. Exporting it here means there is one drizzle, which is the
// same argument the API makes for re-exporting its permission model.

export * as schema from './schema.ts'
export {
  createPool,
  type Pool,
  type Db,
  type Tenant,
  type PoolOptions,
  type UnscopedOptions,
  type GitHubDeliveryOptions,
} from './client.ts'
export { migrate, migrationsDir, type MigrateResult } from './migrate.ts'
export { appendAudit, verifyAuditChain, auditEntryHash, type AuditInput, type ChainReport } from './audit.ts'
export {
  apply as applyPartitions,
  plan as planPartitions,
  currentPartitions,
  pruneDefault,
  readPartition,
  monthStart,
  addMonths,
  partitionName,
  PARTITIONED_TABLE,
  EVENTS,
  ANALYTICS_EVENTS,
  type PartitionedTable,
  type PartitionPlan,
  type PartitionState,
  type PartitionOptions,
  type ApplyResult,
  type PruneResult,
  type ArchivedEvent,
} from './partitions.ts'

export { sql, eq, and, or, inArray } from 'drizzle-orm'
export type { SQL } from 'drizzle-orm'
