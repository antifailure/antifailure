// The loop that carries the control plane's audit log to a sink.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// WHAT WAS MISSING, AND WHY IT LOOKED PRESENT.
//
// sink.ts and sinks.ts have been complete for as long as this package has
// existed: a bounded queue, four destinations, retries, backoff, and signed
// batch manifests over the chain head. Nothing called any of it. The package
// was not a declared dependency of `ee/web/server`, or of anything else in the
// workspace, so it could not be imported without a package.json change, and
// `npm --prefix ee/web test` ran its suite green on every pull request anyway
// because that command runs every workspace. Tested, green and unreachable.
//
// So this file is deliberately small and is the whole of what was absent: read
// the rows above a cursor, ask whether each organization is entitled, hand the
// entitled ones to the queue, and persist each organization's position.
// Writers serialize within an organization, so a global sequence cursor would
// skip an earlier allocated row that commits after another organization's row.
//
// ---------------------------------------------------------------------------
// The five decisions in here, each of which has a wrong answer that looks fine
// ---------------------------------------------------------------------------
//
// ONE: the cursor advances only past entries a sink ACCEPTED. `Queue.flush`
// leaves a failed batch at the front and never throws, and it reports how many
// entries it delivered, so the first undelivered sequence number is knowable
// exactly. Stopping there makes delivery at least once: a duplicate is
// detectable by `seq` and a gap is not detectable at all, and an audit stream
// has to pick the failure its reader can see.
//
// TWO: a fresh queue per pass, not one held across passes. A queue that
// survives the pass would hold entries the cursor has not advanced past, so the
// next pass would read them again and enqueue a second copy. Retrying is
// therefore reading again from the cursor, and the poll interval is the
// backoff. `Queue.backoffMs` is for a caller that holds its queue; this one
// does not, and the honest version of that is to say so rather than to call a
// method whose value nothing uses.
//
// THREE: one queue per organization within a pass. `sign` takes the org from
// the FIRST entry, so a batch mixing two organizations would carry a manifest
// naming one of them and covering both, and a receiver checking the manifest
// against the batch would be checking the wrong claim.
//
// FOUR: the entitlement is asked per pass, per organization, never cached. An
// organization entitlement that is withdrawn stops forwarding on the next pass
// without a restart, and one that is granted starts it the same way. That is the
// rule `ee/engine/auditsink` already follows per action, and the reason both
// follow it is that a gate evaluated once at startup is a gate that cannot
// expire.
//
// FIVE: an entry belonging to an organization that is NOT entitled is skipped
// and the cursor advances past it. It is not held for a licence that might
// arrive later. That is the behaviour docs/enterprise/audit-stream.md already
// describes for the engine, "accepts every entry and writes none", and here it
// avoids repeatedly reading entries deliberately declined by the licence.
//
// SIX, added when a destination stopped being one per process: the sink is
// resolved per organization, per pass, and an organization's entries reach its
// own destination and nothing else. The installation sink from the environment
// is the destination for an organization that has not chosen one, and only for
// such an organization; a row in audit_stream_destinations decides for its
// organization even when it is switched off. The read query does not return an
// organization with no route at all, so an installation where nobody has
// configured anything reads nothing and advances nothing, and an organization
// that configures a destination later starts from the `from_seq` its row
// carries rather than from wherever an idle position happened to be.
//
// The entitlement is asked BEFORE the destination is resolved, so an
// organization whose entitlement was withdrawn never has its credential opened.

// `sql` through @antifailure/db rather than from drizzle-orm directly, for the
// reason web/packages/db/src/index.ts opens with: a second physical copy of
// drizzle gives a nominally different SQL type and the compile error names a
// private field rather than the cause.
import { sql, type Db, type Pool } from '@antifailure/db'
import { Queue, type Clock, type Entry, type Sink } from './sink.ts'
import type { Route } from './destinations.ts'

export interface ForwarderOptions {
  pool: Pool
  clock: Clock
  /** The installation's destination, from the environment, for every
   *  organization that has not chosen its own. Null when the operator has not
   *  configured one, which on a hosted installation is the ordinary case.
   *  Replicas share delivery positions. */
  sink?: Sink | null
  /** Signs the manifests of batches delivered to the installation sink, so an
   *  object sitting in an archive can be checked without reaching back to the
   *  control plane that wrote it. Required whenever `sink` is. A batch delivered
   *  to an organization's own destination is signed under a key derived from
   *  that organization's credential instead; see `manifestKeyFor`. */
  key?: string
  /**
   * Resolves one organization's own destination, inside the forwarder's scope.
   *
   * Null when this process cannot open a sealed credential, which means
   * AF_PROVIDER_KEY_SECRET is unset. A destination row that exists then is
   * REFUSED rather than skipped, so its entries wait for a sealing key instead
   * of being advanced past and lost.
   */
  destinations?: ((db: Db, orgId: string) => Promise<Route>) | null
  /**
   * Whether one organization may have its audit log forwarded, right now.
   *
   * Supplied rather than implemented, because the answer needs both the licence
   * key this process was started with and the entitlement the control plane
   * holds for that organization, and neither belongs in a package about
   * carrying rows to a sink.
   */
  permitted: (orgId: string, now: Date) => Promise<boolean>
  /** How many entries one pass reads. A ceiling, so a forwarder that has been
   *  stopped for a week catches up over several passes rather than reading a
   *  week of audit log into memory at once. */
  batchSize?: number
  /**
   * How many entries go in one delivery, which is a different question.
   *
   * Not the same knob as `batchSize` and it was one knob until a test caught
   * it. A pass reads what the forwarder can catch up on; a delivery is what one
   * HTTP request may carry, and Splunk's collector and Event Hubs both have a
   * payload ceiling that has nothing to do with how far behind the cursor is.
   * Sharing one number means raising the catch up rate silently raises the
   * request size until an endpoint answers 413, which `permanent()` correctly
   * reads as "never going to work" and gives up on.
   *
   * Defaults to the pass size, so an installation that does not care sets one
   * number and gets one batch per pass.
   */
  deliveryBatchSize?: number
  log?: (line: string) => void
}

/** What one pass did, in enough detail that a test can tell "nothing to do"
 *  from "nothing was allowed" from "nothing was accepted". Three states that a
 *  count of delivered entries renders identically as zero. */
export interface Pass {
  /** Entries read above the cursor. */
  read: number
  /** Entries a sink accepted. */
  delivered: number
  /** Entries skipped because their organization is not entitled. */
  unlicensed: number
  /** Entries skipped because their organization switched its own stream off. */
  disabled: number
  /** Entries held because their organization's destination could not be built:
   *  a credential that will not open, or a URL the customer rule refuses. Held,
   *  not skipped, so they are delivered once the organization repairs it. */
  refused: number
  /** The cursor before and after. Equal means the pass moved nothing, which is
   *  correct for an empty log and is a stuck stream for a failing sink. */
  from: number
  to: number
  /** The organizations this pass touched, sorted, so a test can name a cell
   *  rather than count one. */
  organizations: string[]
}

interface Row extends Record<string, unknown> {
  seq: string | number
  org_id: string
  actor_label: string
  action: string
  target_type: string
  target_id: string | null
  origin: string
  detail: unknown
  occurred_at: Date | string
  entry_hash: string
  /** Whether the organization has a row in audit_stream_destinations, enabled
   *  or not. Read in the same statement, so the decision about which sink an
   *  entry goes to cannot see a different state from the one that selected it. */
  routed: boolean
}

/**
 * Turns a stored row into the entry a sink receives.
 *
 * `seq` is read as a string by the driver, because it is a bigint and a bigint
 * does not fit a JavaScript number in general. Every value this product can
 * produce does, so it is converted here rather than carried as a string through
 * a comparison that would then be lexicographic: '9' is greater than '10' as
 * text, and a cursor that compared that way would stop advancing at the tenth
 * entry and look like a stalled sink.
 *
 * `detail` is jsonb and arrives as an object. A row whose detail is null gets
 * an empty object rather than null, because the canonical form the manifest is
 * computed over sorts keys and cannot sort a null.
 */
function toEntry(row: Row): Entry {
  const occurredAt =
    row.occurred_at instanceof Date ? row.occurred_at : new Date(row.occurred_at)
  return {
    seq: Number(row.seq),
    orgId: row.org_id,
    actor: row.actor_label,
    action: row.action,
    targetType: row.target_type,
    targetId: row.target_id,
    origin: row.origin,
    detail:
      row.detail !== null && typeof row.detail === 'object' && !Array.isArray(row.detail)
        ? (row.detail as Record<string, unknown>)
        : {},
    occurredAt: occurredAt.toISOString(),
    entryHash: row.entry_hash,
  }
}

/**
 * Reads the audit log above a cursor and forwards it.
 *
 * Every method is safe to call on a control plane with no audit entries, no
 * cursor movement and no licence, and none of them throws for any of those:
 * a forwarder that raised on an idle installation would be a process that
 * restarts all night for no reason.
 */
export class Forwarder {
  private readonly opts: ForwarderOptions
  private readonly batchSize: number
  private readonly deliveryBatchSize: number
  private readonly log: (line: string) => void

  constructor(options: ForwarderOptions) {
    if (options.sink && !options.key) {
      // The same refusal configure.ts makes about the environment, repeated here
      // for an embedder that passes a sink directly: a manifest signed under a
      // key nobody chose is a decoration rather than evidence.
      throw new Error('An installation sink needs a manifest signing key, and none was given.')
    }
    this.opts = options
    this.batchSize = options.batchSize ?? 500
    this.deliveryBatchSize = options.deliveryBatchSize ?? this.batchSize
    this.log = options.log ?? (() => {})
  }

  /**
   * One pass: read, gate, deliver, advance.
   *
   * The read and the advance are separate transactions on purpose. The
   * entitlement check opens its own tenant transaction, and asking it from
   * inside the read would hold a connection while waiting for another one,
   * which is how a pool of three deadlocks against itself on an installation
   * with four organizations.
   */
  async pass(): Promise<Pass> {
    const now = this.opts.clock.now()
    const from = await this.cursor()

    // An organization is read when it has a destination row of its own, or when
    // the installation has a sink that covers everybody without one. Nobody else
    // is read at all, so a hosted installation where no organization has
    // configured anything reads nothing on every pass rather than reading every
    // entry and discarding it.
    //
    // `greatest` with the destination's `from_seq` is what makes "entries arrive
    // and then a destination is configured" start at the configuration rather
    // than at the beginning of the organization's history. See migration 0044.
    const installation = this.opts.sink ? sql`true` : sql`false`
    const rows = await this.opts.pool.withAuditForwarder(async (db) =>
      db.execute<Row>(sql`
        SELECT a.seq, a.org_id, a.actor_label, a.action, a.target_type, a.target_id,
               a.origin, a.detail, a.occurred_at, a.entry_hash,
               (d.org_id IS NOT NULL) AS routed
        FROM audit_entries a
        LEFT JOIN audit_stream_positions p ON p.org_id = a.org_id
        LEFT JOIN audit_stream_destinations d ON d.org_id = a.org_id
        WHERE a.seq > greatest(coalesce(p.delivered_seq, 0), coalesce(d.from_seq, 0))
          AND (d.org_id IS NOT NULL OR ${installation})
        ORDER BY a.seq ASC
        LIMIT ${this.batchSize}`),
    )

    const entries = rows.map(toEntry)
    const routed = new Set(rows.filter((r) => r.routed === true).map((r) => r.org_id))
    if (entries.length === 0) {
      return {
        read: 0, delivered: 0, unlicensed: 0, disabled: 0, refused: 0,
        from, to: from, organizations: [],
      }
    }

    // Grouped in sequence order within each organization, which is what the
    // queue delivers in and what `verify` checks a batch for.
    const byOrg = new Map<string, Entry[]>()
    for (const entry of entries) {
      const held = byOrg.get(entry.orgId)
      if (held) held.push(entry)
      else byOrg.set(entry.orgId, [entry])
    }

    let delivered = 0
    let unlicensed = 0
    let disabled = 0
    let refused = 0
    // The lowest sequence number that was neither delivered nor deliberately
    // skipped. The cursor stops below it, so the next pass reads it again.
    let blocked: number | null = null

    for (const [orgId, forOrg] of byOrg) {
      if (!(await this.opts.permitted(orgId, now))) {
        unlicensed += forOrg.length
        await this.advanceOrganization(orgId, forOrg[forOrg.length - 1]!.seq)
        continue
      }

      // Which sink, decided for THIS organization and only for it. The one
      // property the whole per organization design rests on is that the sink
      // built here is handed this organization's entries and nothing else, and
      // `byOrg` is what guarantees it: a queue never sees two organizations.
      const target = await this.resolve(orgId, routed.has(orgId))
      if (target.state === 'off') {
        disabled += forOrg.length
        await this.advanceOrganization(orgId, forOrg[forOrg.length - 1]!.seq)
        continue
      }
      if (target.state === 'refused') {
        // Held below the position, NOT advanced past. A credential that will not
        // open after a sealing key rotation looks exactly like a tampered row,
        // and either way the entries must still be there when it is repaired.
        refused += forOrg.length
        const stuck = forOrg[0]!.seq
        blocked = blocked === null ? stuck : Math.min(blocked, stuck)
        await this.recordAttempt(orgId, now, 0, target.reason)
        this.log(`audit stream: ${orgId} has a destination that cannot be used: ${target.reason}`)
        continue
      }

      const queue = new Queue({
        sink: target.sink,
        clock: this.opts.clock,
        key: target.key,
        batchSize: this.deliveryBatchSize,
        capacity: Math.max(10_000, forOrg.length),
      })
      for (const entry of forOrg) queue.enqueue(entry)

      const sent = await queue.flush()
      delivered += sent
      const completed = forOrg.length - queue.depth
      if (completed > 0) {
        await this.advanceOrganization(orgId, forOrg[completed - 1]!.seq)
      }
      const outcome = queue.state
      await this.recordAttempt(
        orgId, now, sent,
        sent === forOrg.length ? null : 'reason' in outcome ? outcome.reason : `${outcome.state}`,
      )

      // WHAT STILL WAITS, NOT WHAT WAS NOT DELIVERED, and the difference is the
      // whole correctness of the cursor.
      //
      // `flush` has three outcomes per batch and only one of them means "come
      // back for this". Delivered is delivered. A PermanentError, meaning a
      // credential or a payload the endpoint will never accept, DROPS the batch
      // and carries on, because one entry nobody will ever take must not stop
      // every entry behind it. Only a transient failure leaves the batch at the
      // front, and `depth` is the count of exactly those.
      //
      // Reading `forOrg.length - sent` here instead would treat a dropped batch
      // as still owed, so the cursor would stop below it, the next pass would
      // read it again, drop it again, and the stream would stall on it forever
      // while reporting a healthy sink.
      //
      // Delivery is in order and drops happen from the front, so what is left
      // is always a suffix and the first entry of that suffix is the one the
      // cursor must stop below.
      if (queue.depth > 0) {
        const stuck = forOrg[forOrg.length - queue.depth]!.seq
        blocked = blocked === null ? stuck : Math.min(blocked, stuck)
      }
      if (sent < forOrg.length) {
        // `state` carries the sink's own words, which is the only place a
        // reason for a stalled or a lossy stream exists.
        const state = queue.state
        this.log(
          `audit stream: ${target.sink.name()} took ${String(sent)} of ` +
            `${String(forOrg.length)} entries for ${orgId}, ${String(queue.depth)} waiting, ` +
            `${String(queue.dropped)} given up on, and is ${state.state}` +
            ('reason' in state ? `: ${state.reason}` : ''),
        )
      }
    }

    const highest = entries[entries.length - 1]!.seq
    const to = Math.max(from, blocked === null ? highest : blocked - 1)
    if (to > from) await this.advance(to)

    return {
      read: entries.length,
      delivered,
      unlicensed,
      disabled,
      refused,
      from,
      to,
      organizations: [...byOrg.keys()].sort(),
    }
  }

  /**
   * The sink one organization's entries go to on this pass.
   *
   * `routed` is whether the read statement saw a destination row for this
   * organization. It is passed in rather than asked again so that an
   * organization whose row was deleted between the read and here is not
   * silently rerouted to the installation sink: it resolves to `none`, and the
   * installation sink applies only because that is what `none` means.
   */
  private async resolve(
    orgId: string,
    routed: boolean,
  ): Promise<{ state: 'sink'; sink: Sink; key: string } | { state: 'off' } | { state: 'refused'; reason: string }> {
    let route: Route = { state: 'none' }
    if (routed) {
      route = this.opts.destinations
        ? await this.opts.pool.withAuditForwarder((db) => this.opts.destinations!(db, orgId))
        : {
            state: 'refused',
            reason:
              'This organization has a destination and this control plane cannot open its ' +
              'credential, because AF_PROVIDER_KEY_SECRET is not set. Its entries are held.',
          }
    }
    if (route.state === 'sink') {
      return { state: 'sink', sink: route.sink, key: route.manifestKey }
    }
    if (route.state === 'off' || route.state === 'refused') return route
    if (this.opts.sink && this.opts.key) {
      return { state: 'sink', sink: this.opts.sink, key: this.opts.key }
    }
    // Reachable only when a row was read and then deleted before this pass got
    // to it, with no installation sink to fall back on. Held, so the next pass
    // decides with the table as it now is; the read query will not return these
    // entries again unless a destination reappears.
    return { state: 'refused', reason: 'The destination was removed while this pass was running.' }
  }

  /** What happened for one organization on this pass, where it can read it.
   *
   *  A failure increments the count and replaces the reason; a pass that
   *  delivered everything clears both. `last_delivered_at` moves only when
   *  something was accepted, so a stream failing for a week still says when it
   *  last worked. */
  private async recordAttempt(
    orgId: string,
    at: Date,
    sent: number,
    error: string | null,
  ): Promise<void> {
    const when = at.toISOString()
    const deliveredAt = sent > 0 ? when : null
    await this.opts.pool.withAuditForwarder(async (db) => {
      await db.execute(sql`
        INSERT INTO audit_stream_positions (
          org_id, delivered_seq, updated_at, last_attempt_at, last_delivered_at,
          last_error, consecutive_failures)
        VALUES (${orgId}, 0, ${when}, ${when}, ${deliveredAt}, ${error}, ${error === null ? 0 : 1})
        ON CONFLICT (org_id) DO UPDATE SET
          last_attempt_at = EXCLUDED.last_attempt_at,
          last_delivered_at = coalesce(EXCLUDED.last_delivered_at, audit_stream_positions.last_delivered_at),
          last_error = EXCLUDED.last_error,
          consecutive_failures = CASE
            WHEN EXCLUDED.last_error IS NULL THEN 0
            ELSE audit_stream_positions.consecutive_failures + 1
          END`)
    })
  }

  /** Where the last pass got to. Zero on an installation that has never
   *  forwarded anything, which is not the same as up to date and is why the row
   *  exists rather than being inferred from an empty sink. */
  async cursor(): Promise<number> {
    return this.opts.pool.withAuditForwarder(async (db) => {
      const rows = await db.execute<{ delivered_seq: string | number }>(
        sql`SELECT delivered_seq FROM audit_stream_cursor WHERE id`,
      )
      // No row means the policy denied, which on this scope means the migration
      // that creates the row has not run. Saying so beats reporting zero, which
      // reads as a fresh installation and would re-stream the whole log the
      // moment the policy started working.
      if (rows.length === 0) {
        throw new Error(
          'audit_stream_cursor has no row this connection can read. Migration 0043 creates it ' +
            'and grants the read to the audit forwarder scope; a control plane that has not ' +
            'applied it cannot forward, and reporting a cursor of zero here would re-stream ' +
            'the entire audit log to the sink the first time it did.',
        )
      }
      return Number(rows[0]!.delivered_seq)
    })
  }

  /** Moves the cursor forward, never backward. The guard is in the statement
   *  rather than in this process, because two control plane replicas both
   *  forwarding is a duplicate and a cursor that went backwards is a replay of
   *  an organization's whole audit history into somebody's SIEM. */
  private async advance(to: number): Promise<void> {
    await this.opts.pool.withAuditForwarder(async (db) => {
      await db.execute(sql`
        UPDATE audit_stream_cursor
        SET delivered_seq = ${to}, updated_at = ${this.opts.clock.now().toISOString()}
        WHERE id AND delivered_seq < ${to}`)
    })
  }

  private async advanceOrganization(orgId: string, to: number): Promise<void> {
    await this.opts.pool.withAuditForwarder(async (db) => {
      await db.execute(sql`
        INSERT INTO audit_stream_positions (org_id, delivered_seq, updated_at)
        VALUES (${orgId}, ${to}, ${this.opts.clock.now().toISOString()})
        ON CONFLICT (org_id) DO UPDATE
        SET delivered_seq = greatest(audit_stream_positions.delivered_seq, EXCLUDED.delivered_seq),
            updated_at = EXCLUDED.updated_at`)
    })
  }
}

export interface ForwarderHandle {
  /** Stops the schedule. A pass already in flight finishes on its own.
   *
   *  There is deliberately nothing to flush here, and that is a property of the
   *  design rather than an omission. A pass holds its queue for the length of
   *  the pass, so a process that stops mid pass leaves every undelivered entry
   *  ABOVE the cursor, where the next process reads it. boot.ts flushes the
   *  failure store on shutdown because that one buffers in memory across ticks;
   *  this one has nothing that a restart would not pick up. */
  stop(): void
}

/**
 * Runs a pass now and then on an interval.
 *
 * A failure is logged and the schedule continues, which is the rule
 * startMaintenance already follows and for the same reason: the failure that
 * matters is a sink being unreachable for a while, and giving up after the
 * first one is how forwarding stops quietly.
 *
 * Passes never overlap. A slow sink would otherwise have two passes reading the
 * same rows above the same cursor and delivering both copies, which turns one
 * slow destination into a duplicate storm.
 */
export function startForwarder(
  forwarder: Pick<Forwarder, 'pass'>,
  intervalMs: number,
  onError?: (err: unknown) => void,
): ForwarderHandle {
  const report = onError ?? ((err: unknown) => console.error('audit stream', err))
  let busy = false

  const tick = (): void => {
    if (busy) return
    busy = true
    void (async () => {
      try {
        await forwarder.pass()
      } catch (err) {
        report(err)
      } finally {
        busy = false
      }
    })()
  }

  tick()
  const timer = setInterval(tick, intervalMs)
  // Unreferenced, so a forwarder never holds a process open on its own. The
  // control plane's own server is what keeps it alive, and a timer that kept
  // node running would make every test that starts one hang at teardown.
  timer.unref()

  return { stop: () => clearInterval(timer) }
}

