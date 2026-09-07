package clickhouse

import (
	"context"
	"fmt"
	"strings"
)

// The copy, source to candidate, which is the only part of a refresh that
// touches production.
//
// Two statements per table and no decoding in Go. The schema comes from the
// source's own CREATE TABLE, retargeted at the candidate database, so a
// partitioning expression, a TTL, a codec and an index granularity survive
// rather than being reconstructed from a catalog read. The rows come from
// SELECT ... FORMAT Native streamed straight into INSERT ... FORMAT Native, so
// a DateTime64 keeps its precision, a Decimal keeps its scale and a Map keeps
// its shape, none of which a value rendered as text and parsed back reliably
// does.

// copyTable creates one table in the candidate and streams its rows in.
func copyTable(ctx context.Context, src, dst *client, t table) error {
	ddl, err := src.value(ctx,
		"SELECT create_table_query FROM system.tables WHERE database = currentDatabase() "+
			"AND name = {name:String}", map[string]string{"name": t.name})
	if err != nil {
		return err
	}
	retargeted, err := retarget(ddl, src.database, dst.database, t.name)
	if err != nil {
		return err
	}
	if err := dst.exec(ctx, retargeted, nil); err != nil {
		return fmt.Errorf("creating %s in the golden candidate: %w", t.name, err)
	}

	cols := t.writableColumns()
	names := make([]string, 0, len(cols))
	for _, c := range cols {
		names = append(names, quoteIdent(c.name))
	}
	list := strings.Join(names, ", ")

	read := fmt.Sprintf("SELECT %s FROM %s FORMAT Native", list, quoteIdent(t.name))
	write := fmt.Sprintf("INSERT INTO %s (%s) FORMAT Native", quoteIdent(t.name), list)
	if err := stream(ctx, src, read, dst, write); err != nil {
		return fmt.Errorf("copying %s into the golden candidate: %w", t.name, err)
	}
	return nil
}

// stream reads one statement's output and writes it as another's input.
//
// The response body of the read is the request body of the write, so a table
// larger than this machine's memory is copied in constant memory and no value
// is ever decoded. The write's statement goes in the query string because the
// body is the data.
func stream(ctx context.Context, src *client, read string, dst *client, write string) error {
	body, err := src.post(ctx, read, nil, nil)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	return dst.insert(ctx, write, body)
}

// retarget rewrites a CREATE TABLE statement to name another database, and
// rewrites a Replicated engine into its single node equivalent.
//
// The name is replaced by span rather than by search and replace, because a
// table called events appears in the column list of a table that has a column
// called events, and a replacement that hit the wrong one would produce a
// statement that runs and builds the wrong table. Everything from the start of
// the statement to the opening bracket of the column list is the name, so that
// is what is replaced.
func retarget(ddl, fromDB, toDB, name string) (string, error) {
	const prefix = "CREATE TABLE "
	trimmed := strings.TrimSpace(ddl)
	if !strings.HasPrefix(trimmed, prefix) {
		return "", fmt.Errorf(
			"clickhouse: the source's own CREATE statement for %s.%s does not begin with "+
				"%q, so this cannot tell where the name ends; it begins %q",
			fromDB, name, strings.TrimSpace(prefix), firstLine(trimmed))
	}
	open := strings.IndexByte(trimmed, '(')
	if open < 0 {
		return "", fmt.Errorf(
			"clickhouse: the source's own CREATE statement for %s.%s has no column list, "+
				"so there is nothing to copy", fromDB, name)
	}
	head := trimmed[len(prefix):open]
	if !strings.Contains(head, name) {
		// The span being replaced has to be the name, and this is the check
		// that it is. A statement whose head does not carry the table's own
		// name is one this has misread, and building a table from a misread
		// statement is worse than refusing: the refusal is visible.
		return "", fmt.Errorf(
			"clickhouse: the source's own CREATE statement for %s.%s names %q before its "+
				"column list, and this expected the table's own name there",
			fromDB, name, strings.TrimSpace(head))
	}
	rest := singleNode(trimmed[open:])
	return prefix + quoteIdent(toDB) + "." + quoteIdent(name) + " " + rest, nil
}

// singleNode rewrites a Replicated table engine into the plain one.
//
// A golden is one database on one server. A ReplicatedMergeTree names a
// coordination path and a replica name, both of which belong to the cluster it
// came from: recreated verbatim, it either collides with the production path
// or waits for a keeper that is not there. Every other argument is kept,
// because the ones after the first two are the engine's own, such as the
// version column of a ReplacingMergeTree, and dropping one would change what
// the table means.
func singleNode(rest string) string {
	i := strings.Index(rest, "ENGINE = Replicated")
	if i < 0 {
		return rest
	}
	head := rest[:i]
	tail := rest[i+len("ENGINE = Replicated"):]
	// The engine name runs to the bracket or to the first space.
	end := strings.IndexAny(tail, "( \n")
	if end < 0 {
		return head + "ENGINE = " + tail
	}
	engine := tail[:end]
	after := tail[end:]
	if !strings.HasPrefix(strings.TrimSpace(after), "(") {
		return head + "ENGINE = " + engine + after
	}
	args, remainder, ok := bracketed(after)
	if !ok {
		return head + "ENGINE = " + engine + after
	}
	kept := dropCoordinationArgs(args)
	if kept == "" {
		return head + "ENGINE = " + engine + "()" + remainder
	}
	return head + "ENGINE = " + engine + "(" + kept + ")" + remainder
}

// bracketed splits a string beginning with a bracket into its contents and
// what follows it.
func bracketed(s string) (inner, rest string, ok bool) {
	start := strings.IndexByte(s, '(')
	if start < 0 {
		return "", s, false
	}
	depth := 0
	inString := false
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '\'':
			inString = !inString
		case '(':
			if !inString {
				depth++
			}
		case ')':
			if inString {
				continue
			}
			depth--
			if depth == 0 {
				return s[start+1 : i], s[i+1:], true
			}
		}
	}
	return "", s, false
}

// dropCoordinationArgs removes the keeper path and the replica name.
//
// They are the first two arguments and they are string literals. A Replicated
// engine can also be declared with none, which is the default path form, and
// then there is nothing to drop.
func dropCoordinationArgs(args string) string {
	parts := splitTopLevel(args)
	drop := 0
	for drop < 2 && drop < len(parts) {
		if !strings.HasPrefix(strings.TrimSpace(parts[drop]), "'") {
			break
		}
		drop++
	}
	kept := make([]string, 0, len(parts))
	for _, p := range parts[drop:] {
		if strings.TrimSpace(p) == "" {
			continue
		}
		kept = append(kept, strings.TrimSpace(p))
	}
	return strings.Join(kept, ", ")
}
