package iac

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// WHY THIS PACKAGE TOKENIZES HCL ITSELF rather than taking hashicorp/hcl.
//
// Three reasons, and the third is the one that decided it.
//
// First, hcl/v2 is under the Mozilla Public License and the af binary carries
// no MPL licensed dependency today. tools/notices/licence.go recognises seven
// licence texts exactly and FAILS NAMING THE MODULE on an eighth, so taking it
// is not a go.mod line, it is a change to what the shipped binary is licensed
// under.
//
// Second, go.work says in its own words that the small auditable dependency
// set is a design property of the shipped binary. hcl/v2 brings go-cty,
// levenshtein, textseg and wordwrap with it, to read a file format whose
// grammar fits in this file.
//
// Third, and this is the argument that is actually about quality rather than
// about cost: this repository already answered the same question two floors
// down. tools/notices/licence.go rejected google/licensecheck for a hand
// written matcher, on the stated ground that "a matcher that knows seven texts
// exactly and refuses the eighth is the smaller and the more honest
// instrument". A reader whose whole contract is "resolve what you can, and
// refuse the rest BY NAME with a line number" is that same instrument. A
// general parser would tempt this package into answering questions it cannot
// answer, because it would have an AST for them.
//
// THE RISK THIS SHAPE CARRIES, named so it can be contained. A hand written
// tokenizer's failure mode is not refusing, it is MIS-READING: a mishandled
// heredoc that swallows the next resource block, and then every attribute of
// that resource is silently attributed to the wrong thing, and the reader is
// confidently wrong rather than honestly incomplete. Silent wrongness is worse
// than any hole.
//
// So this file FAILS CLOSED, everywhere, without exception. Any byte sequence
// the lexer does not fully understand ends the read of the WHOLE FILE with a
// reason and a position, and that file becomes a Source with Read false. There
// is no recovery, no resynchronisation, and no partial body. A file this
// tokenizer is unsure about contributes NOTHING rather than contributing
// something that might be wrong.

type tokenKind uint8

const (
	tokEOF tokenKind = iota
	tokNewline
	tokIdent
	tokNumber
	tokString  // a quoted string, its pieces in parts
	tokHeredoc // a heredoc, its pieces in parts
	tokPunct
)

func (k tokenKind) String() string {
	switch k {
	case tokEOF:
		return "end of file"
	case tokNewline:
		return "a line break"
	case tokIdent:
		return "a name"
	case tokNumber:
		return "a number"
	case tokString:
		return "a string"
	case tokHeredoc:
		return "a heredoc"
	default:
		return "punctuation"
	}
}

// token is one lexical item.
type token struct {
	kind  tokenKind
	text  string
	parts []strPart
	pos   Position
}

// strPart is one piece of a string or heredoc: either literal text, or an
// interpolation's tokens, or a template directive this reader does not
// evaluate.
type strPart struct {
	lit string
	// interp holds the tokens between `${` and its matching `}`. Non nil for
	// an interpolation, including an empty interpolation.
	interp []token
	// directive marks a `%{ ... }` control directive. A string containing one
	// is never resolved, because evaluating it would mean implementing the
	// template language's `if` and `for`.
	directive bool
	pos       Position
}

// lexError is a refusal to read a file, carrying the reason a Source will show
// and the position it happened at.
type lexError struct {
	why string
	pos Position
}

func (e *lexError) Error() string {
	if at := e.pos.String(); at != "" {
		return at + ": " + e.why
	}
	return e.why
}

func lexErrf(pos Position, format string, args ...any) *lexError {
	return &lexError{why: fmt.Sprintf(format, args...), pos: pos}
}

type lexer struct {
	src  string
	file string
	i    int
	line int
	col  int
}

func newLexer(file string, src []byte) *lexer {
	return &lexer{src: string(src), file: file, line: 1, col: 1}
}

func (l *lexer) pos() Position { return Position{File: l.file, Line: l.line, Col: l.col} }

func (l *lexer) eof() bool { return l.i >= len(l.src) }

func (l *lexer) peek() byte {
	if l.eof() {
		return 0
	}
	return l.src[l.i]
}

func (l *lexer) peekAt(n int) byte {
	if l.i+n >= len(l.src) {
		return 0
	}
	return l.src[l.i+n]
}

// advance consumes n bytes, keeping line and column right. Column counts
// runes rather than bytes, so a position in a file with a non ASCII comment
// still points where an editor puts the cursor.
func (l *lexer) advance(n int) {
	for k := 0; k < n && l.i < len(l.src); {
		if l.src[l.i] == '\n' {
			l.line++
			l.col = 1
			l.i++
			k++
			continue
		}
		_, size := utf8.DecodeRuneInString(l.src[l.i:])
		if size == 0 {
			size = 1
		}
		l.i += size
		l.col++
		k += size
	}
}

// lex turns a whole file into tokens, or refuses the file.
func lex(file string, src []byte) ([]token, *lexError) {
	l := newLexer(file, src)
	toks, err := l.tokens(0)
	if err != nil {
		return nil, err
	}
	toks = append(toks, token{kind: tokEOF, pos: l.pos()})
	return toks, nil
}

// tokens reads tokens until end of input, or until an unmatched closing brace
// when depth is above zero, which is how an interpolation body ends.
func (l *lexer) tokens(depth int) ([]token, *lexError) {
	var out []token
	for {
		if err := l.skipSpaceAndComments(); err != nil {
			return nil, err
		}
		if l.eof() {
			if depth > 0 {
				return nil, lexErrf(l.pos(), "an interpolation was opened with ${ and never closed")
			}
			return out, nil
		}
		start := l.pos()
		c := l.peek()
		switch {
		case c == '\n' || c == '\r':
			if c == '\r' {
				if l.peekAt(1) != '\n' {
					return nil, lexErrf(start, "a carriage return that is not part of a line break")
				}
				l.advance(1)
			}
			l.advance(1)
			out = append(out, token{kind: tokNewline, text: "\n", pos: start})
		case c == '}' && depth > 0:
			// The caller consumes it: this is the end of an interpolation.
			return out, nil
		case c == '"':
			tok, err := l.lexQuoted()
			if err != nil {
				return nil, err
			}
			out = append(out, tok)
		case c == '<' && l.peekAt(1) == '<':
			tok, err := l.lexHeredoc()
			if err != nil {
				return nil, err
			}
			out = append(out, tok)
		case c >= '0' && c <= '9':
			out = append(out, l.lexNumber())
		case isIdentStart(c):
			out = append(out, l.lexIdent())
		default:
			tok, err := l.lexPunct()
			if err != nil {
				return nil, err
			}
			out = append(out, tok)
		}
	}
}

func (l *lexer) skipSpaceAndComments() *lexError {
	for !l.eof() {
		switch c := l.peek(); {
		case c == ' ' || c == '\t':
			l.advance(1)
		case c == '#' || (c == '/' && l.peekAt(1) == '/'):
			for !l.eof() && l.peek() != '\n' {
				l.advance(1)
			}
		case c == '/' && l.peekAt(1) == '*':
			start := l.pos()
			l.advance(2)
			for {
				if l.eof() {
					return lexErrf(start, "a /* comment was opened and never closed")
				}
				if l.peek() == '*' && l.peekAt(1) == '/' {
					l.advance(2)
					break
				}
				l.advance(1)
			}
		default:
			return nil
		}
	}
	return nil
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= utf8.RuneSelf
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9') || c == '-'
}

func (l *lexer) lexIdent() token {
	start := l.pos()
	from := l.i
	for !l.eof() && isIdentPart(l.peek()) {
		l.advance(1)
	}
	return token{kind: tokIdent, text: l.src[from:l.i], pos: start}
}

func (l *lexer) lexNumber() token {
	start := l.pos()
	from := l.i
	for !l.eof() {
		c := l.peek()
		if c >= '0' && c <= '9' {
			l.advance(1)
			continue
		}
		if c == '.' && l.peekAt(1) >= '0' && l.peekAt(1) <= '9' {
			l.advance(1)
			continue
		}
		if (c == 'e' || c == 'E') && l.i > from {
			next := l.peekAt(1)
			if next == '+' || next == '-' {
				if d := l.peekAt(2); d >= '0' && d <= '9' {
					l.advance(2)
					continue
				}
			} else if next >= '0' && next <= '9' {
				l.advance(1)
				continue
			}
		}
		break
	}
	return token{kind: tokNumber, text: l.src[from:l.i], pos: start}
}

// punctuation lists the multi byte operators first, so that `==` is never read
// as two `=` and an attribute assignment is never confused with a comparison.
var punctuation = []string{
	"==", "!=", "<=", ">=", "&&", "||", "=>", "...",
	"{", "}", "[", "]", "(", ")", ",", ".", "=", ":", "?",
	"+", "-", "*", "/", "%", "!", "<", ">",
}

func (l *lexer) lexPunct() (token, *lexError) {
	start := l.pos()
	rest := l.src[l.i:]
	for _, p := range punctuation {
		if strings.HasPrefix(rest, p) {
			l.advance(len(p))
			return token{kind: tokPunct, text: p, pos: start}, nil
		}
	}
	r, _ := utf8.DecodeRuneInString(rest)
	return token{}, lexErrf(start, "the character %q is not part of this language as this reader reads it", r)
}

// lexQuoted reads a quoted string, including its interpolations.
func (l *lexer) lexQuoted() (token, *lexError) {
	start := l.pos()
	l.advance(1) // the opening quote
	var (
		parts []strPart
		lit   strings.Builder
		at    = l.pos()
	)
	flush := func() {
		if lit.Len() > 0 {
			parts = append(parts, strPart{lit: lit.String(), pos: at})
			lit.Reset()
		}
	}
	for {
		if l.eof() {
			return token{}, lexErrf(start, "a quoted string was opened and never closed")
		}
		switch c := l.peek(); {
		case c == '"':
			l.advance(1)
			flush()
			return token{kind: tokString, parts: parts, pos: start}, nil
		case c == '\n':
			return token{}, lexErrf(l.pos(), "a line break inside a quoted string")
		case c == '\\':
			s, err := l.lexEscape()
			if err != nil {
				return token{}, err
			}
			lit.WriteString(s)
		case c == '$' && l.peekAt(1) == '{':
			flush()
			at = l.pos()
			part, err := l.lexInterp()
			if err != nil {
				return token{}, err
			}
			parts = append(parts, part)
			at = l.pos()
		case c == '%' && l.peekAt(1) == '{':
			flush()
			at = l.pos()
			part, err := l.lexDirective()
			if err != nil {
				return token{}, err
			}
			parts = append(parts, part)
			at = l.pos()
		default:
			from := l.i
			l.advance(1)
			lit.WriteString(l.src[from:l.i])
		}
	}
}

// lexEscape reads one backslash escape and returns the text it stands for.
//
// It refuses an escape it does not know rather than passing the character
// through, because a reader that quietly turned an unknown escape into its own
// letter would change the value it reports.
func (l *lexer) lexEscape() (string, *lexError) {
	start := l.pos()
	l.advance(1)
	if l.eof() {
		return "", lexErrf(start, "a string ended on a backslash")
	}
	c := l.peek()
	l.advance(1)
	switch c {
	case 'n':
		return "\n", nil
	case 'r':
		return "\r", nil
	case 't':
		return "\t", nil
	case '"':
		return "\"", nil
	case '\\':
		return "\\", nil
	case '\'':
		return "'", nil
	case '$', '%':
		// `\${` and `\%{` escape the sequence that would open a template.
		if !l.eof() && l.peek() == '{' {
			l.advance(1)
			return string(c) + "{", nil
		}
		return string(c), nil
	case 'u', 'U':
		n := 4
		if c == 'U' {
			n = 8
		}
		if l.i+n > len(l.src) {
			return "", lexErrf(start, "a unicode escape ran off the end of the file")
		}
		digits := l.src[l.i : l.i+n]
		var r rune
		for _, d := range []byte(digits) {
			v := hexValue(d)
			if v < 0 {
				return "", lexErrf(start, "a unicode escape with a non hexadecimal digit in it")
			}
			r = r<<4 | rune(v)
		}
		l.advance(n)
		if !utf8.ValidRune(r) {
			return "", lexErrf(start, "a unicode escape naming a code point that is not a character")
		}
		return string(r), nil
	default:
		return "", lexErrf(start, "the escape \\%c is not one this reader knows", c)
	}
}

func hexValue(d byte) int {
	switch {
	case d >= '0' && d <= '9':
		return int(d - '0')
	case d >= 'a' && d <= 'f':
		return int(d-'a') + 10
	case d >= 'A' && d <= 'F':
		return int(d-'A') + 10
	}
	return -1
}

// lexInterp reads a `${ ... }` interpolation by lexing its body with the same
// lexer, which is what makes a string inside an interpolation inside a string
// come out right.
func (l *lexer) lexInterp() (strPart, *lexError) {
	start := l.pos()
	l.advance(2)
	body, err := l.interpBody(start)
	if err != nil {
		return strPart{}, err
	}
	return strPart{interp: body, pos: start}, nil
}

// lexDirective reads a `%{ ... }` control directive and records only that
// there was one. The body is skipped with the same brace matching, so that a
// directive containing a string containing a brace still ends in the right
// place.
func (l *lexer) lexDirective() (strPart, *lexError) {
	start := l.pos()
	l.advance(2)
	if _, err := l.interpBody(start); err != nil {
		return strPart{}, err
	}
	return strPart{directive: true, pos: start}, nil
}

// interpBody lexes up to the brace that closes an interpolation, tracking the
// braces an object constructor opens inside it.
func (l *lexer) interpBody(start Position) ([]token, *lexError) {
	var out []token
	depth := 0
	for {
		toks, err := l.tokens(depth + 1)
		if err != nil {
			return nil, err
		}
		out = append(out, toks...)
		for _, t := range toks {
			if t.kind == tokPunct {
				switch t.text {
				case "{":
					depth++
				case "}":
					depth--
				}
			}
		}
		if l.eof() {
			return nil, lexErrf(start, "an interpolation was opened with ${ and never closed")
		}
		// tokens stopped on a closing brace it would not consume.
		if depth > 0 {
			brace := l.pos()
			l.advance(1)
			out = append(out, token{kind: tokPunct, text: "}", pos: brace})
			depth--
			continue
		}
		l.advance(1) // the brace that closes the interpolation
		return out, nil
	}
}

// lexHeredoc reads `<<EOT` and `<<-EOT` bodies.
//
// The indented form strips the SMALLEST indentation of any line in the body,
// which is Terraform's rule; stripping the first line's indentation instead is
// the mistake that silently changes a value.
func (l *lexer) lexHeredoc() (token, *lexError) {
	start := l.pos()
	l.advance(2)
	indented := false
	if !l.eof() && l.peek() == '-' {
		indented = true
		l.advance(1)
	}
	if l.eof() || !isIdentStart(l.peek()) {
		return token{}, lexErrf(start, "a heredoc opened with << and no delimiter after it")
	}
	delim := l.lexIdent().text
	// Only spaces may follow the delimiter on the opening line.
	for !l.eof() && (l.peek() == ' ' || l.peek() == '\t') {
		l.advance(1)
	}
	// Written as "not a line break" rather than with a negated conjunction,
	// because the two readings of the same condition are easy to get backwards
	// and only one of them accepts a CRLF delimiter line.
	atLineBreak := l.peek() == '\n' || (l.peek() == '\r' && l.peekAt(1) == '\n')
	if l.eof() || !atLineBreak {
		return token{}, lexErrf(start, "a heredoc delimiter with something after it on the same line")
	}
	if l.peek() == '\r' {
		l.advance(1)
	}
	l.advance(1)

	var lines []string
	var linePos []Position
	for {
		if l.eof() {
			return token{}, lexErrf(start, "a heredoc opened with <<%s and never closed by a line saying %s", delim, delim)
		}
		lineStart := l.pos()
		from := l.i
		for !l.eof() && l.peek() != '\n' {
			l.advance(1)
		}
		line := strings.TrimSuffix(l.src[from:l.i], "\r")
		if !l.eof() {
			l.advance(1)
		}
		if strings.TrimSpace(line) == delim {
			break
		}
		lines = append(lines, line)
		linePos = append(linePos, lineStart)
	}
	if indented {
		lines = stripCommonIndent(lines)
	}
	body := ""
	if len(lines) > 0 {
		body = strings.Join(lines, "\n") + "\n"
	}
	parts, err := lexTemplateBody(l.file, body, firstOr(linePos, start))
	if err != nil {
		return token{}, err
	}
	return token{kind: tokHeredoc, parts: parts, pos: start}, nil
}

func firstOr(ps []Position, fallback Position) Position {
	if len(ps) > 0 {
		return ps[0]
	}
	return fallback
}

// stripCommonIndent removes the smallest leading run of whitespace shared by
// every non blank line, which is what `<<-` means.
func stripCommonIndent(lines []string) []string {
	least := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		n := len(line) - len(strings.TrimLeft(line, " \t"))
		if least < 0 || n < least {
			least = n
		}
	}
	if least <= 0 {
		return lines
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		if len(line) >= least {
			out[i] = line[least:]
		} else {
			out[i] = strings.TrimLeft(line, " \t")
		}
	}
	return out
}

// lexTemplateBody splits a heredoc body into literal and interpolated parts,
// using the same lexer on a synthetic source so that the brace matching inside
// an interpolation is the same code in both places.
func lexTemplateBody(file, body string, at Position) ([]strPart, *lexError) {
	sub := &lexer{src: body, file: file, line: at.Line, col: 1}
	var (
		parts []strPart
		lit   strings.Builder
		from  = at
	)
	flush := func() {
		if lit.Len() > 0 {
			parts = append(parts, strPart{lit: lit.String(), pos: from})
			lit.Reset()
		}
	}
	for !sub.eof() {
		switch {
		case sub.peek() == '$' && sub.peekAt(1) == '{':
			flush()
			from = sub.pos()
			part, err := sub.lexInterp()
			if err != nil {
				return nil, err
			}
			parts = append(parts, part)
			from = sub.pos()
		case sub.peek() == '%' && sub.peekAt(1) == '{':
			flush()
			from = sub.pos()
			part, err := sub.lexDirective()
			if err != nil {
				return nil, err
			}
			parts = append(parts, part)
			from = sub.pos()
		default:
			start := sub.i
			sub.advance(1)
			lit.WriteString(body[start:sub.i])
		}
	}
	flush()
	return parts, nil
}
