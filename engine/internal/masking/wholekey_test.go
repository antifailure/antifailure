package masking

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The exact statements for the three shapes a Postgres table's key can take,
// and the checkpoint that has to carry all of it.
//
// The conformance suite checks what every dialect shares. These check what only
// Postgres promises: the key is compared as the table stores it, each parameter
// is cast to its column's type in the form a cast accepts, and ctid is compared
// as a tid.

func peopleTable(key ...string) TablePlan {
	t := Table{
		Engine: enginePostgres, Schema: "app", Name: "people",
		Columns: []ColumnInfo{
			{Name: "tenant", Type: "character varying", SQLType: "character varying(20)"},
			{Name: "id", Type: "uuid", SQLType: "uuid"},
			{Name: "email", Type: "text", SQLType: "text", Nullable: true},
		},
		PrimaryKey: key,
	}
	return TablePlan{
		Table:     t,
		Columns:   []Assignment{{Table: t, Column: t.ColumnNamed("email"), Transform: "email", Link: "email"}},
		ChunkSize: 100,
		OrderBy:   key,
	}
}

func TestPostgres_ACompositeKeyIsComparedAsStoredAgainstCastParameters(t *testing.T) {
	t.Parallel()
	d := postgresDialect{}
	tp := peopleTable("tenant", "id")

	require.Equal(t,
		`UPDATE "app"."people" SET "email" = $3 WHERE ("tenant", "id") = ($1::character varying(20), $2::uuid)`,
		d.Update(tp).SQL,
		"one row is named by every column of its key, compared as stored, so the key's index finds it")

	read := d.SelectChunk(tp, []string{"acme", "0189"})
	require.Equal(t,
		`SELECT "tenant"::text, "id"::text, "email" FROM "app"."people" `+
			`WHERE ("tenant", "id") > ($1::character varying(20), $2::uuid) `+
			`ORDER BY "app"."people"."tenant", "app"."people"."id" LIMIT 100`,
		read.SQL,
		"a page resumes after the whole address, in the key's own order, so a boundary inside a tenant "+
			"resumes inside the tenant and the key's index serves both the bound and the order; the order "+
			"names the table's columns, because a bare name would sort the text the read selects")
	require.Equal(t, []any{"acme", "0189"}, read.Args)
}

func TestPostgres_ASingleColumnKeyIsNotARowValue(t *testing.T) {
	t.Parallel()
	d := postgresDialect{}
	tp := peopleTable("id")

	require.Equal(t, `UPDATE "app"."people" SET "email" = $2 WHERE "id" = $1::uuid`, d.Update(tp).SQL)
	require.Equal(t,
		`SELECT "id"::text, "email" FROM "app"."people" WHERE "id" > $1::uuid ORDER BY "app"."people"."id" LIMIT 100`,
		d.SelectChunk(tp, []string{"0189"}).SQL)
}

func TestPostgres_AKeylessTableIsAddressedAsATid(t *testing.T) {
	t.Parallel()
	d := postgresDialect{}
	tp := peopleTable()
	tp.ChunkSize = 0

	require.Equal(t, `UPDATE "app"."people" SET "email" = $2 WHERE ctid = $1::tid`, d.Update(tp).SQL,
		"ctid compared as a tid is a TID scan; compared as text it was a scan of the whole table")
	require.Equal(t, `SELECT ctid::text, "email" FROM "app"."people"`, d.SelectChunk(tp, nil).SQL)
}

func TestPostgres_AKeyColumnTheCatalogDidNotDescribeIsLeftUncast(t *testing.T) {
	t.Parallel()
	d := postgresDialect{}
	tp := peopleTable("tenant", "id")
	tp.Table.Columns[0].SQLType = ""

	require.Contains(t, d.Update(tp).SQL, `WHERE ("tenant", "id") = ($1, $2::uuid)`,
		"a parameter with no known type is left for Postgres to infer from the column, never cast to a guess")
}

func TestCheckpoint_CarriesTheWholeAddressAndRefusesAnythingElse(t *testing.T) {
	t.Parallel()
	tp := peopleTable("tenant", "id")

	got, err := decodeCheckpoint(tp, encodeCheckpoint([]string{"acme", "0189"}))
	require.NoError(t, err)
	require.Equal(t, []string{"acme", "0189"}, got, "a checkpoint does not come back as the address it saved")

	_, err = decodeCheckpoint(tp, "acme")
	require.ErrorContains(t, err, "first key column alone",
		"a checkpoint from before whole key addressing is read as a position, and resuming there can skip rows")
	require.ErrorContains(t, err, "from the beginning")

	_, err = decodeCheckpoint(tp, encodeCheckpoint([]string{"acme"}))
	require.ErrorContains(t, err, "key changed",
		"a checkpoint naming fewer values than the key has columns is used as though it named a row")
}
