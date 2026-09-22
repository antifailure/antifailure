package cli

// LiveWidthOf exposes the width the status line is drawn to, so that the
// fallback when the terminal cannot be measured is testable from outside.
func LiveWidthOf(o *Output) int { return o.liveWidth() }
