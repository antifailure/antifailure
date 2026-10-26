package mysql

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"

	driver "github.com/go-sql-driver/mysql"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// How this package reaches a server.
//
// A store's address is a URL, mysql://user:password@host:port/database, because
// that is what every other address in a manifest is and what the runtime knows
// how to point at a container on an environment's network. The driver wants a
// DSN of its own shape, so the URL is parsed here and never handed to it as
// text: a password holding an @ or a / is percent encoded in a URL and would
// end the user part early in a DSN.

// defaultPort is MySQL's own.
const defaultPort = "3306"

// server is one MySQL address, with or without a database.
type server struct {
	user     string
	password string
	addr     string
	database string
	// tls is the driver's tls setting. See parseURL for how it is chosen.
	tls string
	// tlsStated reports that the URL said tls= itself, rather than the mode
	// being this package's default for the host.
	tlsStated bool
}

// parseURL reads a mysql:// address.
//
// tls is taken from the query and nothing else is. With no tls= the mode
// depends on the host. A host on another machine gets verified TLS, tls=true,
// and a server that cannot provide it is refused with AF-DB-046 naming the
// host. A source is production: the connection carries production's
// credential and its unmasked rows, and preferred, the driver's own default,
// neither checks the certificate nor refuses a plaintext connection, so anyone
// on the path would read both. It used to be the default here, and MySQL
// support was new enough that nobody relied on it.
//
// A loopback address or exactly localhost defaults to preferred: a local
// server usually has no certificate a verifier would accept, and nothing
// between it and this process is somebody else's network. See isLoopback for
// why no other name qualifies.
//
// An explicit tls=preferred, skip-verify or false is the author's stated
// choice and is honoured. af explain says which mode a source is using.
func parseURL(v secrets.Value) (*server, error) {
	raw := strings.TrimSpace(v.Reveal())
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "mysql" || u.Host == "" {
		// The value is a credential, so it is not quoted back.
		return nil, fmt.Errorf(
			"datastore.mysql: the address is not a mysql://user:password@host:port/database URL")
	}
	s := &server{}
	if u.User != nil {
		s.user = u.User.Username()
		s.password, _ = u.User.Password()
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		host, port = u.Host, defaultPort
	}
	s.addr = net.JoinHostPort(host, port)
	s.database = strings.TrimPrefix(u.Path, "/")
	if strings.Contains(s.database, "/") {
		return nil, fmt.Errorf(
			"datastore.mysql: the address names %q as its database, and a MySQL database name "+
				"has no slash in it", s.database)
	}
	switch mode := u.Query().Get("tls"); mode {
	case "":
		s.tls = defaultTLS(host)
	case "true", "false", "skip-verify", "preferred":
		s.tls, s.tlsStated = mode, true
	default:
		return nil, fmt.Errorf(
			"datastore.mysql: tls=%s is not one of true, false, skip-verify or preferred", mode)
	}
	return s, nil
}

// defaultTLS is the mode a URL with no tls= gets for its host: preferred on
// this machine, verified everywhere else.
func defaultTLS(host string) string {
	if isLoopback(host) {
		return "preferred"
	}
	return "true"
}

// isLoopback reports a host that is this machine by what it literally says:
// an address in 127.0.0.0/8 or ::1, or exactly localhost.
//
// Only those. Any other name is resolved, through DNS or a hosts file, and a
// source address naming af-mysql or something.localhost can be pointed at a
// server on another machine by whoever controls that resolution, which would
// hand production's credential to the weaker default. The managed server's
// own name gets preferred only on the connection the provider makes to it,
// in New, never on an address parsed as a source.
func isLoopback(host string) bool {
	h := strings.ToLower(host)
	if h == "localhost" {
		return true
	}
	if ip, err := netip.ParseAddr(strings.Trim(h, "[]")); err == nil {
		return ip.Unmap().IsLoopback()
	}
	return false
}

// TLSMode describes how a mysql:// address will be connected to, for af
// explain: the mode, whether the address chose it or this package did, and a
// sentence a person can act on. It never quotes the address. ok is false for
// a value that is not a mysql:// address at all.
func TLSMode(v secrets.Value) (mode string, stated bool, sentence string, ok bool) {
	s, err := parseURL(v)
	if err != nil {
		return "", false, "", false
	}
	host, _, _ := net.SplitHostPort(s.addr)
	why := "the default for " + host
	if s.tlsStated {
		why = "as the address says"
	}
	switch s.tls {
	case "true":
		sentence = "verified TLS, " + why + ": the certificate must be one this machine trusts " +
			"for " + host + ", or the refresh is refused"
	case "preferred":
		sentence = "tls=preferred, " + why + ": encrypted only if the server offers it and the " +
			"certificate is never checked, so anyone on the path can read the credential and " +
			"the unmasked rows"
	case "skip-verify":
		sentence = "tls=skip-verify, " + why + ": encrypted, but the certificate is never " +
			"checked, so anyone on the path can impersonate the server and read the credential " +
			"and the unmasked rows"
	case "false":
		sentence = "tls=false, " + why + ": not encrypted, so anyone on the path can read the " +
			"credential and the unmasked rows"
	}
	return s.tls, s.tlsStated, sentence, true
}

// refuseUnverified turns a failed verified connection into AF-DB-046, naming
// the host and the way out, and passes every other error through.
//
// The driver's own words for these are a certificate chain or "TLS requested
// but server does not support TLS", neither of which says that the mode was
// chosen on the reader's behalf or how to choose another.
func (s *server) refuseUnverified(err error) error {
	if err == nil || s.tls != "true" || !isTLSFailure(err) {
		return err
	}
	host, _, _ := net.SplitHostPort(s.addr)
	return aferrors.Coded(aferrors.AFDB046, "host", host, "detail", firstLine(err.Error()))
}

// isTLSFailure reports an error from negotiating or verifying TLS.
func isTLSFailure(err error) bool {
	if errors.Is(err, driver.ErrNoTLS) {
		return true
	}
	var unknown x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var verify *tls.CertificateVerificationError
	var record tls.RecordHeaderError
	if errors.As(err, &unknown) || errors.As(err, &hostname) || errors.As(err, &invalid) ||
		errors.As(err, &verify) || errors.As(err, &record) {
		return true
	}
	return strings.HasPrefix(err.Error(), "tls: ") || strings.Contains(err.Error(), "x509: ")
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// DatabaseOf returns the database a mysql:// address names, and empty when it
// names none or is not such an address. It is what masking calls the schema.
func DatabaseOf(v secrets.Value) string {
	s, err := parseURL(v)
	if err != nil {
		return ""
	}
	return s.database
}

// in returns the same server with a different database.
func (s *server) in(database string) *server {
	c := *s
	c.database = database
	return &c
}

// url renders the address back as a URL, for a caller that is handed a branch.
func (s *server) url() secrets.Value {
	u := url.URL{
		Scheme: "mysql",
		User:   url.UserPassword(s.user, s.password),
		Host:   s.addr,
		Path:   "/" + s.database,
	}
	// Written out unless it is what this host gets anyway, so the address
	// reads back as the same mode. Left off in the common case because the
	// branch address is handed to an application's own driver, and several
	// refuse a parameter they do not know.
	host, _, _ := net.SplitHostPort(s.addr)
	if s.tls != defaultTLS(host) {
		u.RawQuery = "tls=" + s.tls
	}
	return secrets.New(u.String())
}

// session is what a connection is opened for, and it decides the settings
// every connection in the pool is given.
type session int

const (
	// sessionPlain reads catalogs, scans and pings. Nothing about it differs
	// from the server's defaults except the time zone.
	sessionPlain session = iota
	// sessionCopy moves rows between servers and between databases, byte for
	// byte. See copySettings.
	sessionCopy
	// sessionMask rewrites rows in place. See maskSettings.
	sessionMask
	// sessionDDL creates tables from their definitions, in name order, so a
	// child may be created before its parent. utf8mb4, because a definition is
	// text, and no foreign key checks.
	sessionDDL
)

// open returns a pool for this server and session.
//
// Every connection is given the session's settings by the driver as it is
// made, rather than by a statement after it, because a pool hands out whichever
// connection is free and a setting applied to one connection is a setting the
// next statement may not see.
func (s *server) open(kind session) (*sql.DB, error) {
	cfg := driver.NewConfig()
	cfg.User = s.user
	cfg.Passwd = s.password
	cfg.Net = "tcp"
	cfg.Addr = s.addr
	cfg.DBName = s.database
	cfg.TLSConfig = s.tls
	cfg.Timeout = 10 * time.Second
	cfg.MultiStatements = false
	// The driver logs a dropped connection to stderr on its own, and a
	// server that is still starting drops every one. Those lines reached the
	// terminal of whoever ran af up, ahead of this package's own sentence
	// about the same wait; every failure the driver logs is also returned,
	// and is reported from there.
	cfg.Logger = &driver.NopLogger{}
	cfg.Params = map[string]string{
		// Every session reads and writes instants in UTC. A TIMESTAMP is
		// stored as an instant and rendered through the session's zone, so a
		// copy whose read and write sessions sat in different zones moved
		// every timestamp in the store by the difference.
		"time_zone": "'+00:00'",
	}
	switch kind {
	case sessionCopy:
		if err := copySettings(cfg); err != nil {
			return nil, fmt.Errorf("datastore.mysql: %w", err)
		}
	case sessionMask, sessionDDL:
		maskSettings(cfg)
	}
	connector, err := driver.NewConnector(cfg)
	if err != nil {
		return nil, fmt.Errorf("datastore.mysql: %w", err)
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(4)
	db.SetConnMaxIdleTime(time.Minute)
	return db, nil
}

// copySettings make a session that moves rows exactly as they are stored.
//
//   - The binary character set, so that every string column is read as the
//     bytes it holds and written back as those bytes into a column of the same
//     definition. A utf8mb4 session would convert a latin1 column on the way
//     out and back on the way in, which is lossless only for what latin1 can
//     spell, and would reject a binary string that is not valid UTF-8.
//   - A permissive sql_mode. A source made under an older or a looser mode can
//     hold a zero date, and the copy's job is to hold what production holds.
//     NO_AUTO_VALUE_ON_ZERO, because a row whose AUTO_INCREMENT key is 0 would
//     otherwise be given a new key on insert.
//   - No foreign key checks. Tables are created and filled in name order, and a
//     child arriving before its parent is a copy, not a violation.
func copySettings(cfg *driver.Config) error {
	cfg.Params["sql_mode"] = "'NO_ENGINE_SUBSTITUTION,NO_AUTO_VALUE_ON_ZERO'"
	cfg.Params["foreign_key_checks"] = "0"
	// SET NAMES binary, with no collation: binary has exactly one.
	return cfg.Apply(driver.Charset("binary", ""))
}

// maskSettings make a session that rewrites rows in place.
//
// The server's own sql_mode is kept, strict where it is strict, so a masked
// value too long for its column is an error rather than a truncation. Foreign
// key checks are off because a parent and its child are rewritten one row at a
// time by the same deterministic transform, and between those two statements
// the reference is briefly wrong in exactly the way the second one fixes.
func maskSettings(cfg *driver.Config) {
	cfg.Params["foreign_key_checks"] = "0"
}

// quoteIdent quotes an identifier with backticks, doubling any inside it.
func quoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// shortHash names a database this provider makes. Twelve hex characters, so a
// prefixed name stays far inside MySQL's sixty four.
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:6])
}

// ping reports whether a server answers.
func ping(ctx context.Context, db *sql.DB) error {
	var one int
	return db.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}
