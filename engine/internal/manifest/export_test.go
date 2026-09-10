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
