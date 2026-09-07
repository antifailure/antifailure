package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/volume"
)

// The volume profile: what production holds, so that what the branch holds is
// a fraction rather than a sentence.
//
// The fidelity report said "12 tables over 184,000 rows, branched from gv_..."
// and called that a reproduction of production. Nothing in the engine had ever
// been told what production holds, so a golden built from a staging database
// with two hundred rows in events scored exactly the same as one built from a
// production holding four billion. This is the missing denominator, and it is
// a file rather than a connection because the machine that needs it, a pull
// request check, cannot reach production.
//
// Recording it needs no application change and no SDK. Every figure comes from
// a catalog the planner already maintains, over a connection that may be read
// only and should be a replica, and no row is read at any point.

// VolumeJSON is one profile, for a caller that wants the numbers.
type VolumeJSON struct {
	CollectedAt string            `json:"collected_at"`
	Source      string            `json:"source,omitempty"`
	AgeHours    float64           `json:"age_hours"`
	MaxAgeHours float64           `json:"max_age_hours,omitempty"`
	Stale       bool              `json:"stale"`
	Rows        int64             `json:"rows"`
	Tables      []VolumeTableJSON `json:"tables"`
	Missing     []string          `json:"missing,omitempty"`
}

// VolumeTableJSON is one table in the profile.
type VolumeTableJSON struct {
	Name       string `json:"name"`
	Rows       int64  `json:"rows"`
	Analyzed   bool   `json:"analyzed"`
	TableBytes int64  `json:"table_bytes,omitempty"`
	IndexBytes int64  `json:"index_bytes,omitempty"`
	Partitions int    `json:"partitions,omitempty"`
	// LargestPartitionShare is how much of the table sits in its biggest
	// partition, and it is a pointer so that "not partitioned" is absent
	// rather than zero.
	LargestPartitionShare *float64     `json:"largest_partition_share,omitempty"`
	Keys                  []volume.Key `json:"keys,omitempty"`
}

func newVolumeCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "volume",
		Short: "What production holds, and what fraction of it this twin has",
		Long: strings.TrimSpace(`
A fidelity report can say a branch holds twelve tables over a hundred thousand
rows. Without a volume profile it has nothing to compare that against, so a
golden built from a staging database with two hundred rows in it reports as
reproducing a production holding four billion, in the same words and with the
same verdict as a full copy.

A profile is row counts, table and index sizes, partition counts and skew, and
the cardinality of every column anything joins on. It carries no data: every
figure comes from a catalog the planner already maintains, and no row is read.
That is what makes it safe to run against production itself and safe to commit
beside the manifest, which is where the check running on a pull request has to
read it from.

Declare where it lives under database.volume.profile, and how old it may be
under database.volume.max_age. A profile past that age is refused rather than
quoted, the same way a stale golden is refused rather than branched.`),
	}
	cmd.AddCommand(newVolumeRecordCommand(env))
	cmd.AddCommand(newVolumeShowCommand(env))
	return cmd
}

func newVolumeRecordCommand(env *Env) *cobra.Command {
	var branch, out string
	cmd := &cobra.Command{
		Use:   "record",
		Short: "Read production's shape over a read only connection and write the profile",
		Long: strings.TrimSpace(`
Reads the database named by database.source_url_env and writes the profile to
the path database.volume.profile names, which --out overrides.

Nothing here reads a row. It is pg_class, pg_stats and the partition catalogs,
which is why a read only role on a replica is enough and why the result is a
file somebody can read before committing it.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := orchestrator(env, branch, false)
			if err != nil {
				return err
			}
			path := out
			switch {
			case path == "":
				declared, ok := o.VolumeProfilePath()
				if !ok {
					return errors.New(
						"the manifest declares no database.volume.profile, so there is nowhere " +
							"to write this. Add the path, or pass --out")
				}
				path = declared
			case !filepath.IsAbs(path):
				// Against the working directory the command was told to run
				// as, not the process's own. af -C somewhere volume record
				// --out volume.json has to write inside somewhere.
				path = filepath.Join(env.WorkDir, path)
			}

			env.Out.Section("Reading what production holds")
			profile, err := o.RecordVolume(cmd.Context())
			if err != nil {
				return err
			}
			if err := volume.Write(path, profile); err != nil {
				return err
			}

			if env.Out.Format == FormatJSON {
				return env.Out.JSON(volumeJSON(profile, 0, env.Clock.Now()))
			}
			env.Out.Println("")
			env.Out.Status(SymbolOK, path, fmt.Sprintf("%d tables, %s",
				len(profile.Tables), volume.Rows(profile.Rows())))
			printVolumeMissing(env, profile)
			env.Out.Hint("Commit it, so the check running on a pull request can read it. Then",
				"af fidelity")
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "",
		"Branch context to use, defaulting to the checked out one")
	cmd.Flags().StringVar(&out, "out", "",
		"Write the profile here instead of where the manifest says")
	return cmd
}

func newVolumeShowCommand(env *Env) *cobra.Command {
	var branch string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the committed profile, or say why there is none to print",
		Long: strings.TrimSpace(`
Reads the profile the manifest names and prints it, largest table first.

A profile older than database.volume.max_age is REFUSED rather than printed
with a warning beside it. A stale denominator is not a smaller number, it is an
unknown one, and the one thing a number in a report must never be is a figure
somebody quotes without knowing how old it is.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := orchestrator(env, branch, false)
			if err != nil {
				return err
			}
			profile, why := o.VolumeProfile()
			if why != "" {
				if env.Out.Format == FormatJSON {
					return env.Out.JSON(map[string]string{"missing": why})
				}
				env.Out.Empty(env.Out.Wrap(why, 0), "Record one with", "af volume record")
				return nil
			}

			maxAge := o.VolumeMaxAge()
			if env.Out.Format == FormatJSON {
				return env.Out.JSON(volumeJSON(*profile, maxAge, env.Clock.Now()))
			}
			env.Out.Section("What production holds")
			env.Out.Println("")
			env.Out.Printf("  Collected %s from %s.\n",
				profile.CollectedAt.UTC().Format(time.RFC3339), orUnknownSource(profile.Source))
			env.Out.Printf("  %d tables, %s.\n", len(profile.Tables), volume.Rows(profile.Rows()))
			env.Out.Println("")

			tables := append([]volume.Table(nil), profile.Tables...)
			sort.Slice(tables, func(i, j int) bool { return tables[i].Rows > tables[j].Rows })
			rows := make([][]string, 0, len(tables))
			for _, t := range tables {
				rows = append(rows, []string{
					t.Name, volume.Count(t.Rows), volume.Bytes(t.TableBytes),
					volume.Bytes(t.IndexBytes), partitionCount(t),
				})
			}
			env.Out.Table([]Column{
				Col("TABLE"), Num("ROWS"), Num("TABLE SIZE"), Num("INDEXES"),
				Num("PARTITIONS"),
			}, rows)
			// Blocks rather than two more columns. Seven columns do not fit a
			// terminal, and the ones that give up width are the ones that get
			// truncated: "24, largest holds 95 p..." and "id 4,200,..." are a
			// skew and a cardinality nobody can read, printed at the cost of
			// the columns beside them. Both are findings in their own right
			// and both read better with a heading over them.
			printVolumeSkew(env, tables)
			printVolumeKeys(env, tables)
			printVolumeMissing(env, *profile)
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "",
		"Branch context to use, defaulting to the checked out one")
	return cmd
}

// partitionCount says how many partitions a table is divided into.
//
// "none" rather than a blank cell, because a blank under a heading reads as a
// zero somebody is not sure about, and rather than "0", because zero
// partitions and not partitioned are different facts.
func partitionCount(t volume.Table) string {
	if t.Partitions == 0 {
		return "none"
	}
	return strconv.Itoa(t.Partitions)
}

// printVolumeSkew says how evenly each partitioned table is divided.
//
// The share in the largest partition rather than the count alone, because a
// table in ninety partitions with all of its rows in one behaves like an
// unpartitioned table and the count on its own says the opposite. It is the
// finding rather than the fact, which is why it gets a heading instead of a
// cell.
func printVolumeSkew(env *Env, tables []volume.Table) {
	rows := make([][]string, 0, len(tables))
	for _, t := range tables {
		share, ok := t.Skew()
		if !ok {
			continue
		}
		rows = append(rows, []string{
			t.Name,
			fmt.Sprintf("%d, largest holds %s", t.Partitions, volume.Percent(share)),
		})
	}
	if len(rows) == 0 {
		return
	}
	env.Out.Println("")
	env.Out.Println(env.Out.Wrap("How each partitioned table is divided:", 0))
	env.Out.Table([]Column{Col("TABLE"), Flex("PARTITIONS")}, rows)
}

// printVolumeKeys renders the cardinality of every column anything joins on.
//
// A column the planner has no statistics for says so rather than printing a
// zero, because zero distinct values and no estimate are different facts and a
// zero is the one somebody would divide by.
func printVolumeKeys(env *Env, tables []volume.Table) {
	rows := make([][]string, 0, len(tables))
	for _, t := range tables {
		for _, k := range t.Keys {
			distinct := volume.Count(k.Distinct)
			if k.Reason != "" {
				distinct = "unknown"
			}
			rows = append(rows, []string{t.Name + "." + k.Column, distinct})
		}
	}
	if len(rows) == 0 {
		return
	}
	env.Out.Println("")
	env.Out.Println(env.Out.Wrap(
		"The columns anything joins on, and how many distinct values each holds:", 0))
	env.Out.Table([]Column{Col("COLUMN"), Num("DISTINCT")}, rows)
}

func printVolumeMissing(env *Env, p volume.Profile) {
	if len(p.Missing) == 0 {
		return
	}
	env.Out.Println("")
	env.Out.Println(env.Out.Wrap("What this profile could not read:", 0))
	for _, m := range p.Missing {
		env.Out.Printf("  %s %s\n", env.Out.S(StyleWarn, SymbolWarn), env.Out.Wrap(m, 7))
	}
}

func orUnknownSource(s string) string {
	if s == "" {
		return "a source nothing recorded"
	}
	return s
}

func volumeJSON(p volume.Profile, maxAge time.Duration, now time.Time) VolumeJSON {
	doc := VolumeJSON{
		Source: p.Source, Rows: p.Rows(), Missing: p.Missing,
		AgeHours: p.Age(now).Hours(),
		Stale:    volume.Stale(p.CollectedAt, maxAge, now),
	}
	if !p.CollectedAt.IsZero() {
		doc.CollectedAt = p.CollectedAt.UTC().Format(time.RFC3339)
	}
	if maxAge > 0 {
		doc.MaxAgeHours = maxAge.Hours()
	}
	for _, t := range p.Tables {
		row := VolumeTableJSON{
			Name: t.Name, Rows: t.Rows, Analyzed: t.Analyzed,
			TableBytes: t.TableBytes, IndexBytes: t.IndexBytes,
			Partitions: t.Partitions, Keys: t.Keys,
		}
		if share, ok := t.Skew(); ok {
			row.LargestPartitionShare = &share
		}
		doc.Tables = append(doc.Tables, row)
	}
	return doc
}
