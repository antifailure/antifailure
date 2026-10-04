package manifest

// BoundsExceptionsForTest exposes the one list of deliberately unenforced
// constraints to the external test package, so the gate and the pass cannot
// carry two lists that agree until somebody edits one.
func BoundsExceptionsForTest() [][3]string {
	out := make([][3]string, 0, len(boundsExceptions))
	for _, e := range boundsExceptions {
		out = append(out, [3]string{e.Path, e.Keyword, e.Why})
	}
	return out
}

// TerminalKeysForTest exposes the key names the validator sets aside, so a test
// can hold them to the runner's own table in runner/src/drivers/keys.ts.
func TerminalKeysForTest() []string {
	out := make([]string, 0, len(terminalKeys))
	for k := range terminalKeys {
		out = append(out, k)
	}
	return out
}
