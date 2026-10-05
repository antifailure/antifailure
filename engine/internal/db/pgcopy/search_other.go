//go:build !windows

package pgcopy

// exeSuffix is what a program's file name ends in on this platform.
const exeSuffix = ""

// platformSearchDirs adds nothing: searchDirs already names where Unix
// distributions put the versions that are not on PATH.
func platformSearchDirs() []string { return nil }
