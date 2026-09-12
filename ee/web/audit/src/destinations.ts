// The collector one organization chose, and the credential it chose it with.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// WHAT WAS MISSING, AND WHY THE HALF THAT EXISTED LOOKED WHOLE.
//
// configure.ts reads a destination from the environment: one collector per
// PROCESS, refused loudly when half configured, and exactly right for a self
// hosted installation where the operator and the customer are the same person.
// `web/apps/api/src/entitlements.ts` then sells `audit_stream` to a HOSTED
// organization, which shares that process with every other organization on the
// installation. So an entitled hosted customer was told their audit log could
// reach their own security information and event management system, and the only
// destination in existence was the operator's. A control naming the wrong
// destination is not a weaker control, it is somebody else's log.
//
// This file is the per organization half. It is the only place a customer's
// credential is sealed, the only place it is opened, and the only place a Sink
// is built from a row.
//
// ---------------------------------------------------------------------------
// THE ONE RULE, stated before the code because every function below obeys it
// ---------------------------------------------------------------------------
//
// THE PLAINTEXT CREDENTIAL LEAVES THIS MODULE IN NOTHING. Not in a return
// value, not in a log line, not in an error message, not in a thrown Error's
// message. `sinkFor` is the only function that opens a ciphertext at all, and
// what it returns is a Sink that already holds the value in a header it builds
// itself. `read` exists for the screen and selects an explicit column list that
// does not include the ciphertext, so no caller can accidentally carry it into a
// response.
//
// That rule is keepable rather than aspirational for the same reason it is
// keepable in web/apps/api/src/providers/store.ts, which this file follows: the
// fingerprint and the last four are everything a page needs, so no screen has a
// reason to ask for the secret.
//
// ---------------------------------------------------------------------------
// WHERE THE SEALING COMES FROM, which is not a new mechanism
// ---------------------------------------------------------------------------
//
// `web/apps/api/src/providers/seal.ts`, unchanged and imported rather than
// copied. It is the control plane's existing answer for a customer supplied
// credential: AES-256-GCM, the sealing key read from AF_PROVIDER_KEY_SECRET and
// never present in Postgres, and associated data binding each ciphertext to what
// it was sealed for so a row copied between tenants does not open.
//
// The second component of that binding is a purpose string rather than a
// provider name, and for this file it is `audit_sink:<kind>`. Two consequences,
// both deliberate. An audit destination's ciphertext cannot be swapped for the
// same organization's Anthropic key, because the associated data differs. And it
// cannot be moved between destination kinds either, so a Splunk token cannot be
// replayed as an Event Hubs signature by editing one text column.
//
// A SEPARATE SEALING KEY WAS CONSIDERED AND REFUSED, and the reason is
// deployment rather than taste. AF_PROVIDER_KEY_SECRET already reaches staging
// and production: `provider_key_secret_enabled` defaults to true in both the
// stack and the module, the value is generated into the Key Vault secret
// `provider-key-secret`, and app.tf already sets the variable on the container.
// A second variable would be a second Key Vault secret, a second targeted apply
// and a second thing to rotate, in exchange for separating two values that are
// already sealed under distinct associated data and read by the same process. It
// would also be a configuration change that does NOT reach production until
// somebody tags, which is a feature that works in staging and not for a
// customer. See docs/enterprise/audit-stream.md, which says this out loud so an
// operator rotating that secret knows both features depend on it.

import { sql, type Db, type Pool } from '@antifailure/db'
import { appendAudit } from '@antifailure/db'
import { open, seal, sealingKeyFrom, SealError, type Sealed } from '@antifailure/api'
import {
  EventHubsSink,
  SplunkSink,
  WebhookSink,
  type Fetcher,
} from './sinks.ts'
import { manifestKeyFor, type Sink } from './sink.ts'

export { sealingKeyFrom, SealError }

/** The collector protocols a customer may name.
 *
 *  The same three `configure.ts` accepts from an operator, and short of the four
 *  sinks.ts implements for the same reason it gives: the object store sink takes
 *  a `put` callback and this side of the product has no S3 or Blob signer to
 *  supply one, so a name accepted here that then wrote nowhere would be the
 *  identical failure one layer along. */
export const KINDS = ['event_hubs', 'splunk', 'webhook'] as const

export type Kind = (typeof KINDS)[number]

export function isKind(value: string): value is Kind {
  return (KINDS as readonly string[]).includes(value)
}

/** A destination as a screen sees it. No ciphertext, no nonce, by construction:
 *  every query that produces one of these names its columns. */
export interface Destination {
  orgId: string
  kind: Kind
  url: string
  indexName: string | null
  sourcetype: string | null
  enabled: boolean
  /** Where delivery began, which is the organization's head sequence at the
   *  moment the destination was saved. */
  fromSeq: number
  /** SHA-256 of the credential, truncated. Lets somebody prove they rotated it
   *  without either value being displayed. */
  fingerprint: string
  /** The last four characters, which is what every collector's own console shows
   *  and is how somebody confirms they pasted the credential they meant to. */
  last4: string
  createdBy: string | null
  createdAt: Date
  updatedAt: Date
}

/** What the stream has actually done for one organization.
 *
 *  Separate from the destination because it is written by the forwarder and the
 *  forwarder holds no write on the destination; see migration 0044. */
export interface Delivery {
  deliveredSeq: number
  lastAttemptAt: Date | null
  lastDeliveredAt: Date | null
  /** The sink's own words about the last failure, or null after a success.
   *  `send` in sinks.ts never reads a collector's response body, so this is a
   *  status code or a transport message and never something a collector echoed. */
  lastError: string | null
  consecutiveFailures: number
}

/** A refusal a person can act on, separate from every other error so a route can
 *  answer 400 rather than 500. Its message is shown to the caller, so nothing
 *  built from a credential is ever put in one. */
export class DestinationRefused extends Error {}

// ---------------------------------------------------------------------------
// What a customer may name
// ---------------------------------------------------------------------------

/**
 * Addresses a destination may not be, whatever DNS says about the name.
 *
 * THIS IS THE CHECK THAT EXISTS BECAUSE THE DESTINATION IS NOW UNTRUSTED.
 *
 * `validateDestination` in sinks.ts requires HTTPS without URL credentials and
 * permits HTTP to a loopback address, which is right for the case it was written
 * for: an operator running a collector beside their own control plane, where
 * loopback is their machine and they chose both ends.
 *
 * A hosted customer's destination arrives from outside, and loopback then means
 * THIS control plane's own loopback. So a customer supplied destination is held
 * to a stricter rule, and the strictness is not a list of blocked hosts but a
 * refusal of everything that is not a public destination:
 *
 *   HTTPS, always, with no loopback exception. This is the single most valuable
 *   line here. The control plane serves plain HTTP on AF_PORT with TLS
 *   terminated at the ingress, and so does everything else inside a deployment
 *   that speaks to itself, so requiring TLS makes every plaintext internal
 *   service unreachable by construction rather than by enumeration.
 *
 *   No URL credentials, because a proxy log on the way keeps them.
 *
 *   No literal address that is not global unicast. Loopback, the private ranges,
 *   link local including the address cloud metadata services answer on, unique
 *   local, multicast, and the unspecified address.
 *
 *   No single label hostname and no `.local`, because those resolve inside a
 *   container network and nowhere else.
 *
 * WHAT THIS CANNOT SEE, said here rather than implied by its absence: a public
 * hostname whose DNS resolves into a private network. No URL check can, and
 * neither can a check made when the row is saved, because resolution can change
 * between the save and the delivery. The control that would close it is egress
 * policy on the container, which this deployment does not have today. It is named
 * as a limit in docs/enterprise/audit-stream.md.
 */
export function checkCustomerDestination(raw: string): string | null {
  let url: URL
  try {
    url = new URL(raw)
  } catch {
    return 'That is not a URL. Give the collector endpoint in full, starting with https://.'
  }
  if (url.protocol !== 'https:') {
    return (
      'A destination has to be https. Plain HTTP would send an audit batch, and the credential ' +
      'it is signed with, across the network in the clear.'
    )
  }
  if (url.username || url.password) {
    return (
      'Take the credentials out of the URL and paste the token on its own. A URL with ' +
      'credentials in it is kept by every proxy log between here and the collector.'
    )
  }
  const host = url.hostname.toLowerCase()
  const literal = addressRefusal(host)
  if (literal) return literal
  if (host.endsWith('.local') || host.endsWith('.localhost') || host === 'localhost') {
    return `${url.hostname} resolves inside a private network and never to a collector this control plane can reach.`
  }
  // An address literal has already been judged above, and a public IPv6 one has
  // no dot in it, so the single label rule is for names only. Without this the
  // rule refused every public IPv6 collector, which the table of cases caught.
  const isLiteral = host.startsWith('[') || /^\d{1,3}(\.\d{1,3}){3}$/.test(host)
  if (!isLiteral && !host.includes('.')) {
    return (
      `${url.hostname} has no domain in it, so it can only name something on this control plane's ` +
      "own network. Give the collector's public hostname."
    )
  }
  return null
}

/**
 * Whether a hostname is a literal address that is not a public one.
 *
 * Literals only, and that is the honest boundary of what a string can decide.
 * Written out rather than taken from a library because the list is short, the
 * ranges are fixed by their own standards, and a dependency here would be a
 * dependency in the path that stores a customer's credential.
 */
function addressRefusal(host: string): string | null {
  const bracketed = host.startsWith('[') && host.endsWith(']')
  const v6 = bracketed ? host.slice(1, -1) : host.includes(':') ? host : null
  if (v6 !== null) {
    const lower = v6.toLowerCase()
    // ::1 loopback, :: unspecified, fc00::/7 unique local, fe80::/10 link local,
    // ff00::/8 multicast. Written as prefixes on the textual form, which is
    // sound because every one of these is fixed in the first two octets.
    if (lower === '::1' || lower === '::' || lower === '::0') return refuseAddress(host, 'loopback or unspecified')
    if (/^f[cd]/.test(lower)) return refuseAddress(host, 'a unique local address')
    if (/^fe[89ab]/.test(lower)) return refuseAddress(host, 'a link local address')
    if (/^ff/.test(lower)) return refuseAddress(host, 'a multicast address')
    // An IPv4 address wearing an IPv6 coat, in the three coats that exist. The
    // URL parser rewrites the dotted tail into hex, so `[::ffff:10.0.0.1]`
    // reaches this function as `::ffff:a00:1`, and a check written against the
    // dotted spelling alone let a private address through. The table of cases
    // caught that; the hex form is what is actually matched now.
    //
    //   ::ffff:a.b.c.d    IPv4 mapped, which a dual stack socket connects to as v4
    //   64:ff9b::a.b.c.d  the NAT64 well known prefix, which a NAT64 gateway
    //                     translates straight to the v4 address inside
    //   ::a.b.c.d         IPv4 compatible, deprecated and refused outright
    const embedded = /^(::ffff:|64:ff9b::|::)([0-9a-f]{1,4}):([0-9a-f]{1,4})$/.exec(lower)
      ?? /^(::ffff:|64:ff9b::|::)(\d+\.\d+\.\d+\.\d+)$/.exec(lower)
    if (embedded) {
      if (embedded[1] === '::') return refuseAddress(host, 'an IPv4 compatible address, which nothing routes')
      const v4 = embedded[3] === undefined
        ? embedded[2]!
        : (() => {
            const hi = parseInt(embedded[2]!, 16)
            const lo = parseInt(embedded[3]!, 16)
            return `${String(hi >> 8)}.${String(hi & 255)}.${String(lo >> 8)}.${String(lo & 255)}`
          })()
      return addressRefusal(v4)
    }
    return null
  }

  const parts = host.split('.')
  if (parts.length !== 4 || !parts.every((p) => /^\d{1,3}$/.test(p))) return null
  const octets = parts.map((p) => Number(p))
  if (octets.some((o) => o > 255)) return refuseAddress(host, 'not an address at all')
  const [a, b] = octets as [number, number, number, number]
  if (a === 0) return refuseAddress(host, 'the unspecified range')
  if (a === 10) return refuseAddress(host, 'a private range')
  if (a === 127) return refuseAddress(host, 'loopback, which is this control plane itself')
  if (a === 172 && b >= 16 && b <= 31) return refuseAddress(host, 'a private range')
  if (a === 192 && b === 168) return refuseAddress(host, 'a private range')
  // 169.254.0.0/16, which is where every cloud's instance metadata service
  // answers. Refused by the HTTPS rule already, since none of them serve TLS,
  // and refused here too because two independent reasons is the right number for
  // the address that hands out the deployment's own credentials.
  if (a === 169 && b === 254) return refuseAddress(host, 'link local, where a cloud metadata service answers')
  if (a === 100 && b >= 64 && b <= 127) return refuseAddress(host, 'a carrier private range')
  if (a === 192 && b === 0) return refuseAddress(host, 'a reserved range')
  if (a >= 224) return refuseAddress(host, 'multicast or reserved')
  return null
}

function refuseAddress(host: string, why: string): string {
  return (
    `${host} is ${why}, so it can only name something inside this control plane's own network. ` +
    'A destination has to be a collector reachable from the public internet.'
  )
}

// ---------------------------------------------------------------------------
// Reading
// ---------------------------------------------------------------------------

interface Row extends Record<string, unknown> {
  org_id: string
  kind: string
  url: string
  index_name: string | null
  sourcetype: string | null
  enabled: boolean
  from_seq: string | number
  fingerprint: string
  last4: string
  created_by: string | null
  created_at: Date | string
  updated_at: Date | string
}

/** The columns a screen may have. Written once so the two callers cannot drift,
 *  and deliberately not `*`: a `SELECT *` here is how a ciphertext starts
 *  travelling into a response. */
const SCREEN_COLUMNS = sql`org_id, kind, url, index_name, sourcetype, enabled, from_seq,
  fingerprint, last4, created_by, created_at, updated_at`

function toDestination(row: Row): Destination {
  return {
    orgId: row.org_id,
    kind: row.kind as Kind,
    url: row.url,
    indexName: row.index_name,
    sourcetype: row.sourcetype,
    enabled: row.enabled,
    fromSeq: Number(row.from_seq),
    fingerprint: row.fingerprint,
    last4: row.last4,
    createdBy: row.created_by,
    createdAt: asDate(row.created_at),
    updatedAt: asDate(row.updated_at),
  }
}

function asDate(value: Date | string): Date {
  return value instanceof Date ? value : new Date(value)
}

/** One organization's destination, or null. Inside a tenant transaction, so the
 *  policy on the table is what scopes it rather than this WHERE clause, and both
 *  are present on purpose. */
export async function read(pool: Pool, orgId: string): Promise<Destination | null> {
  return pool.withTenant({ orgId }, async (db) => {
    const rows = await db.execute<Row>(
      sql`SELECT ${SCREEN_COLUMNS} FROM audit_stream_destinations WHERE org_id = ${orgId}`,
    )
    return rows[0] ? toDestination(rows[0]) : null
  })
}

/** What the stream has done for one organization, or null when it has never run
 *  for them. Read inside the tenant, under the SELECT policy migration 0044
 *  adds, so an organization sees its own state and no other. */
export async function delivery(pool: Pool, orgId: string): Promise<Delivery | null> {
  return pool.withTenant({ orgId }, async (db) => {
    const rows = await db.execute<{
      delivered_seq: string | number
      last_attempt_at: Date | string | null
      last_delivered_at: Date | string | null
      last_error: string | null
      consecutive_failures: number
    }>(sql`
      SELECT delivered_seq, last_attempt_at, last_delivered_at, last_error, consecutive_failures
      FROM audit_stream_positions WHERE org_id = ${orgId}`)
    const row = rows[0]
    if (!row) return null
    return {
      deliveredSeq: Number(row.delivered_seq),
      lastAttemptAt: row.last_attempt_at === null ? null : asDate(row.last_attempt_at),
      lastDeliveredAt: row.last_delivered_at === null ? null : asDate(row.last_delivered_at),
      lastError: row.last_error,
      consecutiveFailures: Number(row.consecutive_failures),
    }
  })
}

// ---------------------------------------------------------------------------
// Writing
// ---------------------------------------------------------------------------

export interface SaveInput {
  orgId: string
  kind: Kind
  url: string
  /** The collector's token, signature or secret. Sealed before it is written and
   *  never held anywhere else. */
  credential: string
  /** Splunk only. Refused on the other two rather than ignored: a value that is
   *  stored and never read is a value somebody will believe is being used. */
  indexName?: string | null
  sourcetype?: string | null
  enabled?: boolean
  actorUserId: string | null
  actorLabel: string
  /** Where the change came from, which an audit reader needs in order to tell a
   *  person at a browser from a token on a build machine. */
  origin: 'web' | 'cli'
}

/**
 * Stores or replaces one organization's destination.
 *
 * THE ORDER INSIDE THE TRANSACTION IS LOAD BEARING and it is not the obvious
 * one:
 *
 *   1. Read the organization's head sequence, BEFORE anything is written.
 *   2. Write the destination with that head as `from_seq`.
 *   3. Append the audit entry for this change.
 *
 * Step 1 before step 3 is what makes the configuration change itself the first
 * thing the new collector receives. Reading the head afterwards would set
 * `from_seq` past the entry that records the change, so a customer would
 * configure a destination, see nothing arrive, and have no way to tell a working
 * stream from a broken one until somebody else did something audited.
 *
 * `from_seq` moves only when the destination is CREATED. Amending a URL or
 * rotating a credential leaves it alone, because the position has moved past it
 * by then and re-reading the head would silently discard whatever was pending
 * when the customer pressed save.
 */
export async function save(
  pool: Pool,
  sealingKey: Buffer,
  input: SaveInput,
  now: Date,
): Promise<Destination> {
  const refusal = checkCustomerDestination(input.url)
  if (refusal) throw new DestinationRefused(refusal)

  const credential = input.credential.trim()
  if (credential.length < 8) {
    throw new DestinationRefused(
      'That is too short to be a collector credential. Paste the token on its own rather than ' +
        'the whole line it came on.',
    )
  }
  if (/\s/.test(credential)) {
    throw new DestinationRefused(
      'That contains a space or a newline. Paste the credential on its own, not the whole export ' +
        'line it came on.',
    )
  }
  if (input.kind !== 'splunk' && (input.indexName || input.sourcetype)) {
    throw new DestinationRefused(
      `An index and a sourcetype are Splunk's, and this destination is ${input.kind}. They would ` +
        'be stored and never read, which is worse than refusing them.',
    )
  }

  let sealed: Sealed
  try {
    sealed = seal(sealingKey, credential, { orgId: input.orgId, provider: purposeOf(input.kind) })
  } catch (err) {
    // Never the cause verbatim: `seal` refuses an empty plaintext and could in
    // principle raise from the cipher, and neither message is worth risking
    // beside a value this function is holding.
    throw new DestinationRefused(
      `The credential could not be sealed for storage: ${err instanceof SealError ? err.message : 'the sealing key was refused'}`,
    )
  }

  return pool.withTenant({ orgId: input.orgId, userId: input.actorUserId ?? undefined }, async (db) => {
    const head = await headSeq(db, input.orgId)
    const rows = await db.execute<Row>(sql`
      INSERT INTO audit_stream_destinations (
        org_id, kind, url, index_name, sourcetype, ciphertext, nonce, key_version,
        fingerprint, last4, enabled, from_seq, created_by, created_at, updated_at)
      VALUES (
        ${input.orgId}, ${input.kind}, ${input.url}, ${input.indexName ?? null},
        ${input.sourcetype ?? null}, ${sealed.ciphertext}, ${sealed.nonce}, ${sealed.keyVersion},
        ${sealed.fingerprint}, ${sealed.last4}, ${input.enabled ?? true}, ${head},
        ${input.actorUserId}, ${now.toISOString()}, ${now.toISOString()})
      ON CONFLICT (org_id) DO UPDATE SET
        kind = EXCLUDED.kind,
        url = EXCLUDED.url,
        index_name = EXCLUDED.index_name,
        sourcetype = EXCLUDED.sourcetype,
        ciphertext = EXCLUDED.ciphertext,
        nonce = EXCLUDED.nonce,
        key_version = EXCLUDED.key_version,
        fingerprint = EXCLUDED.fingerprint,
        last4 = EXCLUDED.last4,
        enabled = EXCLUDED.enabled,
        -- Moves only when a disabled destination is switched back on, and
        -- then to the head read above: entries written while the stream was
        -- off were declined by its owner, and whether they had been read and
        -- skipped yet depends on whether a forwarder happened to be running,
        -- which is not a thing a customer's answer should depend on.
        from_seq = CASE
          WHEN NOT audit_stream_destinations.enabled AND EXCLUDED.enabled
            THEN greatest(audit_stream_destinations.from_seq, EXCLUDED.from_seq)
          ELSE audit_stream_destinations.from_seq
        END,
        updated_at = EXCLUDED.updated_at
      RETURNING ${SCREEN_COLUMNS}`)

    const saved = toDestination(rows[0]!)

    // Audited last, so the entry is above `from_seq` on a creation and is
    // therefore the first thing the destination receives. The detail carries the
    // fingerprint and the last four and never the credential: an audit entry is
    // the one row in this product designed to be copied somewhere else.
    await appendAudit(db, {
      orgId: input.orgId,
      actorLabel: input.actorLabel,
      action: 'audit_stream.destination.saved',
      targetType: 'organization',
      targetId: input.orgId,
      origin: input.origin,
      detail: {
        kind: saved.kind,
        url: saved.url,
        enabled: saved.enabled,
        fromSeq: saved.fromSeq,
        credentialFingerprint: saved.fingerprint,
        credentialLast4: saved.last4,
      },
    })

    return saved
  })
}

/**
 * Switches one organization's stream on or off without touching its credential.
 *
 * Off stops delivery on the next pass and does NOT fall back to the installation
 * sink: an organization that switched its stream off did not ask for its entries
 * to go somewhere else instead. On starts from the head at the moment it was
 * switched, for the reason the upsert in `save` gives, and the change is audited
 * after that head is read, so re-enabling is the first thing the collector sees.
 *
 * Returns null when there is no destination to switch.
 */
export async function setEnabled(
  pool: Pool,
  input: { orgId: string; enabled: boolean; actorUserId: string | null; actorLabel: string; origin: 'web' | 'cli' },
  now: Date,
): Promise<Destination | null> {
  return pool.withTenant({ orgId: input.orgId, userId: input.actorUserId ?? undefined }, async (db) => {
    const head = await headSeq(db, input.orgId)
    const rows = await db.execute<Row>(sql`
      UPDATE audit_stream_destinations SET
        from_seq = CASE
          WHEN NOT enabled AND ${input.enabled} THEN greatest(from_seq, ${head})
          ELSE from_seq
        END,
        enabled = ${input.enabled},
        updated_at = ${now.toISOString()}
      WHERE org_id = ${input.orgId}
      RETURNING ${SCREEN_COLUMNS}`)
    if (!rows[0]) return null
    const saved = toDestination(rows[0])
    await appendAudit(db, {
      orgId: input.orgId,
      actorLabel: input.actorLabel,
      action: input.enabled ? 'audit_stream.destination.enabled' : 'audit_stream.destination.disabled',
      targetType: 'organization',
      targetId: input.orgId,
      origin: input.origin,
      detail: { kind: saved.kind, url: saved.url, fromSeq: saved.fromSeq },
    })
    return saved
  })
}

/** Removes one organization's destination, and audits it.
 *
 *  Returns whether there was one, so a route can answer 404 for a delete of
 *  nothing rather than reporting a removal that removed nothing. */
export async function remove(
  pool: Pool,
  input: { orgId: string; actorUserId: string | null; actorLabel: string; origin: 'web' | 'cli' },
): Promise<boolean> {
  return pool.withTenant({ orgId: input.orgId, userId: input.actorUserId ?? undefined }, async (db) => {
    const rows = await db.execute<{ kind: string; url: string }>(
      sql`DELETE FROM audit_stream_destinations WHERE org_id = ${input.orgId}
          RETURNING kind, url`,
    )
    if (rows.length === 0) return false
    await appendAudit(db, {
      orgId: input.orgId,
      actorLabel: input.actorLabel,
      action: 'audit_stream.destination.removed',
      targetType: 'organization',
      targetId: input.orgId,
      origin: input.origin,
      detail: { kind: rows[0]!.kind, url: rows[0]!.url },
    })
    return true
  })
}

/** The organization's highest audit sequence right now, or zero.
 *
 *  Read inside the tenant, so it is the organization's own chain and not the
 *  installation's. Scoped by the policy and by the WHERE clause both. */
async function headSeq(db: Db, orgId: string): Promise<number> {
  const rows = await db.execute<{ seq: string | null }>(
    sql`SELECT coalesce(max(seq), 0) AS seq FROM audit_entries WHERE org_id = ${orgId}`,
  )
  return Number(rows[0]?.seq ?? 0)
}

/** What a ciphertext is bound to besides the organization. The kind is in it so
 *  a Splunk token cannot be replayed as an Event Hubs signature by editing one
 *  text column, and the `audit_sink:` prefix is what keeps it distinct from a
 *  provider key sealed for the same organization under the same key. */
function purposeOf(kind: Kind): string {
  return `audit_sink:${kind}`
}

// ---------------------------------------------------------------------------
// Building a sink from a row, which is the forwarder's half
// ---------------------------------------------------------------------------

/**
 * What the forwarder found for one organization.
 *
 * FOUR STATES AND NOT A NULLABLE SINK, because a count of zero deliveries
 * renders all four identically and they want four different behaviours.
 *
 *   `sink`     deliver, advance the position.
 *   `off`      the organization turned its own stream off. Skip, and advance,
 *              because holding entries for a switch somebody deliberately set
 *              would make re-enabling replay everything since.
 *   `none`     no destination at all. The installation sink decides, if there is
 *              one, and the read query does not even return these rows when
 *              there is not.
 *   `refused`  a row exists and cannot be turned into a sink: the sealing key
 *              changed, the row was altered, or the URL no longer passes the
 *              customer rule. Skip and DO NOT ADVANCE, so nothing is silently
 *              lost, and record the reason where the customer can read it. A
 *              stuck stream that says why beats a stream that quietly drops.
 */
export type Route =
  | { state: 'sink'; sink: Sink; destination: Destination; manifestKey: string }
  | { state: 'off' }
  | { state: 'none' }
  | { state: 'refused'; reason: string }

/**
 * Resolves one organization's destination into a Sink.
 *
 * Called inside the forwarder's own scope, so the row is reachable under the
 * declaration migration 0043 introduced and 0044 extends to this table, and
 * under a policy that grants the forwarder SELECT and nothing else.
 *
 * Asked every pass rather than cached, and that is the same decision the
 * entitlement check already made for the same reason: a destination a customer
 * changed, disabled or deleted takes effect on the next pass with no restart, and
 * a cache would mean a customer revoking a collector while this process keeps
 * posting to it.
 */
export async function sinkFor(
  db: Db,
  sealingKey: Buffer,
  fetcher: Fetcher,
  orgId: string,
): Promise<Route> {
  const rows = await db.execute<
    Row & { ciphertext: Buffer; nonce: Buffer }
  >(sql`
    SELECT ${SCREEN_COLUMNS}, ciphertext, nonce
    FROM audit_stream_destinations WHERE org_id = ${orgId}`)
  const row = rows[0]
  if (!row) return { state: 'none' }
  if (!row.enabled) return { state: 'off' }

  const destination = toDestination(row)

  // Re-checked at delivery, not only at save. The row could have been written by
  // a path this module does not own, or the rule could have tightened since, and
  // the delivery is the moment that matters.
  const refusal = checkCustomerDestination(destination.url)
  if (refusal) return { state: 'refused', reason: refusal }
  if (!isKind(destination.kind)) {
    return { state: 'refused', reason: `${destination.kind} is not a destination this build can deliver to.` }
  }

  let credential: string
  try {
    credential = open(
      sealingKey,
      { ciphertext: Buffer.from(row.ciphertext), nonce: Buffer.from(row.nonce) },
      { orgId, provider: purposeOf(destination.kind) },
    )
  } catch (err) {
    return {
      state: 'refused',
      reason:
        err instanceof SealError
          ? `${err.message} Save the credential again to repair it.`
          : 'The stored credential could not be opened. Save it again to repair it.',
    }
  }

  return {
    state: 'sink',
    sink: build(destination, credential, fetcher),
    destination,
    manifestKey: manifestKeyFor(credential),
  }
}

function build(destination: Destination, credential: string, fetcher: Fetcher): Sink {
  switch (destination.kind) {
    case 'splunk':
      return new SplunkSink({
        url: destination.url,
        token: credential,
        fetch: fetcher,
        ...(destination.indexName ? { index: destination.indexName } : {}),
        ...(destination.sourcetype ? { sourcetype: destination.sourcetype } : {}),
      })
    case 'event_hubs':
      return new EventHubsSink({
        url: destination.url,
        authorization: credential,
        fetch: fetcher,
      })
    default:
      return new WebhookSink({
        url: destination.url,
        secret: credential,
        fetch: fetcher,
      })
  }
}
