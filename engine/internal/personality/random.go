// Randomness that follows from a seed, so a personality assignment replays.
//
// This is the Go twin of runner/src/random.ts. The same 32 bit linear
// congruential generator with the Numerical Recipes constants, seeded through
// the same FNV-1a hash, discarding the same poorly distributed low bits. It is
// here rather than imported from the runner because the ENGINE resolves which
// personality drives which workflow and computes each agent's behavioral
// profile, and that resolution must be reproducible in the process that owns
// the run identity and writes the report. Two spellings of one algorithm is
// the hazard this repository keeps finding in itself, so the two are kept
// deliberately identical and a test pins the shared sequence.
//
// It is not cryptographic and must never be used where that matters: it exists
// to be reproducible, not unpredictable.
package personality

// seeded is a generator whose whole sequence follows from its seed.
type seeded struct {
	state    uint32
	original uint32
}

// newSeeded starts a generator from a seed string. The seed is assumed ASCII,
// which every caller here satisfies: ids, workflow names, and integer indices.
func newSeeded(seed string) *seeded {
	s := fnv1a(seed)
	return &seeded{state: s, original: s}
}

// next returns a number in [0, 1).
func (s *seeded) next() float64 {
	// uint32 arithmetic wraps, which is the >>> 0 the TS relies on.
	s.state = s.state*1664525 + 1013904223
	// The top 24 bits, so the poorly distributed low bits never reach a caller.
	return float64(s.state>>8) / float64(0x1000000)
}

// intn returns a whole number in [0, n). Returns 0 for a non positive n, so a
// caller indexing an empty list gets an index rather than a panic.
func (s *seeded) intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(s.next()*float64(n)) % n
}

// probability returns true with the given probability in [0, 1].
func (s *seeded) probability(p float64) bool {
	return s.next() < p
}

// rangeFloat returns a number in [min, max).
func (s *seeded) rangeFloat(min, max float64) float64 {
	return min + s.next()*(max-min)
}

// originalSeed is the starting state, reported into a profile so the exact
// sequence a finding came from can be reconstructed.
func (s *seeded) originalSeed() uint32 {
	return s.original
}

// fnv1a turns a seed string into a starting state. FNV-1a because it is four
// lines, needs no table, and sends two seeds that differ by one character to
// completely different states. A seed that hashed to zero would leave the
// first output fixed regardless of the seed, so zero is moved off itself.
func fnv1a(str string) uint32 {
	var h uint32 = 0x811c9dc5
	for i := 0; i < len(str); i++ {
		h ^= uint32(str[i])
		h *= 0x01000193
	}
	if h == 0 {
		return 0x811c9dc5
	}
	return h
}
