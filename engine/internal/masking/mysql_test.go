package masking_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/verify"
)

func mysqlDialect(t *testing.T) masking.Dialect {
	t.Helper()
	d, err := masking.DialectFor("mysql")
	require.NoError(t, err)
	return d
}

// The names information_schema's COLUMN_TYPE actually spells, read from MySQL
// 8.4 for a table declaring one column of each.
var mysqlTypeCases = []struct{ raw, canonical string }{
	{"varchar(255)", "character varying"},
	{"char(36)", "character"},
	{"text", "text"},
	{"longtext", "text"},
	{"varbinary(64)", "bytea"},
	{"binary(16)", "bytea"},
	{"blob", "bytea"},
	{"tinyint(1)", "smallint"},
	{"tinyint unsigned", "smallint"},
	{"smallint unsigned", "integer"},
	{"int", "integer"},
	{"int(11)", "integer"},
	{"int unsigned", "bigint"},
	{"int(10) unsigned zerofill", "bigint"},
	{"bigint", "bigint"},
	{"bigint unsigned", "numeric"},
	{"decimal(10,2)", "numeric"},
	{"float", "real"},
	{"double", "double precision"},
	{"bit(8)", "bit"},
	{"year", "smallint"},
	{"date", "date"},
	{"datetime(6)", "timestamp without time zone"},
	{"timestamp", "timestamp with time zone"},
	{"time(3)", "time without time zone"},
	{"json", "jsonb"},
	{"enum('active','gone')", "USER-DEFINED"},
	// An enum value is free text, and one that says unsigned does not make
	// the column a number.
	{"enum('unsigned','signed')", "USER-DEFINED"},
	{"set('a','b')", "USER-DEFINED"},
	{"point", "USER-DEFINED"},
	{"geometry", "USER-DEFINED"},
	// A type nobody has a name for stays as it is, which is what lands it in
	// the list a person has to work through.
	{"some_future_type", "some_future_type"},
}

func TestMySQLTypes_AgreeWithTheScanner(t *testing.T) {
	t.Parallel()
	d := mysqlDialect(t)
	for _, c := range mysqlTypeCases {
		require.Equal(t, c.canonical, d.Canonical(c.raw), "masking reads %s as something else", c.raw)
		require.Equal(t, c.canonical, verify.MySQL.Canonical(c.raw),
			"the scanner reads %s as something else", c.raw)
	}
}

func TestMySQL_ReadsTextAsTextBytesAsBytesAndASpatialValueAsUnread(t *testing.T) {
	t.Parallel()
	require.Equal(t, "text", verify.MySQL.Kind("varchar(255)"))
	require.Equal(t, "text", verify.MySQL.Kind("json"))
	require.Equal(t, "text", verify.MySQL.Kind("enum('a','b')"))
	require.Equal(t, "bytea", verify.MySQL.Kind("varbinary(64)"))
	require.Equal(t, "structural", verify.MySQL.Kind("bigint unsigned"))
	require.Equal(t, "structural", verify.MySQL.Kind("datetime(6)"))
	// A point is a location, and its stored form is binary that no detector
	// reads, so it is reported as unread rather than counted as clean.
	require.Equal(t, "unread", verify.MySQL.Kind("point"))
}

func mysqlPeople() masking.TablePlan {
	return masking.TablePlan{
		Table: masking.Table{
			Engine: "mysql", Schema: "shop", Name: "people",
			Columns: []masking.ColumnInfo{
				{Name: "tenant", Type: "bigint unsigned", SQLType: "bigint unsigned"},
				{Name: "id", Type: "bigint", SQLType: "bigint"},
				{Name: "email", Type: "varchar(255)", SQLType: "varchar(255)"},
				{Name: "name", Type: "varchar(255)", SQLType: "varchar(255)"},
			},
			PrimaryKey: []string{"tenant", "id"},
		},
		Columns: []masking.Assignment{
			{Column: masking.ColumnInfo{Name: "email", Type: "varchar(255)"}, Transform: "email"},
			{Column: masking.ColumnInfo{Name: "name", Type: "varchar(255)"}, Transform: "name"},
		},
		ChunkSize: 500, OrderBy: []string{"tenant", "id"},
	}
}

// The executor numbers an update's arguments address first, and MySQL binds
// them in the order the text names them, which is values first. Bound as
// written, the key would be compared with a masked value and the masked value
// written from the key.
func TestMySQL_AnUpdateBindsItsArgumentsInTheOrderItsTextNamesThem(t *testing.T) {
	t.Parallel()
	stmt := mysqlPeople().Compile()
	require.Equal(t,
		"UPDATE `shop`.`people` SET `email` = ?/*3*/, `name` = ?/*4*/ "+
			"WHERE `tenant` = CAST(?/*1*/ AS UNSIGNED) AND `id` = CAST(?/*2*/ AS SIGNED)",
		stmt.SQL)

	bound, err := masking.ArgsInTextOrder(stmt.SQL,
		[]any{"7", "42", "masked@example.test", "Masked Name"})
	require.NoError(t, err)
	require.Equal(t, []any{"masked@example.test", "Masked Name", "7", "42"}, bound)
}

// A resume bound on a composite key compares the first column twice, and both
// comparisons take the same value.
func TestMySQL_ACompositeResumeBoundsEveryColumnWithItsOwnValue(t *testing.T) {
	t.Parallel()
	q := mysqlDialect(t).SelectChunk(mysqlPeople(), []string{"7", "42"})
	require.Equal(t,
		"SELECT `tenant`, `id`, `email`, `name` FROM `shop`.`people` "+
			"WHERE (`tenant` > CAST(?/*1*/ AS UNSIGNED)) OR "+
			"(`tenant` = CAST(?/*1*/ AS UNSIGNED) AND `id` > CAST(?/*2*/ AS SIGNED)) "+
			"ORDER BY `tenant`, `id` LIMIT 500",
		q.SQL)
	require.Equal(t, []any{"7", "42"}, q.Args)

	bound, err := masking.ArgsInTextOrder(q.SQL, q.Args)
	require.NoError(t, err)
	require.Equal(t, []any{"7", "7", "42"}, bound)
}

// MySQL compares an integer column with a string as two doubles, and above two
// to the fifty three a double cannot tell neighbouring keys apart. Every key
// parameter is cast to its column's type, so the comparison is exact.
func TestMySQL_AKeyParameterIsCastToItsColumnsType(t *testing.T) {
	t.Parallel()
	d := mysqlDialect(t)
	cases := map[string]string{
		"bigint":                          "CAST(?/*1*/ AS SIGNED)",
		"int unsigned":                    "CAST(?/*1*/ AS UNSIGNED)",
		"decimal(20,4)":                   "CAST(?/*1*/ AS DECIMAL(20,4))",
		"datetime(6)":                     "CAST(?/*1*/ AS DATETIME(6))",
		"timestamp":                       "CAST(?/*1*/ AS DATETIME)",
		"date":                            "CAST(?/*1*/ AS DATE)",
		"varchar(64)":                     "?/*1*/",
		"binary(16)":                      "?/*1*/",
		"enum('a','b','c')":               "?/*1*/",
		"char(36)":                        "?/*1*/",
		"decimal(10,2) unsigned zerofill": "CAST(?/*1*/ AS DECIMAL(10,2))",
	}
	for sqlType, want := range cases {
		tp := masking.TablePlan{
			Table: masking.Table{
				Engine: "mysql", Schema: "s", Name: "t",
				Columns:    []masking.ColumnInfo{{Name: "k", Type: sqlType, SQLType: sqlType}},
				PrimaryKey: []string{"k"},
			},
			ChunkSize: 10, OrderBy: []string{"k"},
		}
		q := d.SelectChunk(tp, []string{"1"})
		require.Contains(t, q.SQL, "`k` > "+want, "a %s key", sqlType)
	}
}

func TestMySQL_RefusesATableWithNoPrimaryKey(t *testing.T) {
	t.Parallel()
	d := mysqlDialect(t)
	why := d.Unaddressable(masking.Table{Engine: "mysql", Schema: "s", Name: "log"})
	require.Contains(t, why, "no primary key")
	require.Empty(t, d.Unaddressable(masking.Table{
		Engine: "mysql", Schema: "s", Name: "t", PrimaryKey: []string{"id"},
	}))
}

func TestArgsInTextOrder_RefusesWhatItCannotBind(t *testing.T) {
	t.Parallel()
	_, err := masking.ArgsInTextOrder("UPDATE t SET a = ? WHERE id = ?/*1*/", []any{"1", "2"})
	require.ErrorContains(t, err, "unnumbered")

	_, err = masking.ArgsInTextOrder("UPDATE t SET a = ?/*3*/ WHERE id = ?/*1*/", []any{"1"})
	require.ErrorContains(t, err, "argument 3")

	// A question mark inside a quoted name is part of the name.
	bound, err := masking.ArgsInTextOrder(
		"UPDATE `t` SET `what?` = ?/*2*/ WHERE `id` = ?/*1*/", []any{"1", "masked"})
	require.NoError(t, err)
	require.Equal(t, []any{"masked", "1"}, bound)
}

func TestMySQL_IsAmongTheRegisteredDialects(t *testing.T) {
	t.Parallel()
	require.Contains(t, masking.DialectNames(), "mysql")
	require.True(t, strings.HasPrefix(mysqlDialect(t).Placeholder(1), "?"))
}
