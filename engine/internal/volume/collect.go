package volume

import (
	"context"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// Conn is the part of a connection this package uses.
//
// An interface so that the only thing this package can do to a database is ask
// it a question, and so the collector can be exercised without a Postgres.
type Conn interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// relationsQuery reads every ordinary and partitioned table.
//
// pg_table_size and pg_indexes_size rather than pg_total_relation_size,
// because the two are separately actionable: an index larger than the table it
// indexes is a finding on its own and the total hides it. A partitioned parent
// reports zero rows and zero bytes, which is correct of the parent, and its
// partitions are rolled into it below.
const relationsQuery = `
SELECT c.oid, n.nspname, c.relname, c.relkind, c.reltuples::bigint,
       pg_table_size(c.oid), pg_indexes_size(c.oid)
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'p')
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg_toast%'`

// partitionsQuery reads which relation is a partition of which.
//
// Restricted to a parent that is itself partitioned, because pg_inherits also
// carries classic table inheritance, where the child is a table in its own
// right and folding it into its parent would count its rows twice.
const partitionsQuery = `
SELECT i.inhrelid, i.inhparent
FROM pg_inherits i
JOIN pg_class p ON p.oid = i.inhparent
WHERE p.relkind = 'p'`

// keysQuery reads the cardinality of every column anything joins on.
//
// The primary key, the unique constraints and the foreign keys, which is the
// set of columns a plan turns on. Every column would make a profile the size
// of the schema and most of it would answer nothing.
//
// The join to pg_stats is a LEFT JOIN on purpose. A column with no row there
// has never been analyzed, or belongs to a table this role may not read
// statistics for, and both are a reason rather than no distinct values.
const keysQuery = `
SELECT n.nspname, c.relname, a.attname, s.n_distinct
FROM pg_constraint con
JOIN pg_class c ON c.oid = con.conrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN LATERAL unnest(con.conkey) AS k(attnum) ON true
JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = k.attnum
LEFT JOIN pg_stats s
  ON s.schemaname = n.nspname AND s.tablename = c.relname AND s.attname = a.attname
WHERE con.contype IN ('p', 'u', 'f')
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
ORDER BY n.nspname, c.relname, a.attname`

// relation is one row of relationsQuery, before partitions are folded in.
type relation struct {
	oid        uint32
	name       string
	rows       int64
	analyzed   bool
	tableBytes int64
	indexBytes int64
	// partitioned is the parent side, and partitions the children folded into
	// it. largest is the biggest child, because a partitioned table with all
	// of its rows in one partition behaves like an unpartitioned one and an
	// average over the partitions hides that completely.
	partitioned bool
	partitions  int
	largest     int64
}

// Collect reads a database's shape.
//
// ONE ESTIMATOR ON BOTH SIDES, which is why pg_stat_user_tables is not read
// here even though it carries a row count. n_live_tup is the statistics
// collector's figure and pg_class.reltuples is the planner's, they are
// maintained by different machinery and they disagree between an insert and
// the next ANALYZE. The branch side of every comparison this profile feeds
// reads reltuples, in engine/internal/env's branchSize and in
// insights.CaptureSchema, so reading n_live_tup here would divide one
// estimator by another and call the difference a fidelity gap. A table the
// planner has never analyzed therefore has NO row count rather than the
// collector's, and says so, which is the same refusal the rest of this package
// makes: an unknown is not a smaller number.
//
// It never reads a row. Every figure comes from a catalog the planner already
// maintains, so it is cheap enough to run against production itself and
// carries nothing a masking rule would have had an opinion about. That is the
// argument for the whole artifact: the half of the volume question that
// matters needs a read only connection and no change to the application.
//
// source describes the database in words somebody chose, for the artifact.
// Never pass a connection string.
func Collect(ctx context.Context, conn Conn, source string, now time.Time) (Profile, error) {
	p := Profile{CollectedAt: now.UTC(), Source: source}

	var version string
	if err := conn.QueryRow(ctx, "SELECT version()").Scan(&version); err != nil {
		p.Missing = append(p.Missing,
			"the server would not say which version it is: "+oneLine(err))
	} else {
		p.ServerVersion = version
	}

	byOID, err := readRelations(ctx, conn)
	if err != nil {
		return Profile{}, err
	}
	if err := foldPartitions(ctx, conn, byOID, &p); err != nil {
		return Profile{}, err
	}

	live := map[string]*relation{}
	for _, r := range byOID {
		live[r.name] = r
	}
	keys, err := readKeys(ctx, conn, live, &p)
	if err != nil {
		return Profile{}, err
	}

	for _, r := range live {
		if !r.analyzed {
			p.Missing = append(p.Missing, r.name+
				" has never been analyzed, so the planner has no row count for it and this "+
				"profile carries none")
		}
		p.Tables = append(p.Tables, Table{
			Name: r.name, Rows: r.rows, Analyzed: r.analyzed,
			TableBytes: r.tableBytes, IndexBytes: r.indexBytes,
			Partitions: r.partitions, LargestPartitionRows: r.largest,
			Keys: keys[r.name],
		})
	}
	sort.Slice(p.Tables, func(i, j int) bool { return p.Tables[i].Name < p.Tables[j].Name })
	sort.Strings(p.Missing)
	return p, nil
}

// readRelations reads pg_class into a map by oid.
func readRelations(ctx context.Context, conn Conn) (map[uint32]*relation, error) {
	rows, err := conn.Query(ctx, relationsQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[uint32]*relation{}
	for rows.Next() {
		var (
			oid                    uint32
			schemaName, name, kind string
			estimate               int64
			tableBytes, indexBytes int64
		)
		if err := rows.Scan(&oid, &schemaName, &name, &kind, &estimate,
			&tableBytes, &indexBytes); err != nil {
			return nil, err
		}
		r := &relation{
			oid: oid, name: schemaName + "." + name,
			tableBytes: tableBytes, indexBytes: indexBytes,
			partitioned: kind == "p",
		}
		// A relation the planner has never analyzed reports minus one, which
		// would render as a negative row count. Zero is the wrong word for it
		// too, so the count is left at zero and Analyzed carries the fact.
		if estimate >= 0 {
			r.rows, r.analyzed = estimate, true
		}
		out[oid] = r
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// foldPartitions rolls every partition into the table it partitions, and
// removes it from the map.
//
// Rolled up rather than listed, because a manifest and a migration name the
// partitioned table and nothing anybody writes names events_2026_09. A
// comparison against a profile that listed the partitions separately would
// report every one of them as a table the branch does not have, on the first
// month production rolls over.
func foldPartitions(ctx context.Context, conn Conn, byOID map[uint32]*relation, p *Profile) error {
	rows, err := conn.Query(ctx, partitionsQuery)
	if err != nil {
		// A server that will not answer this is not a server with no
		// partitions, and reporting no partitions would be the false clean
		// bill of health this whole package refuses.
		p.Missing = append(p.Missing,
			"the partition catalog could not be read, so a partitioned table's rows are "+
				"reported as its parent holds them, which is none: "+oneLine(err))
		return nil
	}
	parentOf := map[uint32]uint32{}
	for rows.Next() {
		var child, parent uint32
		if err := rows.Scan(&child, &parent); err != nil {
			rows.Close()
			return err
		}
		parentOf[child] = parent
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// The top ancestor, not the immediate parent, because a partitioned table
	// may itself be partitioned and a sub partition's rows belong to the
	// table somebody named.
	root := func(oid uint32) uint32 {
		seen := map[uint32]bool{}
		for {
			parent, ok := parentOf[oid]
			if !ok || seen[oid] {
				return oid
			}
			seen[oid] = true
			oid = parent
		}
	}
	isParent := map[uint32]bool{}
	for _, parent := range parentOf {
		isParent[parent] = true
	}
	// A partitioned parent carries a row count of its own: ANALYZE on the
	// parent estimates the whole table, so adding its partitions to it counts
	// every row twice. The parent's figure is DISCARDED here rather than kept,
	// because the sum over the partitions is the number that also tells us
	// which partition is the largest, and two numbers for one table is how a
	// report ends up quoting a total no row in its own table adds up to.
	//
	// The discard happens only when there is a partition to replace it with.
	// A server that would not answer the partition catalog leaves every parent
	// reporting its own estimate, which is the right fallback and is why
	// foldPartitions says so in Missing rather than returning an error.
	replaced := map[uint32]bool{}
	for child := range parentOf {
		c, ok := byOID[child]
		if !ok {
			continue
		}
		top := root(child)
		parent, ok := byOID[top]
		if !ok || top == child {
			continue
		}
		if !replaced[top] {
			replaced[top] = true
			parent.rows, parent.tableBytes, parent.indexBytes = 0, 0, 0
			parent.analyzed = false
		}
		parent.rows += c.rows
		parent.tableBytes += c.tableBytes
		parent.indexBytes += c.indexBytes
		// Only the leaves are counted. A sub partitioned table's intermediate
		// level holds no rows of its own, so counting it as a partition would
		// report more partitions than there are places rows can be.
		if !isParent[child] {
			parent.partitions++
			if c.rows > parent.largest {
				parent.largest = c.rows
			}
		}
		// A parent whose partitions were all analyzed has a row count, and a
		// parent with one partition nobody analyzed has an undercount. The
		// second is said rather than presented as the total.
		if c.analyzed {
			parent.analyzed = true
		} else {
			p.Missing = append(p.Missing,
				c.name+" is a partition that has never been analyzed, so the row count of the "+
					"table it belongs to is short by whatever it holds")
		}
		delete(byOID, child)
	}
	return nil
}

// readKeys reads the cardinality of the key columns.
func readKeys(
	ctx context.Context, conn Conn, live map[string]*relation, p *Profile,
) (map[string][]Key, error) {
	rows, err := conn.Query(ctx, keysQuery)
	if err != nil {
		p.Missing = append(p.Missing,
			"the key cardinalities could not be read, so nothing here says how many distinct "+
				"values a join column holds: "+oneLine(err))
		return nil, nil
	}
	defer rows.Close()

	out := map[string][]Key{}
	seen := map[string]bool{}
	for rows.Next() {
		var schemaName, table, column string
		var distinct *float64
		if err := rows.Scan(&schemaName, &table, &column, &distinct); err != nil {
			return nil, err
		}
		name := schemaName + "." + table
		r, ok := live[name]
		if !ok {
			// A key on a partition, whose parent already carries the same
			// constraint. Folded away with the partition itself.
			continue
		}
		if seen[name+"."+column] {
			continue
		}
		seen[name+"."+column] = true
		out[name] = append(out[name], resolveDistinct(column, distinct, r.rows))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for name := range out {
		sort.Slice(out[name], func(i, j int) bool { return out[name][i].Column < out[name][j].Column })
	}
	return out, nil
}

// resolveDistinct turns pg_stats.n_distinct into a count.
//
// The catalog stores a negative number to mean a fraction of the table's rows,
// which is how it says "unique" without having to be re-estimated every time
// the table grows. Minus one on a table of four billion rows means four
// billion distinct values, and reporting it as minus one would be a number
// somebody divides by.
func resolveDistinct(column string, n *float64, rows int64) Key {
	k := Key{Column: column}
	switch {
	case n == nil:
		k.Reason = "no statistics for it are visible to this role, so it has never been estimated"
	case *n < 0:
		k.Distinct = int64(-*n * float64(rows))
	default:
		k.Distinct = int64(*n)
	}
	return k
}

func oneLine(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
