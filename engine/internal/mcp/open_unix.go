//go:build !windows

package mcp

import "syscall"

// syscallNonblock opens without blocking on a named pipe.
//
// Opening a fifo for reading blocks until a writer arrives, which for a server
// handed a path pointing at one would be an indefinite hang rather than a
// refusal. With this flag the open returns and the regular file check that
// follows rejects it, so the refusal is reachable instead of theoretical.
const syscallNonblock = syscall.O_NONBLOCK
