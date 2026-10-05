//go:build windows

package pgcopy

import (
	"os"
	"path/filepath"
)

// exeSuffix is what a program's file name ends in on Windows, and without it
// the search below matched nothing even where pg_dump.exe was installed.
const exeSuffix = ".exe"

// platformSearchDirs is where the PostgreSQL installer, which is also what
// 'winget install PostgreSQL.PostgreSQL.18' runs, puts each version. It does not
// add itself to PATH, so a machine that followed doctor's advice would
// otherwise still be told pg_dump is missing.
func platformSearchDirs() []string {
	var out []string
	for _, v := range []string{"ProgramFiles", "ProgramW6432"} {
		if root := os.Getenv(v); root != "" {
			out = append(out, filepath.Join(root, "PostgreSQL", "*", "bin"))
		}
	}
	return out
}
