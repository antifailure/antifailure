package redact

// matcher answers "which of these literals appear in this string" in one pass
// over the input, without allocating.
//
// Redaction runs on every log line and every event, and the sidecar proxy
// emits one decision event per request, so the cost of a line that contains no
// secret is the cost that decides whether redaction is affordable. The naive
// implementation, a strings.Contains per literal, is linear in the number of
// literals: sixty rule prefilter literals plus seven encodings of every
// registered secret adds up to hundreds of scans of the same short line.
//
// Instead, every literal is indexed by its first two bytes. A 65,536 bit
// bitmap says whether any literal starts with a given pair, so the scan is one
// array lookup per input byte. Only when a pair is set is the candidate list
// for that pair consulted and the full literal compared at that position.
// Literals are high entropy in the cases that matter, so for an ordinary line
// the expected number of comparisons is far below one.
//
// A matcher is immutable once built. Register builds a new one and swaps it,
// so readers never see a half-built table.
type matcher struct {
	// present has one bit per two byte prefix.
	present [1024]uint64
	// cand maps a two byte prefix to the literal indexes that start with it.
	cand map[uint16][]int32
	// lits are the literals in the order the caller supplied them, so that a
	// result index refers back to the caller's own slice.
	lits []string
	// fold makes the match case insensitive over ASCII. Literals must already
	// be lowercase when fold is set.
	fold bool
	// empty short circuits everything when there is nothing to match.
	empty bool
}

func newMatcher(lits []string, fold bool) *matcher {
	m := &matcher{lits: lits, fold: fold, cand: make(map[uint16][]int32, len(lits))}
	if len(lits) == 0 {
		m.empty = true
		return m
	}
	for i, l := range lits {
		if len(l) < 2 {
			// A one byte literal cannot be indexed by a pair. None of the
			// rule literals or registered secrets are that short, and
			// Register enforces a twelve byte minimum, so this is a guard
			// rather than a case.
			continue
		}
		key := uint16(l[0])<<8 | uint16(l[1])
		if _, ok := m.cand[key]; !ok {
			m.present[key>>6] |= 1 << (key & 63)
		}
		m.cand[key] = append(m.cand[key], int32(i))
	}
	m.empty = len(m.cand) == 0
	return m
}

func lowerByte(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

// matchSet returns the indexes of the literals that appear in s, each at most
// once, in ascending order. The returned slice is freshly allocated only when
// something matched; the common case allocates nothing.
func (m *matcher) matchSet(s string) []int32 {
	var out []int32
	var seen map[int32]struct{}
	m.scan(s, func(idx int32) {
		if seen == nil {
			seen = make(map[int32]struct{}, 4)
		}
		if _, ok := seen[idx]; ok {
			return
		}
		seen[idx] = struct{}{}
		out = append(out, idx)
	})
	if len(out) > 1 {
		// Ascending order keeps rule application deterministic.
		for i := 1; i < len(out); i++ {
			for j := i; j > 0 && out[j] < out[j-1]; j-- {
				out[j], out[j-1] = out[j-1], out[j]
			}
		}
	}
	return out
}

// scan walks s and calls fn for every literal occurrence.
func (m *matcher) scan(s string, fn func(idx int32)) {
	if m.empty || len(s) < 2 {
		return
	}
	fold := m.fold
	for i := 0; i+1 < len(s); i++ {
		a, b := s[i], s[i+1]
		if fold {
			a, b = lowerByte(a), lowerByte(b)
		}
		key := uint16(a)<<8 | uint16(b)
		if m.present[key>>6]&(1<<(key&63)) == 0 {
			continue
		}
		for _, idx := range m.cand[key] {
			if hasAt(s, m.lits[idx], i, fold) {
				fn(idx)
			}
		}
	}
}

// hasAt reports whether lit occurs in s starting at position i.
func hasAt(s, lit string, i int, fold bool) bool {
	if i+len(lit) > len(s) {
		return false
	}
	if !fold {
		return s[i:i+len(lit)] == lit
	}
	for j := 0; j < len(lit); j++ {
		if lowerByte(s[i+j]) != lit[j] {
			return false
		}
	}
	return true
}
