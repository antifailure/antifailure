package oracle

import (
	"strconv"
	"strings"
)

// A path locates a value inside a decoded JSON document, and a pattern selects
// a set of them.
//
// The syntax is the subset of JSONPath people actually write, and nothing
// else. Supporting the whole of JSONPath would mean filters and scripts, which
// is a language, and a manifest key that takes a language is a manifest key
// nobody can validate:
//
//	$.token                  one field at the root
//	$.orders[0].total        one element
//	$.orders[*].placed_at    every element
//	$..created_at            that name at any depth
//	$.meta.*                 every field of one object
//
// Paths are rendered in the same syntax, so a pattern can be copied out of a
// report and pasted into the manifest. That is the only reason the rendering
// and the matching live in one file: they have to agree, and two files is how
// they stop agreeing.

// segment is one step of a path.
type segment struct {
	// key is the object field, when this step is one.
	key string
	// index is the array position, when this step is one.
	index int
	// isIndex distinguishes the two, because a field can be named "0".
	isIndex bool
}

func keySegment(k string) segment { return segment{key: k} }
func indexSegment(i int) segment  { return segment{index: i, isIndex: true} }
func rootPath() []segment         { return nil }
func childOf(p []segment, s segment) []segment {
	// Copied rather than appended in place. The recursive walk holds several
	// live paths at once and a shared backing array makes a sibling's last
	// segment appear in a finding that was already recorded.
	out := make([]segment, len(p), len(p)+1)
	copy(out, p)
	return append(out, s)
}

// renderPath writes a path in the syntax a pattern uses.
func renderPath(p []segment) string {
	var b strings.Builder
	b.WriteString("$")
	for _, s := range p {
		if s.isIndex {
			b.WriteString("[")
			b.WriteString(strconv.Itoa(s.index))
			b.WriteString("]")
			continue
		}
		b.WriteString(".")
		b.WriteString(s.key)
	}
	return b.String()
}

// token is one step of a pattern.
type token struct {
	// descend is a "..", which matches any number of segments including none.
	descend bool
	// wildcard is a "*" or a "[*]", which matches exactly one segment.
	wildcard bool
	// key matches an object field of that name.
	key string
	// index matches an array position.
	index   int
	isIndex bool
}

// parsePattern turns a pattern string into tokens.
//
// An unparseable pattern returns false rather than matching nothing silently.
// The manifest validator uses that to refuse the pattern at the point somebody
// writes it, which is the only moment they can still remember what they meant.
func parsePattern(pattern string) ([]token, bool) {
	s := strings.TrimSpace(pattern)
	if s == "" {
		return nil, false
	}
	// A leading $ is optional, because half the world writes "$.a.b" and the
	// other half writes ".a.b", and refusing either would be a rule about
	// punctuation rather than about behaviour.
	s = strings.TrimPrefix(s, "$")

	var out []token
	for s != "" {
		switch {
		case strings.HasPrefix(s, ".."):
			out = append(out, token{descend: true})
			s = s[2:]
			// "$..name" is a descend followed by the name. The name is read on
			// the next turn of the loop, which needs the dot back, and putting
			// it back is cheaper than a second parser state.
			if s != "" && !strings.HasPrefix(s, "[") {
				s = "." + s
			}
		case strings.HasPrefix(s, "."):
			s = s[1:]
			end := strings.IndexAny(s, ".[")
			if end < 0 {
				end = len(s)
			}
			name := s[:end]
			if name == "" {
				return nil, false
			}
			if name == "*" {
				out = append(out, token{wildcard: true})
			} else {
				out = append(out, token{key: name})
			}
			s = s[end:]
		case strings.HasPrefix(s, "["):
			end := strings.Index(s, "]")
			if end < 0 {
				return nil, false
			}
			inner := s[1:end]
			switch {
			case inner == "*":
				out = append(out, token{wildcard: true})
			default:
				n, err := strconv.Atoi(inner)
				if err != nil || n < 0 {
					return nil, false
				}
				out = append(out, token{index: n, isIndex: true})
			}
			s = s[end+1:]
		default:
			return nil, false
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	// A trailing descend matches every path there is, so "$.." would ignore
	// the whole body and "$.orders.." would ignore everything under orders.
	// Both read as though they were about to name something, and both are far
	// broader than whoever typed them meant. Refusing beats matching
	// everything: an ignore rule that silently swallows the document is the
	// one mistake this package must not let somebody make quietly.
	if out[len(out)-1].descend {
		return nil, false
	}
	return out, true
}

// ValidPattern reports whether a field pattern can be parsed, for the manifest
// validator.
func ValidPattern(pattern string) bool {
	_, ok := parsePattern(pattern)
	return ok
}

// matcher holds the compiled patterns for one comparison.
type matcher struct {
	patterns [][]token
}

// newMatcher compiles the patterns, discarding any that do not parse.
//
// Discarding is safe here because the manifest validator refuses an
// unparseable pattern before a run starts, and a caller outside the manifest
// gets exactly the behaviour of having asked for nothing.
func newMatcher(patterns []string) *matcher {
	m := &matcher{}
	for _, p := range patterns {
		if tokens, ok := parsePattern(p); ok {
			m.patterns = append(m.patterns, tokens)
		}
	}
	return m
}

// matches reports whether any pattern selects this path.
func (m *matcher) matches(path []segment) bool {
	if m == nil || len(m.patterns) == 0 {
		return false
	}
	for _, tokens := range m.patterns {
		if matchTokens(tokens, path) {
			return true
		}
	}
	return false
}

// matchTokens is the wildcard match, written recursively because "descend"
// makes it a two dimensional problem and the iterative form of that is where
// the bugs live.
func matchTokens(tokens []token, path []segment) bool {
	switch {
	case len(tokens) == 0:
		return len(path) == 0
	case tokens[0].descend:
		// Zero segments consumed, then one, then two. The first branch is what
		// makes "$..created_at" match "$.created_at" as well as
		// "$.a.b.created_at".
		for i := 0; i <= len(path); i++ {
			if matchTokens(tokens[1:], path[i:]) {
				return true
			}
		}
		return false
	case len(path) == 0:
		return false
	case tokens[0].wildcard:
		return matchTokens(tokens[1:], path[1:])
	case tokens[0].isIndex:
		if !path[0].isIndex || path[0].index != tokens[0].index {
			return false
		}
		return matchTokens(tokens[1:], path[1:])
	default:
		if path[0].isIndex || path[0].key != tokens[0].key {
			return false
		}
		return matchTokens(tokens[1:], path[1:])
	}
}
