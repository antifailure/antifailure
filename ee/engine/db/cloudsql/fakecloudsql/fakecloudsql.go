// Package fakecloudsql is a Cloud SQL Admin API control plane backed by a real
// Postgres.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// What this is and what it is NOT, stated here rather than left to be inferred,
// because the same sentence had to be written for fakerds and it is the part a
// reader most needs.
//
// It IS a control plane: it speaks the Admin API's HTTP shapes, holds instances
// and long running operations, and enforces the parts of Cloud SQL's behaviour
// this provider depends on. Behind each instance is a REAL Postgres database on
// a real server, so the conformance behaviours that are claims about BYTES,
// that a branch holds the golden's rows and is isolated from it and from other
// branches, are checked against bytes rather than against a fake's opinion of
// them.
//
// It is NOT evidence that Google accepts these requests. Nothing here has an
// account and nothing here should: section 10 of the plan says no test may need
// one. What stands between this fake and a real Cloud SQL is that the request
// shapes are what the Admin API documents, and that is not the same as Google
// having answered.
//
// THE ONE BEHAVIOUR THIS FAKE MODELS THAT MATTERS MOST, and the reason it is
// not a stub that returns 200 to everything: it distinguishes a FAST clone from
// a STANDARD one by exactly the rule Google documents, and it records which it
// served. A clone request naming a zone, or carrying a point in time, is served
// as a standard clone and counted as one. That is what lets a test assert that
// this provider never causes the slow path, which is the provider's central
// claim and the one that would otherwise be a comment.
//
// Cloning copies bytes here, with CREATE DATABASE ... TEMPLATE, which real fast
// cloning would not. The byte counter is what makes that visible to a test, and
// it is why this provider's conformance suite leaves RealService empty.
package fakecloudsql

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the pgx driver
)

// Options is what New needs.
type Options struct {
	// AdminURL is a Postgres this fake may create and drop databases on.
	AdminURL string
	// Prefix namespaces every database this fake creates.
	//
	// Required rather than optional because the Postgres these suites use is
	// SHARED: other suites in this repository and other branches' containers
	// use the same server, and two runs that agreed on a database name would
	// destroy each other's data rather than fail.
	Prefix string
	// Project is the only project this fake answers for.
	Project string
	// SourceInstance is the instance that exists before anything is created,
	// standing in for the customer's production database.
	SourceInstance string
	// SeedSQL is run against the source instance's database at startup.
	SeedSQL string
	// Now is the clock.
	Now func() time.Time
}

// Server is the fake control plane.
type Server struct {
	rolesMu      sync.Mutex
	createdRoles map[string]bool
	mu           sync.Mutex
	instances    map[string]*fakeInstance
	ops          map[string]*fakeOperation
	admin        *sql.DB
	opts         Options
	http         *httptest.Server
	seq          int

	// bytesCopied is what this fake moved, which a real fast clone would not.
	bytesCopied int64
	// fastClones and standardClones record which workflow each clone request
	// selected, by Google's own rule.
	fastClones     int
	standardClones int
}

type fakeInstance struct {
	Name             string
	Database         string
	Region           string
	Zone             string
	Tier             string
	DiskType         string
	DiskSizeGb       int64
	ActivationPolicy string
	UserLabels       map[string]string
	CreateTime       time.Time
	Password         string
	// Role is the REAL Postgres role standing in for this instance's
	// administrator, created when a password is set.
	//
	// A real role rather than a recorded string, because the provider then
	// authenticates as it against the real Postgres and the conformance
	// behaviours that are claims about bytes are reached through the same
	// credential path a real one would be. A fake that only remembered the
	// password would let the provider's password reset be deleted with every
	// test still green.
	Role          string
	ExtraUsers    map[string]*fakeUser
	DatabaseFlags map[string]string
}

type fakeUser struct {
	Name, Type, Role, Password string
	Listed                     bool
}

type fakeOperation struct {
	Name   string
	Status string
}

// New starts the fake and returns it.
func New(opts Options) (*Server, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Project == "" {
		opts.Project = "af-test"
	}
	if opts.SourceInstance == "" {
		opts.SourceInstance = "source"
	}
	if opts.Prefix == "" {
		return nil, fmt.Errorf("fakecloudsql: Prefix is required; the test Postgres is shared")
	}
	admin, err := sql.Open("pgx", opts.AdminURL)
	if err != nil {
		return nil, fmt.Errorf("fakecloudsql: opening admin connection: %w", err)
	}
	// One connection. Everything this does is a CREATE DATABASE or a DROP
	// DATABASE, neither of which may run inside a transaction, and a pool
	// would hand a later statement a different session than the one that
	// created the thing it names.
	admin.SetMaxOpenConns(1)

	s := &Server{
		instances:    map[string]*fakeInstance{},
		createdRoles: map[string]bool{},
		ops:          map[string]*fakeOperation{},
		admin:        admin,
		opts:         opts,
	}

	source := &fakeInstance{
		Name:             opts.SourceInstance,
		Database:         s.databaseFor(opts.SourceInstance),
		Region:           "us-central1",
		Zone:             "us-central1-a",
		Tier:             "db-custom-2-7680",
		DiskType:         "PD_SSD",
		DiskSizeGb:       10,
		ActivationPolicy: "ALWAYS",
		UserLabels:       map[string]string{},
		CreateTime:       opts.Now().UTC(),
		Password:         "production-password",
		Role:             "postgres",
		ExtraUsers:       map[string]*fakeUser{},
		DatabaseFlags:    map[string]string{},
	}
	if err := s.createDatabase(source.Database); err != nil {
		return nil, err
	}
	if opts.SeedSQL != "" {
		if err := s.execOn(source.Database, opts.SeedSQL); err != nil {
			return nil, err
		}
	}
	s.instances[source.Name] = source

	s.http = httptest.NewServer(http.HandlerFunc(s.serve))
	return s, nil
}

// URL is the endpoint a provider points at.
func (s *Server) URL() string { return s.http.URL }

func (s *Server) ResourceCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.instances)
}

// Close stops the fake and drops every database it made.
//
// It RETURNS what it could not clean up rather than swallowing it. A fake that
// leaked a database per run on a shared server is the shape of defect this
// repository keeps finding in its own instruments, and a cleanup whose failure
// mode is silence is always satisfied by silence.
func (s *Server) Close() []error {
	s.http.Close()
	s.mu.Lock()
	names := make([]string, 0, len(s.instances))
	roles := make([]string, 0, len(s.instances))
	for _, in := range s.instances {
		names = append(names, in.Database)
		if in.Role != "" && in.Role != "postgres" {
			roles = append(roles, in.Role)
		}
		for _, u := range in.ExtraUsers {
			roles = append(roles, u.Role)
		}
	}
	s.instances = map[string]*fakeInstance{}
	s.mu.Unlock()
	var problems []error
	for _, database := range names {
		if _, err := s.admin.Exec(
			`DROP DATABASE IF EXISTS ` + quoteIdent(database) + ` WITH (FORCE)`); err != nil {
			problems = append(problems, fmt.Errorf("dropping %s: %w", database, err))
		}
	}
	s.rolesMu.Lock()
	for role := range s.createdRoles {
		roles = append(roles, role)
	}
	s.rolesMu.Unlock()
	// Roles after databases: a role owning objects in a database that still
	// exists cannot be dropped, and a leaked role on a shared server is the
	// same class of defect as a leaked database.
	for _, role := range roles {
		if _, err := s.admin.Exec(`DROP ROLE IF EXISTS ` + quoteIdent(role)); err != nil {
			problems = append(problems, fmt.Errorf("dropping role %s: %w", role, err))
		}
	}
	if err := s.admin.Close(); err != nil {
		problems = append(problems, err)
	}
	return problems
}

// BytesCopied is what this fake copied, which a real fast clone would not.
func (s *Server) BytesCopied() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bytesCopied
}

// FastClones and StandardClones are how many of each workflow was served.
//
// The pair is the instrument behind this provider's central claim. A provider
// that stopped building fastCloneRequest, or that started naming a zone, would
// move a count from the first to the second, and a test can say no about that
// without an account.
func (s *Server) FastClones() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fastClones
}

func (s *Server) StandardClones() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.standardClones
}

// Reset zeroes the counters without touching the instances.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bytesCopied = 0
	s.fastClones = 0
	s.standardClones = 0
}

// URLFor is the Postgres connection string for one instance's database, which
// a test uses to look at what a branch actually holds.
func (s *Server) URLFor(instance string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.instances[instance]
	if !ok {
		return "", false
	}
	return replaceDatabase(s.opts.AdminURL, in.Database), true
}

func (s *Server) databaseFor(instance string) string {
	sum := 0
	for _, r := range instance {
		sum = sum*31 + int(r)
	}
	return s.opts.Prefix + strings.ToLower(sanitise(instance)) + "_" + strconv.Itoa(abs(sum)%100000)
}

func (s *Server) createDatabase(name string) error {
	if _, err := s.admin.Exec(`CREATE DATABASE ` + quoteIdent(name)); err != nil {
		return fmt.Errorf("fakecloudsql: creating %s: %w", name, err)
	}
	return nil
}

func (s *Server) execOn(database, statements string) error {
	db, err := sql.Open("pgx", replaceDatabase(s.opts.AdminURL, database))
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(statements); err != nil {
		return fmt.Errorf("fakecloudsql: running seed on %s: %w", database, err)
	}
	return nil
}

// copyDatabase is the clone, and it is what a real fast clone would not do.
//
// The byte count is read from pg_database_size immediately BEFORE the copy, so
// a non zero reading is bytes Postgres actually moved rather than a
// declaration. See the package comment.
func (s *Server) copyDatabase(from, to string) error {
	var size int64
	if err := s.admin.QueryRow(`SELECT pg_database_size($1)`, from).Scan(&size); err != nil {
		return fmt.Errorf("fakecloudsql: sizing %s: %w", from, err)
	}
	if _, err := s.admin.Exec(
		`CREATE DATABASE ` + quoteIdent(to) + ` TEMPLATE ` + quoteIdent(from)); err != nil {
		return fmt.Errorf("fakecloudsql: cloning %s into %s: %w", from, to, err)
	}
	s.bytesCopied += size
	return nil
}

func (s *Server) nextOp(kind string) *fakeOperation {
	s.seq++
	op := &fakeOperation{Name: fmt.Sprintf("op-%s-%d", kind, s.seq), Status: "DONE"}
	s.ops[op.Name] = op
	return op
}

func abs(i int) int {
	if i < 0 {
		return -i
	}
	return i
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

// replaceDatabase swaps the database in a Postgres URL.
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
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{
		"code": status, "status": code, "message": message,
	}})
}

// pgUser is the role this fake's own connection owns objects as, and which
// every role it creates is a member of.
//
// Membership rather than superuser: it carries the owner's privileges on the
// owner's objects, which is what the masking and the suite's own writes need,
// and it leaves no superuser behind on a server other suites share.
func (s *Server) pgUser() string {
	var name string
	if err := s.admin.QueryRow(`SELECT current_user`).Scan(&name); err != nil {
		return "postgres"
	}
	return name
}

// ensureRole creates or replaces the real Postgres role for one instance.
//
// A REAL role rather than a recorded string, because the provider then
// authenticates as it against the real Postgres, so the conformance behaviours
// that are claims about bytes are reached through the same credential path a
// real one would be. A fake that only remembered the password would let the
// provider's password reset be deleted with every test still green.
func (s *Server) ensureRole(role, password string) error {
	s.rolesMu.Lock()
	s.createdRoles[role] = true
	s.rolesMu.Unlock()
	owner := s.pgUser()
	var exists bool
	if err := s.admin.QueryRow("SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname=$1)", role).Scan(&exists); err != nil {
		return err
	}
	statement := "CREATE ROLE " + quoteIdent(role) + " LOGIN INHERIT PASSWORD " + quoteLiteral(password) + " IN ROLE " + quoteIdent(owner)
	if exists {
		statement = "ALTER ROLE " + quoteIdent(role) + " LOGIN PASSWORD " + quoteLiteral(password)
	}
	if _, err := s.admin.Exec(statement); err != nil {
		return fmt.Errorf("fakecloudsql: preparing role %q: %w", role, err)
	}

	return nil
}

// AddUser installs a real, separately authenticating source role. Clones copy
// its password into their own independent role, just as the vendor does.
func (s *Server) AddUser(instance, name, kind, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.instances[instance]
	if !ok {
		return fmt.Errorf("fakecloudsql: unknown instance %q", instance)
	}
	role := s.userRole(instance, name)
	if err := s.ensureRole(role, password); err != nil {
		return err
	}
	in.ExtraUsers[name] = &fakeUser{Name: name, Type: kind, Role: role, Password: password, Listed: true}
	if in.Role != "" && in.Role != "postgres" {
		if _, err := s.admin.Exec("GRANT " + quoteIdent(role) + " TO " + quoteIdent(in.Role) + " WITH ADMIN OPTION"); err != nil {
			return err
		}
	}

	if kind != "" && kind != "BUILT_IN" {
		in.DatabaseFlags["cloudsql.iam_authentication"] = "on"
	}
	// A real unrelated setting guards against replacing the whole flag set.
	in.DatabaseFlags["log_min_duration_statement"] = "1234"
	return nil
}

// UserURL uses the caller's credential against the real role for this instance.
func (s *Server) UserURL(instance, name, password string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.instances[instance]
	if !ok {
		return "", fmt.Errorf("fakecloudsql: unknown instance %q", instance)
	}
	u, ok := in.ExtraUsers[name]
	if !ok {
		return "", fmt.Errorf("fakecloudsql: unknown user %q", name)
	}
	parsed, err := url.Parse(replaceDatabase(s.opts.AdminURL, in.Database))
	if err != nil {
		return "", err
	}
	parsed.User = url.UserPassword(u.Role, password)
	return parsed.String(), nil
}

func (s *Server) userRole(instance, name string) string {
	sum := sha256.Sum256([]byte(name))
	base := s.roleFor(instance)
	if len(base) > 49 {
		base = base[:49]
	}
	return base + "u" + hex.EncodeToString(sum[:])[:10]
}

// roleFor is the role name for one instance, inside Postgres's 63 byte limit.
func (s *Server) roleFor(instance string) string {
	role := s.opts.Prefix + "r" + strings.ReplaceAll(instance, "-", "")
	if len(role) > 60 {
		role = role[:60]
	}
	return role
}

// quoteLiteral quotes a string for use as a SQL literal.
func quoteLiteral(in string) string {
	return `'` + strings.ReplaceAll(in, `'`, `''`) + `'`
}

// CustomerLogins is the explicit shared-cluster fixture boundary. It still
// reads actual pg_roles flags, but only for roles owned by this fake instance.
func (s *Server) CustomerLogins(ctx context.Context, db *sql.DB) ([]string, error) {
	var database string
	if err := db.QueryRowContext(ctx, "SELECT current_database()").Scan(&database); err != nil {
		return nil, err
	}
	s.mu.Lock()
	found := false
	var roles []string
	for _, in := range s.instances {
		if in.Database == database {
			found = true
			for _, user := range in.ExtraUsers {
				roles = append(roles, user.Role)
			}
			break
		}
	}
	s.mu.Unlock()
	if !found {
		return nil, fmt.Errorf("fakecloudsql: login catalog requested for a database this fixture does not own")
	}
	if len(roles) == 0 {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, "SELECT rolname FROM pg_catalog.pg_roles WHERE (rolcanlogin OR EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE usename=rolname)) AND rolname=ANY($1::text[]) AND rolname<>current_user ORDER BY rolname", roles)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func (s *Server) AddSQLLogin(instance, name, password string) error {
	if err := s.AddUser(instance, name, "BUILT_IN", password); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.instances[instance].ExtraUsers[name].Listed = false
	return nil
}

func (s *Server) UserPasswordMatches(instance, name, password string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.instances[instance]
	if !ok {
		return false
	}
	user, ok := in.ExtraUsers[name]
	return ok && user.Password == password
}
