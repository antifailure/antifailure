// Package fakeazurepg is an Azure Resource Manager control plane for flexible
// servers, backed by a real Postgres.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// It IS a control plane: it speaks the Resource Manager shapes for the calls
// this provider makes, holds servers and asynchronous operations, and models
// the parts of Azure's behaviour the provider depends on. Behind each server is
// a REAL Postgres database, so the conformance behaviours that are claims about
// BYTES are checked against bytes rather than against a fake's opinion of them.
//
// It is NOT evidence that Azure accepts these requests. Nothing here has a
// subscription and nothing here should: section 10 of the plan says no test may
// need one.
//
// THE THREE AZURE BEHAVIOURS THIS FAKE MODELS ON PURPOSE, because each is a
// place a provider is wrong quietly and a permissive fake would hide it:
//
//   - A RESTORED SERVER INHERITS THE SOURCE'S ADMINISTRATOR LOGIN AND PASSWORD.
//     Modelled so that the provider's password reset can be seen happening. A
//     fake that started every restore with a blank password would let that
//     reset be deleted with every test still green, and the defect it prevents
//     is a preview environment reachable with production's credential.
//   - FIREWALL RULES ARE NOT COPIED ACROSS A RESTORE. Microsoft lists applying
//     them as a post restore task. This fake starts a restored server with NO
//     rules and refuses a connection attempt that no rule admits, so a provider
//     that forgot to create one fails here rather than in somebody's
//     subscription.
//   - DELETING A SERVER DELETES ITS BACKUPS. Modelled by dropping the database
//     and forgetting the server entirely, so a test cannot restore from
//     something the provider deleted.
package fakeazurepg

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the pgx driver
)

// Options is what New needs.
type Options struct {
	// AdminURL is a Postgres this fake may create and drop databases on.
	AdminURL string
	// Prefix namespaces every database this fake creates. Required, because
	// the test Postgres is shared between suites and between branches.
	Prefix string
	// Subscription and ResourceGroup are the only pair this fake answers for.
	Subscription  string
	ResourceGroup string
	// SourceServer exists before anything is created, standing in for the
	// customer's production database.
	SourceServer string
	// Location is the region every server here reports.
	Location string
	// SeedSQL is run against the source server's database at startup.
	SeedSQL string
	// Now is the clock.
	Now func() time.Time
}

// Server is the fake control plane.
type Server struct {
	mu      sync.Mutex
	servers map[string]*fakeServer
	ops     map[string]string
	admin   *sql.DB
	opts    Options
	http    *httptest.Server
	seq     int

	bytesCopied int64
	restores    int
}

type fakeServer struct {
	Name          string
	Database      string
	Tags          map[string]string
	AdminLogin    string
	AdminPassword string
	StorageGB     int64
	FirewallRules map[string][2]string
	Subnet        string
}

// New starts the fake.
func New(opts Options) (*Server, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Prefix == "" {
		return nil, fmt.Errorf("fakeazurepg: Prefix is required; the test Postgres is shared")
	}
	if opts.Subscription == "" {
		opts.Subscription = "00000000-0000-0000-0000-000000000000"
	}
	if opts.ResourceGroup == "" {
		opts.ResourceGroup = "af-test"
	}
	if opts.SourceServer == "" {
		opts.SourceServer = "source"
	}
	if opts.Location == "" {
		opts.Location = "centralus"
	}
	admin, err := sql.Open("pgx", opts.AdminURL)
	if err != nil {
		return nil, fmt.Errorf("fakeazurepg: opening admin connection: %w", err)
	}
	// One connection: everything here is a CREATE DATABASE or DROP DATABASE,
	// neither of which may run inside a transaction, and a pool would hand a
	// later statement a different session than the one that created the thing
	// it names.
	admin.SetMaxOpenConns(1)

	s := &Server{
		servers: map[string]*fakeServer{},
		ops:     map[string]string{},
		admin:   admin,
		opts:    opts,
	}
	source := &fakeServer{
		Name:          opts.SourceServer,
		Database:      s.databaseFor(opts.SourceServer),
		Tags:          map[string]string{},
		AdminLogin:    "afadmin",
		AdminPassword: "production-password",
		StorageGB:     32,
		// The source is reachable: a customer's production server has rules.
		FirewallRules: map[string][2]string{"existing": {"0.0.0.0", "255.255.255.255"}},
	}
	if _, err := admin.Exec(`CREATE DATABASE ` + quoteIdent(source.Database)); err != nil {
		return nil, fmt.Errorf("fakeazurepg: creating %s: %w", source.Database, err)
	}
	if opts.SeedSQL != "" {
		if err := s.execOn(source.Database, opts.SeedSQL); err != nil {
			return nil, err
		}
	}
	s.servers[source.Name] = source

	s.http = httptest.NewServer(http.HandlerFunc(s.serve))
	return s, nil
}

// URL is the endpoint a provider points at.
func (s *Server) URL() string { return s.http.URL }

// Close stops the fake and drops every database it made, REPORTING what it
// could not clean up rather than swallowing it.
func (s *Server) Close() []error {
	s.http.Close()
	s.mu.Lock()
	names := make([]string, 0, len(s.servers))
	for _, srv := range s.servers {
		names = append(names, srv.Database)
	}
	s.servers = map[string]*fakeServer{}
	s.mu.Unlock()
	var problems []error
	for _, database := range names {
		if _, err := s.admin.Exec(
			`DROP DATABASE IF EXISTS ` + quoteIdent(database) + ` WITH (FORCE)`); err != nil {
			problems = append(problems, fmt.Errorf("dropping %s: %w", database, err))
		}
	}
	if err := s.admin.Close(); err != nil {
		problems = append(problems, err)
	}
	return problems
}

// BytesCopied is what this fake copied, which a snapshot restore would not.
func (s *Server) BytesCopied() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bytesCopied
}

// Restores is how many restores were served.
func (s *Server) Restores() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.restores
}

// Reset zeroes the counters without touching the servers.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bytesCopied = 0
	s.restores = 0
}

// PasswordOf is the administrator password a server currently has, for the
// test that checks the provider reset the inherited one.
func (s *Server) PasswordOf(name string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	srv, ok := s.servers[name]
	if !ok {
		return "", false
	}
	return srv.AdminPassword, true
}

// FirewallRulesOf is how many rules a server has, for the test that checks a
// branch was actually opened.
func (s *Server) FirewallRulesOf(name string) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	srv, ok := s.servers[name]
	if !ok {
		return 0, false
	}
	return len(srv.FirewallRules), true
}

// Exists reports whether a server is still there.
func (s *Server) Exists(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.servers[name]
	return ok
}

// MakePrivate delegates a server to a subnet, for the test that checks the
// provider refuses to cross Azure's public and private access boundary.
func (s *Server) MakePrivate(name, subnet string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	srv, ok := s.servers[name]
	if !ok {
		return false
	}
	srv.Subnet = subnet
	return true
}

func (s *Server) databaseFor(name string) string {
	sum := 0
	for _, r := range name {
		sum = sum*31 + int(r)
	}
	if sum < 0 {
		sum = -sum
	}
	return fmt.Sprintf("%s%s_%d", s.opts.Prefix, sanitise(name), sum%100000)
}

func (s *Server) execOn(database, statements string) error {
	db, err := sql.Open("pgx", replaceDatabase(s.opts.AdminURL, database))
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(statements); err != nil {
		return fmt.Errorf("fakeazurepg: running seed on %s: %w", database, err)
	}
	return nil
}

// copyDatabase is the restore, and the byte count is read BEFORE the copy so a
// non zero reading is bytes Postgres actually moved.
func (s *Server) copyDatabase(from, to string) error {
	var size int64
	if err := s.admin.QueryRow(`SELECT pg_database_size($1)`, from).Scan(&size); err != nil {
		return fmt.Errorf("fakeazurepg: sizing %s: %w", from, err)
	}
	if _, err := s.admin.Exec(
		`CREATE DATABASE ` + quoteIdent(to) + ` TEMPLATE ` + quoteIdent(from)); err != nil {
		return fmt.Errorf("fakeazurepg: restoring %s into %s: %w", from, to, err)
	}
	s.bytesCopied += size
	return nil
}

func (s *Server) nextOp() string {
	s.seq++
	name := fmt.Sprintf("op-%d", s.seq)
	s.ops[name] = "Succeeded"
	return name
}

func sanitise(in string) string {
	var b strings.Builder
	for _, r := range in {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 32)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func replaceDatabase(base, database string) string {
	if i := strings.LastIndex(base, "/"); i >= 0 {
		rest := ""
		if j := strings.Index(base[i:], "?"); j >= 0 {
			rest = base[i+j:]
		}
		return base[:i+1] + database + rest
	}
	return base
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{
		"code": code, "message": message,
	}})
}
