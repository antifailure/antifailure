package pgcrash

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Relations is whether the table and its index still agree after replay.
//
// Two independent readings rather than one. amcheck walks the index and, with
// heapallindexed set, walks the heap and requires an index entry for every
// live tuple in it, which is the strong statement. The two counts are the
// weak one, and they are taken anyway because they fail differently: amcheck
// is an extension that may not be installed, and a check whose only instrument
// is optional is a check that quietly stops running the day somebody builds a
// slimmer image.
type Relations struct {
	// Checked reports whether the comparison could be made at all. False is
	// not a pass, and Why says what stopped it.
	Checked bool `json:"checked"`
	// Why says what stopped it, when it was not checked.
	Why string `json:"why,omitempty"`
	// Agreed is the verdict.
	Agreed bool `json:"agreed"`
	// HeapRows is what a sequential scan counted, and IndexRows is what an
	// index only scan counted.
	HeapRows  int64 `json:"heapRows"`
	IndexRows int64 `json:"indexRows"`
	// Amcheck is what bt_index_check said, or why it could not be asked.
	Amcheck string `json:"amcheck,omitempty"`
}

// checkRelations reads the table two ways and asks amcheck about its index.
func checkRelations(ctx context.Context, opts Options) Relations {
	wl := opts.Workload.withDefaults()
	conn, err := pgx.Connect(ctx, opts.URL)
	if err != nil {
		return Relations{Why: "the database could not be connected to after recovery: " + err.Error()}
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	var rel Relations
	table := wl.Qualified()

	// The heap, read with every index path switched off, so the number comes
	// from the pages themselves. Session settings rather than a transaction,
	// because the two readings have to be separate plans and a reader of this
	// code should see which one is which.
	if _, err := conn.Exec(ctx, "SET enable_indexscan = off; SET enable_indexonlyscan = off; SET enable_bitmapscan = off; SET enable_seqscan = on"); err != nil {
		return Relations{Why: "the planner could not be pinned to a sequential scan: " + err.Error()}
	}
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&rel.HeapRows); err != nil {
		return Relations{Why: "the heap could not be counted: " + err.Error()}
	}

	// The index, read with the sequential path switched off. The predicate is
	// what makes an index only scan legal on the primary key.
	if _, err := conn.Exec(ctx, "SET enable_indexscan = on; SET enable_indexonlyscan = on; SET enable_bitmapscan = on; SET enable_seqscan = off"); err != nil {
		return Relations{Why: "the planner could not be pinned to an index scan: " + err.Error()}
	}
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE id IS NOT NULL").Scan(&rel.IndexRows); err != nil {
		return Relations{Why: "the index could not be counted: " + err.Error()}
	}
	if _, err := conn.Exec(ctx, "RESET ALL"); err != nil {
		return Relations{Why: "the planner settings could not be reset: " + err.Error()}
	}

	rel.Amcheck = amcheck(ctx, conn, wl)
	// amcheck returning anything other than the word it prints on success
	// means it was not asked, which is an unverified run rather than a clean
	// one. Checked stays false and Why carries what happened.
	if rel.Amcheck != AmcheckPassed {
		return Relations{HeapRows: rel.HeapRows, IndexRows: rel.IndexRows, Amcheck: rel.Amcheck,
			Why: "amcheck could not verify the index: " + rel.Amcheck}
	}
	rel.Checked = true
	rel.Agreed = rel.HeapRows == rel.IndexRows
	return rel
}

// AmcheckPassed is what this package records when bt_index_check returned
// without raising. Exported because it is the ONLY value of Relations.Amcheck
// that is a pass, and a renderer that treats any non empty answer as one
// shows "bt_index_check reported a problem" as a clean index.
const AmcheckPassed = "the index verified, with every heap tuple present in it"

// amcheck asks amcheck to verify the table's primary key index against its
// heap.
//
// bt_index_check with heapallindexed does the part that matters here: it
// requires every live tuple in the heap to have an entry in the index. An
// index that lost entries during replay, or a heap that gained tuples the
// index does not know about, is exactly what a badly replayed write ahead log
// produces and exactly what a row count on its own cannot see.
func amcheck(ctx context.Context, conn *pgx.Conn, wl WorkloadOptions) string {
	if _, err := conn.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS amcheck"); err != nil {
		return "the amcheck extension is not available in this database: " + err.Error()
	}
	var index string
	err := conn.QueryRow(ctx, `
		SELECT c.oid::regclass::text
		FROM pg_index i
		JOIN pg_class c ON c.oid = i.indexrelid
		WHERE i.indrelid = $1::regclass AND i.indisprimary`,
		strings.TrimSpace(wl.Qualified())).Scan(&index)
	if err != nil {
		return "the primary key index could not be found: " + err.Error()
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("SELECT bt_index_check(%s::regclass, true)", pgLiteral(index))); err != nil {
		return "bt_index_check reported a problem: " + err.Error()
	}
	return AmcheckPassed
}

// pgLiteral quotes a value as a SQL string literal.
func pgLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
