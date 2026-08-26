export * as schema from './schema.ts'
export {
  createPool,
  type Pool,
  type Db,
  type Tenant,
  type PoolOptions,
  type UnscopedOptions,
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
  type PartitionPlan,
  type PartitionState,
  type PartitionOptions,
  type ApplyResult,
  type PruneResult,
  type ArchivedEvent,
} from './partitions.ts'
