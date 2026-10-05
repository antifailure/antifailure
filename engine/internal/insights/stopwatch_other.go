//go:build !windows

package insights

import "time"

// epoch anchors the monotonic reading, so ticks is a count rather than a wall
// clock and cannot step backwards with the system time.
var epoch = time.Now()

func ticks() int64 { return int64(time.Since(epoch)) }

func tickHz() int64 { return int64(time.Second) }
