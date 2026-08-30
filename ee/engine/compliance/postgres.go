package compliance

// Reading the evidence out of a real control plane.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Kept apart from the packs so that every control's logic is a pure function
// over a value and every query is here, where it can be run against a real
// Postgres. Mixing them would mean a control could only be tested with a
// database, and a database can only be tested through a control.
//
// Two decisions in here are worth stating.
//
// A query that fails is recorded and does not stop the report. An auditor asked
// for a document about eleven controls, and returning nothing because one query
// timed out serves nobody. What must not happen is a control silently reading
// as "nothing to show" when the truth is "we could not look", so every failure
// is attached to the check it belonged to AND listed at the top of the report.
//
// The chain is anchored outside the window. The first entry inside a period
// carries the hash of the entry before it, which is outside, so verifying only
// what is in the window would report a break at the first row of every report.
// One row before the window is read as an anchor and is not counted.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Reader gathers evidence from a control plane database.
//
// It takes a connection rather than opening one, so that the caller decides the
// credentials. This should be run as a role that can SELECT and nothing else:
// the whole document is a read, and a tool that produces evidence about a
// database it can also write is a tool whose evidence is worth less.
type Reader struct {
	conn *pgx.Conn
	// AppRole is the role whose privileges are checked, which is the role the
	// application connects as rather than the one this is running as.
	AppRole string
	// RetentionDays is how long the operator says audit entries are kept. Read
	// from configuration rather than from the database, because a retention
	// policy that has never deleted anything leaves no trace in the data.
	RetentionDays int
}

// NewReader wraps a connection.
func NewReader(conn *pgx.Conn) *Reader {
	return &Reader{conn: conn, AppRole: "antifailure_app"}
}

// Gather reads everything the packs need for one organization and period.
func (r *Reader) Gather(ctx context.Context, org string, from, to time.Time) (Evidence, error) {
	e := Evidence{
		Org: org, From: from, To: to, GeneratedAt: time.Now().UTC(),
		Unread: map[Check]string{},
	}
	if r.conn == nil {
		return e, fmt.Errorf("compliance: no database connection")
	}

	anchor, entries, err := r.auditEntries(ctx, org, from, to)
	if err != nil {
		e.note(CheckAuditChain, err.Error())
		e.note(CheckAuditCoverage, err.Error())
	} else {
		e.Audit = VerifyChain(anchor, entries)
		e.Access = accessFrom(entries)
	}

	if err := r.posture(ctx, &e); err != nil {
		e.note(CheckTenantIsolation, err.Error())
		e.note(CheckAuditAppendOnly, err.Error())
	}
	e.Posture.AuditRetentionDays = r.RetentionDays

	if err := r.goldens(ctx, org, from, to, &e); err != nil {
		e.note(CheckMasking, err.Error())
		e.note(CheckMaskingBeforeUse, err.Error())
	}
	if err := r.environments(ctx, org, from, to, &e); err != nil {
		e.note(CheckTeardown, err.Error())
	}
	if err := r.egress(ctx, org, from, to, &e); err != nil {
		e.note(CheckEgress, err.Error())
	}

	// Policy has no table in the control plane yet, so there is nothing to read
	// rather than a query that failed. Said plainly, because "not evidenced"
	// with no explanation reads as "we looked and found nothing" and the truth
	// is that there is nowhere to look.
	e.Policy = PolicyEvidence{Configured: false}
	return e, nil
}

func (r *Reader) auditEntries(ctx context.Context, org string, from, to time.Time) (*AuditEntry, []AuditEntry, error) {
	// The anchor row: the last entry before the window. Included so the first
	// entry inside it can have its link checked, and dropped from the count
	// afterwards so the report describes the period it claims to.
	const query = `
		(SELECT seq, org_id::text, coalesce(actor_user_id::text, ''), actor_label, action,
		        target_type, coalesce(target_id, ''), origin, detail, occurred_at,
		        coalesce(prev_hash, ''), entry_hash
		   FROM audit_entries
		  WHERE org_id = $1::uuid AND occurred_at < $2
		  ORDER BY seq DESC LIMIT 1)
		UNION ALL
		(SELECT seq, org_id::text, coalesce(actor_user_id::text, ''), actor_label, action,
		        target_type, coalesce(target_id, ''), origin, detail, occurred_at,
		        coalesce(prev_hash, ''), entry_hash
		   FROM audit_entries
		  WHERE org_id = $1::uuid AND occurred_at >= $2 AND occurred_at < $3
		  ORDER BY seq)
		ORDER BY 1`

	rows, err := r.conn.Query(ctx, query, org, from, to)
	if err != nil {
		return nil, nil, fmt.Errorf("the audit log could not be read: %w", err)
	}
	defer rows.Close()

	var all []AuditEntry
	for rows.Next() {
		var entry AuditEntry
		var detail []byte
		if err := rows.Scan(&entry.Seq, &entry.OrgID, &entry.ActorUserID, &entry.ActorLabel,
			&entry.Action, &entry.TargetType, &entry.TargetID, &entry.Origin, &detail,
			&entry.OccurredAt, &entry.PrevHash, &entry.EntryHash); err != nil {
			return nil, nil, fmt.Errorf("an audit row could not be read: %w", err)
		}
		entry.Detail = json.RawMessage(detail)
		all = append(all, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("the audit log could not be read: %w", err)
	}

	// The anchor is the first row, and only when it precedes the window.
	if len(all) > 0 && all[0].OccurredAt.Before(from) {
		anchor := all[0]
		return &anchor, all[1:], nil
	}
	return nil, all, nil
}

// accessFrom counts membership removals and whether each revoked sessions.
//
// Read out of the audit log rather than from a table, because the audit log is
// the record that is append only and hash chained, and a control about access
// removal that rested on a mutable table would rest on the weaker of the two.
func accessFrom(entries []AuditEntry) AccessEvidence {
	var out AccessEvidence
	revoked := map[string]bool{}
	for _, entry := range entries {
		if entry.Action == "session.revoked" || entry.Action == "sessions.revoked" {
			revoked[entry.TargetID] = true
		}
	}
	for _, entry := range entries {
		if entry.Action != "member.removed" && entry.Action != "member.deprovisioned" {
			continue
		}
		out.Removals++
		if !revoked[entry.TargetID] {
			out.RemovalsWithoutSessionRevoke++
		}
	}
	return out
}

func (r *Reader) posture(ctx context.Context, e *Evidence) error {
	e.Posture.AppRole = r.AppRole

	rows, err := r.conn.Query(ctx, `
		SELECT privilege_type FROM information_schema.role_table_grants
		 WHERE table_name = 'audit_entries' AND grantee = $1`, r.AppRole)
	if err != nil {
		return fmt.Errorf("the grants on the audit log could not be read: %w", err)
	}
	for rows.Next() {
		var grant string
		if err := rows.Scan(&grant); err != nil {
			rows.Close()
			return err
		}
		e.Posture.AuditGrants = append(e.Posture.AuditGrants, grant)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	if err := r.conn.QueryRow(ctx,
		`SELECT coalesce((SELECT rolbypassrls FROM pg_roles WHERE rolname = $1), false)`,
		r.AppRole).Scan(&e.Posture.AppRoleBypassesRLS); err != nil {
		return fmt.Errorf("the application role could not be read: %w", err)
	}

	// A table holding tenant data is one with an org_id column. Read out of the
	// catalogue rather than from a list in this file, for the same reason the
	// control plane's own tenancy suite does it: a table added next year and
	// forgotten is exactly the table this has to notice.
	tables, err := r.conn.Query(ctx, `
		SELECT c.relname, c.relrowsecurity
		  FROM pg_class c
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = 'public' AND c.relkind = 'r'
		   AND EXISTS (SELECT 1 FROM pg_attribute a
		                WHERE a.attrelid = c.oid AND a.attname = 'org_id' AND NOT a.attisdropped)
		 ORDER BY c.relname`)
	if err != nil {
		return fmt.Errorf("the tables holding tenant data could not be read: %w", err)
	}
	defer tables.Close()
	for tables.Next() {
		var name string
		var rls bool
		if err := tables.Scan(&name, &rls); err != nil {
			return err
		}
		e.Posture.TenantTables = append(e.Posture.TenantTables, name)
		if !rls {
			e.Posture.TablesWithoutRLS = append(e.Posture.TablesWithoutRLS, name)
		}
	}
	return tables.Err()
}

func (r *Reader) goldens(ctx context.Context, org string, from, to time.Time, e *Evidence) error {
	rows, err := r.conn.Query(ctx, `
		SELECT version, verified, coalesce(attestation::text, '')
		  FROM golden_versions
		 WHERE org_id = $1::uuid AND created_at >= $2 AND created_at < $3
		 ORDER BY created_at`, org, from, to)
	if err != nil {
		return fmt.Errorf("the golden versions could not be read: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var version, attestation string
		var verified bool
		if err := rows.Scan(&version, &verified, &attestation); err != nil {
			return err
		}
		e.Goldens.Total++
		if !verified {
			e.Goldens.Unverified++
			e.Goldens.UnverifiedIDs = append(e.Goldens.UnverifiedIDs, version)
		}
		if strings.TrimSpace(attestation) == "" {
			continue
		}
		parsed, err := ParseAttestation([]byte(attestation))
		if err != nil {
			// A stored attestation that cannot be decoded is reported as an
			// attestation that does not verify, rather than skipped. Skipping
			// would make a corrupted document indistinguishable from an absent
			// one, and only one of those is a problem.
			parsed = Attestation{Golden: version, Unverifiable: err.Error()}
		}
		e.Attestations = append(e.Attestations, parsed)
	}
	return rows.Err()
}

func (r *Reader) environments(ctx context.Context, org string, from, to time.Time, e *Evidence) error {
	err := r.conn.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE torn_down_at IS NOT NULL)
		  FROM environments
		 WHERE org_id = $1::uuid AND created_at >= $2 AND created_at < $3`,
		org, from, to).Scan(&e.Environments.Created, &e.Environments.Destroyed)
	if err != nil {
		return fmt.Errorf("the environments could not be read: %w", err)
	}

	// A leak is an event the engine emits when teardown could not remove
	// something, not an inference from a missing timestamp. An environment
	// still running is not leaked, and treating one as the other would report a
	// failure on every open pull request.
	rows, err := r.conn.Query(ctx, `
		SELECT DISTINCT coalesce(env_id, '')
		  FROM events
		 WHERE org_id = $1::uuid AND type = 'resource.leaked'
		   AND occurred_at >= $2 AND occurred_at < $3`, org, from, to)
	if err != nil {
		return fmt.Errorf("the leak events could not be read: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var envID string
		if err := rows.Scan(&envID); err != nil {
			return err
		}
		e.Environments.Leaked++
		e.Environments.LeakedIDs = append(e.Environments.LeakedIDs, envID)
	}
	return rows.Err()
}

func (r *Reader) egress(ctx context.Context, org string, from, to time.Time, e *Evidence) error {
	e.Egress.BlockedByHost = map[string]int{}
	rows, err := r.conn.Query(ctx, `
		SELECT coalesce(payload->>'host', ''), coalesce(payload->>'mode', '')
		  FROM events
		 WHERE org_id = $1::uuid AND type = 'egress.decision'
		   AND occurred_at >= $2 AND occurred_at < $3`, org, from, to)
	if err != nil {
		return fmt.Errorf("the egress decisions could not be read: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var host, mode string
		if err := rows.Scan(&host, &mode); err != nil {
			return err
		}
		e.Egress.Decisions++
		if mode == "block" {
			e.Egress.Blocked++
			e.Egress.BlockedByHost[host]++
		}
	}
	return rows.Err()
}
