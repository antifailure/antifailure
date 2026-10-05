//go:build !windows

package env

// symlinkNotPermitted is false everywhere a process may create a symbolic
// link without asking for a privilege first.
func symlinkNotPermitted(error) bool { return false }
