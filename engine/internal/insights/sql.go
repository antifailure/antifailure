package insights

import "strings"

// Statement is one SQL statement from a migration file.
type Statement struct {
	// Migration is the file it came from.
	Migration string `json:"migration"`
	// Index is its position within that file, from one.
	Index int `json:"index"`
	// SQL is the statement itself, without its terminating semicolon.
	SQL string `json:"sql"`
}

// Split cuts a migration file into statements.
//
// It is a scanner rather than a parser because it only has to find statement
// boundaries, and the things that hide a semicolon from a boundary scan are a
// short list: a single quoted literal, a double quoted identifier, a dollar
// quoted body, a line comment, a block comment. Each of those is handled here.
// A parser would be a much larger thing that answered the same question.
//
// The case this exists for is a migration containing a function body, which
// is full of semicolons and is one statement. Splitting on every semicolon
// turns one CREATE FUNCTION into a dozen syntax errors, and a rehearsal that
// fails on a migration production applies cleanly is worse than no rehearsal.
func Split(migration, body string) []Statement {
	var out []Statement
	var cur strings.Builder
	emit := func() {
		text := strings.TrimSpace(cur.String())
		cur.Reset()
		if text == "" {
			return
		}
		out = append(out, Statement{Migration: migration, Index: len(out) + 1, SQL: text})
	}

	for i := 0; i < len(body); {
		c := body[i]
		switch {
		case c == '-' && i+1 < len(body) && body[i+1] == '-':
			j := strings.IndexByte(body[i:], '\n')
			if j < 0 {
				i = len(body)
			} else {
				i += j // leave the newline, so statements stay readable
			}
		case c == '/' && i+1 < len(body) && body[i+1] == '*':
			j := strings.Index(body[i+2:], "*/")
			if j < 0 {
				i = len(body)
			} else {
				i += 2 + j + 2
			}
		case c == '\'' || c == '"':
			j := scanQuoted(body, i, c)
			cur.WriteString(body[i:j])
			i = j
		case c == '$':
			if tag, end := dollarTag(body, i); end > i {
				j := strings.Index(body[end:], tag)
				if j < 0 {
					cur.WriteString(body[i:])
					i = len(body)
				} else {
					cur.WriteString(body[i : end+j+len(tag)])
					i = end + j + len(tag)
				}
			} else {
				cur.WriteByte(c)
				i++
			}
		case c == ';':
			emit()
			i++
		default:
			cur.WriteByte(c)
			i++
		}
	}
	emit()
	return out
}

// scanQuoted returns the index just past a quoted run starting at i, treating
// a doubled quote as an escaped one, which is how Postgres escapes both kinds.
func scanQuoted(s string, i int, quote byte) int {
	i++
	for i < len(s) {
		if s[i] == quote {
			if i+1 < len(s) && s[i+1] == quote {
				i += 2
				continue
			}
			return i + 1
		}
		i++
	}
	return len(s)
}

// dollarTag recognises a dollar quote opening at i and returns the tag and the
// index just past it. $$ and $body$ are both tags; $1 is a parameter and is
// not.
func dollarTag(s string, i int) (string, int) {
	j := i + 1
	for j < len(s) && (isIdentByte(s[j])) {
		j++
	}
	if j < len(s) && s[j] == '$' {
		return s[i : j+1], j + 1
	}
	return "", i
}

func isIdentByte(c byte) bool {
	return c == '_' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9')
}

// fold makes a statement comparable: one space between words, upper case, and
// no trailing semicolon. Only used for matching keywords, never for display.
func fold(sql string) string {
	return strings.ToUpper(strings.Join(strings.Fields(sql), " "))
}

// unquote strips one layer of double quotes from an identifier and lower cases
// it when there were none, which is what Postgres itself does.
func unquote(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimSuffix(name, ",")
	name = strings.TrimSuffix(name, "(")
	if len(name) >= 2 && name[0] == '"' && name[len(name)-1] == '"' {
		return strings.ReplaceAll(name[1:len(name)-1], `""`, `"`)
	}
	return strings.ToLower(name)
}

// bareTable drops a schema qualifier, because pg_stat_user_tables reports
// relname without one and the two have to compare.
func bareTable(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}
