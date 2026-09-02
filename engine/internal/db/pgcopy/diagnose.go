package pgcopy

// Reading a real production database is where a first run stops, and it
// stopped by printing whatever pg_dump, pg_restore or the driver said and
// nothing else. Those messages are accurate and none of them says what to do,
// which makes each one a place somebody gives up.
//
// The cases below were found by pointing a refresh at a Postgres carrying what
// a real schema carries. Every one of them is ordinary configuration rather
// than a broken database:
//
//   - a read only role, which is what the generated manifest's own comment
//     tells you to use, cannot read the sequences, so the dump stops on the
//     first one it reaches
//   - row level security is on, and Postgres refuses to dump a table it would
//     have to filter, because a filtered dump silently carries some of the rows
//   - an extension the source has and a plain Postgres image does not, which is
//     every schema using PostGIS, pgvector, TimescaleDB or pg_cron
//   - a typo in the connection string, which arrives as a driver dump naming
//     four failed dial attempts and no next step
//
// Classification is by the text, because the text is all a subprocess returns.
// It matches the invariant part of each message, and anything it does not
// recognise keeps its transcript and gets the one next step that always works:
// run the program yourself against the same connection string.

import (
	"errors"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

var (
	// ERROR:  extension "postgis" is not available
	reExtension = regexp.MustCompile(`extension "([^"]+)" is not available`)
	// ERROR:  permission denied for sequence orders_id_seq
	// ERROR:  permission denied for table customers
	// ERROR:  permission denied for schema Mixed Case Schema
	//
	// The object kind comes from a fixed vocabulary and the name that follows
	// it does not: Postgres prints the name unquoted, so a schema called
	// "Mixed Case Schema" ends the message with three words and nothing says
	// where the name began. Matching a known kind and taking the rest of the
	// line is the only reading of that which does not truncate the answer to
	// its first word, which is what naming the wrong schema in the remedy
	// would do.
	rePermission = regexp.MustCompile(`permission denied for ` +
		`(materialized view|foreign table|large object|foreign server|` +
		`table|sequence|schema|relation|view|function|procedure|database|` +
		`column|type|domain|tablespace|language|extension) ([^\n]+)`)
	// ERROR:  query would be affected by row-level security policy for table "customers"
	reRowSecurity = regexp.MustCompile(`row-level security policy for table "?([^"\s;]+)"?`)
)

// transcriptError turns a pg_dump or pg_restore transcript into a coded error.
//
// program is the one that stopped, so the fallback says which of the two it
// was. The transcript is carried as the cause on every path, so -v shows the
// whole of what Postgres said even where the code has replaced the headline.
func transcriptError(program, transcript string) error {
	cause := errors.New(Tail(transcript))

	if m := reExtension.FindStringSubmatch(transcript); m != nil {
		return aferrors.Wrap(cause, aferrors.AFDB007, "extension", m[1])
	}
	// Ordered before the permission check on purpose. A row level security
	// refusal is a permission problem in the ordinary sense and the remedy is
	// a different one, so the more specific message has to win.
	if m := reRowSecurity.FindStringSubmatch(transcript); m != nil {
		return aferrors.Wrap(cause, aferrors.AFDB018, "table", m[1])
	}
	if m := rePermission.FindStringSubmatch(transcript); m != nil {
		object := strings.TrimRight(strings.TrimSpace(m[2]), ".;")
		return aferrors.Wrap(cause, aferrors.AFDB017,
			"object", m[1]+" "+strings.Trim(object, `"`))
	}
	return aferrors.Wrap(cause, aferrors.AFDB019,
		"program", program, "detail", Tail(transcript))
}

// connectError turns a failure to reach the source into a coded error.
//
// The driver reports one line per address it tried, so an unreachable host
// arrives as four near identical lines about IPv6 and IPv4 and no statement of
// what is wrong. Which of the three it is decides the remedy: a host that
// answers and refuses a password is not a host that is not there, and neither
// is a string the driver would not parse.
func connectError(err error, what string) error {
	if err == nil {
		return nil
	}
	text := err.Error()

	// Parsing happens before any address is tried, so it is checked first.
	if strings.Contains(text, "cannot parse") ||
		strings.Contains(text, "invalid dsn") ||
		strings.Contains(text, "invalid keyword/value") {
		return aferrors.Wrap(err, aferrors.AFDB024, "detail", firstLine(text))
	}

	// The server answered and refused, which every driver reports through a
	// SQLSTATE rather than through prose. 28P01 is a bad password, 28000 is
	// the rest of the authentication failures, and 3D000 is a database name
	// that does not exist on a server that does.
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "28P01", "28000", "28P02":
			return aferrors.Wrap(err, aferrors.AFDB023, "detail", pgErr.Message)
		case "3D000":
			return aferrors.Wrap(err, aferrors.AFDB023, "detail", pgErr.Message)
		}
	}
	// pgx wraps the connect attempts rather than returning the PgError, so the
	// text is the only thing left to read on that path.
	if strings.Contains(text, "password authentication failed") ||
		strings.Contains(text, "failed SASL auth") ||
		strings.Contains(text, "no pg_hba.conf entry") ||
		strings.Contains(text, "database \"") && strings.Contains(text, "does not exist") {
		return aferrors.Wrap(err, aferrors.AFDB023, "detail", lastMeaningfulLine(text))
	}

	return aferrors.Wrap(err, aferrors.AFDB002, "host", what)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// lastMeaningfulLine is the last line of a multi address connection failure.
//
// The driver tries IPv6 and IPv4 for each address and reports every attempt,
// so the first line is usually about a TLS negotiation that was never the
// problem and the last is the one that reached a server and was refused.
func lastMeaningfulLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return line
		}
	}
	return strings.TrimSpace(s)
}
