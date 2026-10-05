package local

// ParseDecisions and ParseMessages expose the log parsers to the external test
// package, which is the only place the families that read their answers can be
// imported from without a cycle.
var (
	ParseDecisions   = parseDecisions
	ParseMessages    = parseMessages
	SidecarLinesFrom = sidecarLinesFrom
)
