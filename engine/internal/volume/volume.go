// Package volume answers the question a fidelity report could not ask: how
// much of production is actually in this copy.
//
// THE FAILURE IT WAS WRITTEN FOR. The database dimension of a fidelity report
// said "12 tables over 184,000 rows, branched from gv_...", called that
// Reproduced, and never compared it against anything. A golden built from a
// staging database holding two hundred rows in events scored exactly the same
// way as one built from production holding four billion, because nothing in
// the engine had ever been told what production holds. The report's whole job
// is to say what the twin does not reproduce, and the single largest thing it
// can fail to reproduce is the size of the data.
//
// It undermines the strongest number the product has, which is what a
// migration really costs. A lock held for five hundred milliseconds over two
// hundred rows is a real measurement and a worthless prediction, and until
// there is a denominator nothing says which of the two it is.
//
// So: a profile is production's own row counts, sizes, partition shape and key
// cardinality, read from the catalogs over a read only connection, written
// down as a dated artifact and committed. Nothing in it is a sample of the
// data and nothing in it is a value from a row. It is a count.
//
// Where each part lives:
//
//   - This file: the artifact, the comparison, and the arithmetic that turns
//     two row counts into a sentence.
//   - collect.go: reading it out of pg_class, pg_stats and the partition
//     catalogs, and saying what could not be read.
//   - artifact.go: writing it, reading it back, and refusing a stale one.
package volume

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Profile is what one database held when it was measured.
//
// It carries no data and no credential, which is what makes it committable:
// every field is a count, a size or a name that is already in the repository's
// migrations. That is deliberate. The profile has to live in the tree beside
// the manifest for the fidelity report to have a denominator on a machine that
// cannot reach production, and an artifact somebody cannot commit is an
// artifact nobody has.
type Profile struct {
	// CollectedAt is when it was read. A profile with no time on it is
	// refused rather than trusted, because the age is the only thing that
	// says whether the denominator is still production's.
	CollectedAt time.Time `json:"collected_at"`
	// Source describes the database it was read from, in words somebody
	// chose. Never a connection string.
	Source string `json:"source,omitempty"`
	// ServerVersion is what the server said it was.
	ServerVersion string `json:"server_version,omitempty"`
	// Tables are the relations it found, schema qualified, sorted.
	Tables []Table `json:"tables"`
	// Missing names what could not be read, and why. A profile that silently
	// omits a table reads exactly like a database that does not have one.
	Missing []string `json:"missing,omitempty"`
}

// Table is one relation and what production holds in it.
type Table struct {
	// Name is schema qualified, because two schemas may hold a table of the
	// same name and an unqualified count would be whichever the search path
	// found, twice.
	Name string `json:"name"`
	// Rows is the planner's estimate, which is what pg_class carries. It is
	// an estimate on both sides of every comparison this package makes, and
	// Analyzed says whether the planner had ever looked at all: a table it
	// has never analyzed reports minus one, and zero is the wrong word for
	// that.
	Rows     int64 `json:"rows"`
	Analyzed bool  `json:"analyzed"`
	// TableBytes and IndexBytes are what it occupies, including its
	// partitions when it has them.
	TableBytes int64 `json:"table_bytes,omitempty"`
	IndexBytes int64 `json:"index_bytes,omitempty"`
	// Partitions is how many partitions it is divided into, and zero means it
	// is not partitioned. LargestPartitionRows is the biggest one, because a
	// partitioned table whose rows are all in one partition behaves like an
	// unpartitioned table and the average hides that completely.
	Partitions           int   `json:"partitions,omitempty"`
	LargestPartitionRows int64 `json:"largest_partition_rows,omitempty"`
	// Keys are the columns of its primary key, its unique constraints and its
	// foreign keys, with how many distinct values each holds. Those are the
	// columns anything joins on, so they are the ones whose cardinality
	// decides what a plan measured on a copy is worth.
	Keys []Key `json:"keys,omitempty"`
}

// Key is one key column and how many distinct values it holds.
type Key struct {
	Column string `json:"column"`
	// Distinct is the estimate, resolved from pg_stats: the catalog stores a
	// negative number to mean a fraction of the row count, and this is the
	// count either way.
	Distinct int64 `json:"distinct"`
	// Reason says why Distinct could not be read, and a non-empty one makes
	// Distinct meaningless. A column the planner has no statistics for, or
	// one this role may not read statistics for, is not a column with no
	// distinct values.
	Reason string `json:"reason,omitempty"`
}

// Skew is how much of a partitioned table sits in its largest partition, and
// false when it is not partitioned or holds nothing.
//
// One number rather than a distribution, because the question somebody asks of
// a partitioned table is whether partitioning bought them anything, and the
// answer is no as soon as one partition holds most of it.
func (t Table) Skew() (float64, bool) {
	if t.Partitions <= 1 || t.Rows <= 0 || t.LargestPartitionRows <= 0 {
		return 0, false
	}
	return float64(t.LargestPartitionRows) / float64(t.Rows), true
}

// Rows is what the whole database holds.
func (p Profile) Rows() int64 {
	var n int64
	for _, t := range p.Tables {
		if t.Rows > 0 {
			n += t.Rows
		}
	}
	return n
}

// Find returns one table by qualified name.
func (p Profile) Find(name string) (Table, bool) {
	for _, t := range p.Tables {
		if t.Name == name {
			return t, true
		}
	}
	return Table{}, false
}

// Age is how old the profile is at now.
func (p Profile) Age(now time.Time) time.Duration {
	if p.CollectedAt.IsZero() {
		return 0
	}
	return now.Sub(p.CollectedAt)
}

// TableRows is one table and how many rows a copy holds in it.
//
// The copy's side of the comparison, read from the branch by whoever is
// holding a connection to it. A separate type from Table because the branch is
// asked for a count and nothing else: sizes, partitions and cardinality on the
// copy answer no question the profile is here to answer.
type TableRows struct {
	Name string `json:"name"`
	Rows int64  `json:"rows"`
}

// TableShare is one table on both sides.
type TableShare struct {
	Name       string `json:"name"`
	Branch     int64  `json:"branch"`
	Production int64  `json:"production"`
}

// Share is the fraction of production's rows the copy holds, and false when
// production holds none, where there is no fraction rather than a zero.
func (s TableShare) Share() (float64, bool) {
	if s.Production <= 0 {
		return 0, false
	}
	return float64(s.Branch) / float64(s.Production), true
}

// Comparison is the copy measured against the profile.
type Comparison struct {
	// CollectedAt is when the production side was read, carried through so
	// that anything quoting the comparison can date it.
	CollectedAt time.Time `json:"collected_at"`
	Source      string    `json:"source,omitempty"`
	// Branch and Production are the totals over the tables BOTH sides have,
	// so the ratio is not moved by a table only one of them holds. Those are
	// reported separately below.
	Branch     int64        `json:"branch_rows"`
	Production int64        `json:"production_rows"`
	Tables     []TableShare `json:"tables,omitempty"`
	// Absent names tables the profile holds and the branch does not, which is
	// production data the copy is missing entirely rather than holding less
	// of.
	Absent []string `json:"absent,omitempty"`
	// Unprofiled names tables the branch holds and the profile does not. It
	// is a gap in the comparison and not a fault in the copy: a table a
	// migration added after the profile was taken is exactly this.
	Unprofiled []string `json:"unprofiled,omitempty"`
}

// Compare measures a branch against a profile.
//
// Every table is matched on its qualified name, and a table only one side has
// is named rather than folded into either total. Folding it in would let a
// migration that adds an empty table move the headline share, which is a
// number that would then be about the migration rather than about the copy.
func Compare(branch []TableRows, p Profile) Comparison {
	c := Comparison{CollectedAt: p.CollectedAt, Source: p.Source}
	have := map[string]int64{}
	for _, t := range branch {
		have[t.Name] = t.Rows
	}
	seen := map[string]bool{}
	for _, t := range p.Tables {
		seen[t.Name] = true
		rows, ok := have[t.Name]
		if !ok {
			c.Absent = append(c.Absent, t.Name)
			continue
		}
		// A table the planner has never analyzed on production's side has no
		// production number, so there is nothing to compare it against. It is
		// left out of the totals and named, rather than compared against a
		// minus one.
		if !t.Analyzed {
			c.Unprofiled = append(c.Unprofiled, t.Name)
			continue
		}
		c.Tables = append(c.Tables, TableShare{Name: t.Name, Branch: rows, Production: t.Rows})
		c.Branch += rows
		c.Production += t.Rows
	}
	for _, t := range branch {
		if !seen[t.Name] {
			c.Unprofiled = append(c.Unprofiled, t.Name)
		}
	}
	sort.Slice(c.Tables, func(i, j int) bool { return c.Tables[i].Name < c.Tables[j].Name })
	sort.Strings(c.Absent)
	sort.Strings(c.Unprofiled)
	return c
}

// Share is the fraction of production's rows the branch holds, over the tables
// both sides have. False when there was nothing to compare, which is not zero:
// a comparison against nothing has not shown the copy to be empty.
func (c Comparison) Share() (float64, bool) {
	if c.Production <= 0 || len(c.Tables) == 0 {
		return 0, false
	}
	return float64(c.Branch) / float64(c.Production), true
}

// Smallest returns the table holding the least of production's rows, which is
// the one worth naming: a copy is only as good as the table somebody's query
// reads, and the average over twelve tables hides the one that is empty.
func (c Comparison) Smallest() (TableShare, bool) {
	var worst TableShare
	var worstShare float64
	found := false
	for _, t := range c.Tables {
		share, ok := t.Share()
		if !ok {
			continue
		}
		if !found || share < worstShare {
			worst, worstShare, found = t, share, true
		}
	}
	return worst, found
}

// ReproducesAt is the share of production's rows a copy has to hold before it
// is production's data rather than a sample of it.
//
// Not one, and that is a fact about the two numbers rather than a tolerance
// somebody wanted. Both sides are pg_class.reltuples, the planner's own
// estimate, which moves on every autovacuum: a byte for byte copy of a table
// reports a different figure from its source as soon as either is vacuumed, so
// an equality test would call every faithful copy a substitution on the first
// vacuum after the restore. Ninety five percent is far above that drift and
// far below any slice anybody takes on purpose. The case this exists for is
// two hundred rows against four billion, which is 0.000005 percent.
const ReproducesAt = 0.95

// MinShortfall is how many rows a table has to be short by before the share
// decides anything about it.
//
// The share alone is unusable at the small end, and this is quantisation
// rather than tolerance. reltuples on a table of twenty rows moves by five
// percent every time one row is inserted or vacuumed away, so a faithful copy
// of a small lookup table crosses ReproducesAt on the ordinary churn of the
// day. Ten rows is below any absence worth reporting and far above that
// noise: a table production holds twenty rows in and the copy holds none is
// short by twenty and still fails, which is the case that matters.
const MinShortfall = 10

// Reproduces reports whether the copy holds production's data.
//
// Every table both sides have has to be at or above the share, or short by
// fewer rows than MinShortfall, and no table the profile names may be missing
// from the copy. A headline share alone would pass a copy holding all of a
// large table and none of a small one, and the small one is usually the one
// somebody's feature reads.
func (c Comparison) Reproduces() bool {
	if len(c.Tables) == 0 || len(c.Absent) > 0 {
		return false
	}
	for _, t := range c.Tables {
		share, ok := t.Share()
		if !ok {
			// Production holds nothing in it, so the copy cannot hold less.
			continue
		}
		if share < ReproducesAt && t.Production-t.Branch >= MinShortfall {
			return false
		}
	}
	return true
}

// Describe renders the comparison as the sentence a fidelity report carries.
func (c Comparison) Describe() string {
	share, ok := c.Share()
	if !ok {
		return "the volume profile names no table this branch also has, so there is nothing to compare against"
	}
	out := fmt.Sprintf("%s against production's %s, which is %s",
		Rows(c.Branch), Rows(c.Production), Percent(share))
	if worst, found := c.Smallest(); found {
		if s, okWorst := worst.Share(); okWorst && s < share {
			out += fmt.Sprintf(". Its smallest table is %s, at %s of production's %s",
				worst.Name, Percent(s), Rows(worst.Production))
		}
	}
	if len(c.Absent) > 0 {
		out += ". Production has " + list(c.Absent) + " and this branch does not"
	}
	return out
}

// Percent renders a share as a percentage that is still a number at the small
// end.
//
// Two significant figures, however many decimal places that takes, because the
// figure this exists to print is 0.000033 and "0 percent" is not it. A share
// small enough to round away entirely says so in words rather than as a zero.
func Percent(share float64) string {
	pct := share * 100
	switch {
	case pct <= 0:
		return "0 percent"
	case pct >= 100:
		return "100 percent"
	case pct >= 10:
		return trunc(pct, 0) + " percent"
	case pct >= 1:
		return trunc(pct, 1) + " percent"
	}
	decimals := 1 - int(math.Floor(math.Log10(pct)))
	if decimals > 12 {
		return "less than 0.000000000001 percent"
	}
	return trunc(pct, decimals) + " percent"
}

// trunc renders toward zero rather than to the nearest.
//
// A copy holding 99.9 percent of production must never print as 100, and
// rounding to nearest overstates it exactly at the boundary somebody would
// quote. Understating the copy is the only direction this number is allowed to
// err in.
func trunc(v float64, decimals int) string {
	f := math.Pow(10, float64(decimals))
	return strconv.FormatFloat(math.Trunc(v*f)/f, 'f', decimals, 64)
}

// Rows renders a row count with separators, exactly.
//
// Exactly, rather than rounded to "4.2 billion", because a rounded figure in a
// report is a figure somebody has to go and check. The separators are what
// make four billion readable without rounding it.
func Rows(n int64) string {
	word := " rows"
	if n == 1 || n == -1 {
		word = " row"
	}
	return Count(n) + word
}

// Count renders a whole number with separators and no unit.
func Count(n int64) string { return group(n) }

// Bytes renders a size in the unit somebody would say it in.
func Bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for size := n / unit; size >= unit; size /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// group puts a separator every three digits.
func group(n int64) string {
	s := strconv.FormatInt(n, 10)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return sign + b.String()
}

// list renders names for one line of prose.
func list(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}
