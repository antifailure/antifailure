package subset

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Orphan is one foreign key that does not resolve, and how many rows are on
// the wrong end of it.
type Orphan struct {
	Key string
	// Rows is how many rows hold a reference to something that is not there.
	Rows int64
	// Example is one such value, so that the report names a row somebody can
	// go and look at rather than a count they have to go and find.
	Example string
}

// CheckIntegrity asks whether every foreign key resolves.
//
// This is the property subsetting exists to provide and the only one worth
// asserting, so it is a query rather than a claim, and it runs against the
// loaded database rather than against the plan. Revalidating the constraints
// would not do: the load suspends enforcement so that a cycle can be loaded at
// all, and a constraint Postgres was not watching reports nothing when it is
// switched back on.
//
// It takes the keys rather than reading them so that a caller can check a
// database against the relationships it believes in, including the virtual
// ones the schema does not know about. A virtual relationship that does not
// hold is exactly the failure this catches and the schema cannot.
func CheckIntegrity(ctx context.Context, conn *pgx.Conn, keys []ForeignKey) ([]Orphan, error) {
	cat, err := ReadCatalog(ctx, conn)
	if err != nil {
		return nil, err
	}
	var out []Orphan
	for _, k := range keys {
		child, ok := cat.Table(k.From)
		if !ok {
			continue
		}
		parent, ok := cat.Table(k.To)
		if !ok {
			continue
		}
		if !hasColumns(child, k.FromColumns) || !hasColumns(parent, k.ToColumns) {
			// A declared relationship naming a column that is not there is a
			// mistake in the manifest, and it is reported as one rather than
			// crashing a query on the way past.
			out = append(out, Orphan{
				Key: k.String(),
				Example: fmt.Sprintf(
					"the relationship names columns that do not exist on %s or %s", k.From, k.To),
			})
			continue
		}

		var set []string
		for _, c := range k.FromColumns {
			set = append(set, "c."+quote(c)+" IS NOT NULL")
		}
		var join []string
		for i := range k.FromColumns {
			join = append(join, fmt.Sprintf("p.%s = c.%s",
				quote(k.ToColumns[i]), quote(k.FromColumns[i])))
		}
		shown := make([]string, 0, len(k.FromColumns))
		for _, c := range k.FromColumns {
			shown = append(shown, "c."+quote(c)+"::text")
		}
		q := fmt.Sprintf(`
SELECT count(*), COALESCE(min(concat_ws(', ', %s)), '')
FROM %s AS c
WHERE %s AND NOT EXISTS (SELECT 1 FROM %s AS p WHERE %s)`,
			strings.Join(shown, ", "), child.Qualified(),
			strings.Join(set, " AND "), parent.Qualified(), strings.Join(join, " AND "))

		var rows int64
		var example string
		if err := conn.QueryRow(ctx, q).Scan(&rows, &example); err != nil {
			return nil, fmt.Errorf("subset: checking %s: %w", k, err)
		}
		if rows > 0 {
			out = append(out, Orphan{Key: k.String(), Rows: rows, Example: example})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// CheckDatabase reads a database's own foreign keys and checks every one of
// them, which is what a test wants when it has no plan to hand.
func CheckDatabase(ctx context.Context, conn *pgx.Conn) ([]Orphan, error) {
	cat, err := ReadCatalog(ctx, conn)
	if err != nil {
		return nil, err
	}
	return CheckIntegrity(ctx, conn, cat.Keys)
}

func hasColumns(t Table, cols []string) bool {
	for _, c := range cols {
		if _, ok := t.ColumnNamed(c); !ok {
			return false
		}
	}
	return true
}

func describeOrphans(orphans []Orphan) []string {
	out := make([]string, 0, len(orphans))
	for _, o := range orphans {
		if o.Rows == 0 {
			out = append(out, fmt.Sprintf("%s: %s", o.Key, o.Example))
			continue
		}
		out = append(out, fmt.Sprintf("%s: %d rows point at something that is not there, the first at (%s)",
			o.Key, o.Rows, o.Example))
	}
	return out
}
