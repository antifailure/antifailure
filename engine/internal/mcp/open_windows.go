//go:build windows

package mcp

// syscallNonblock is zero on Windows, which has no O_NONBLOCK.
//
// Nothing is lost. The hang this guards against is a Unix fifo, and the
// regular file check that follows the open is what actually refuses every
// non regular file on both platforms.
const syscallNonblock = 0
