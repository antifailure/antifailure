// Package fakexata is a Xata control plane that answers on localhost, backed by
// a real Postgres.
//
// It is a package rather than a test file for one reason: the engine's own
// orchestrator has to be able to drive the Xata provider through it. A test in
// engine/internal/env proves that a built in provider's progress reaches the
// event stream through the engine's attach, and that test cannot import a file
// that only compiles inside the provider's own package. So the fake lives here,
// the way fakerds, fakecloudsql and fakeazurepg live beside the enterprise
// providers they drive. It is a test fixture and nothing ships it.
//
// So the control plane is fake and the DATA PLANE IS REAL. Each branch this
// pretends to create is an actual database on an actual Postgres, and a branch
// of a branch is CREATE DATABASE ... TEMPLATE. The provider gets one override,
// its base URL, and every other line of it runs unchanged: the URLs it builds,
// the JSON it sends, the status codes it maps and the connection strings it
// hands out are the real ones.
//
// WHAT THIS PROVES AND WHAT IT CANNOT. It proves the provider's logic, its
// request shapes and its error mapping. It does NOT prove that Xata accepts
// those requests, and it cannot produce a wall clock number. It also cannot
// exhibit copy on write: the only way one local Postgres can produce a second
// database holding the first one's data is to copy the files, which is why
// BytesCopied exists and why the provider's conformance run asserts no real
// service.
//
// WHERE THE RULES COME FROM. Every refusal below is one the document at
// https://api.xata.tech/openapi.json states, answered with a status that
// operation documents and in its ErrorResponse shape. Where the document states
// no rule, the fake invents none: it does not refuse a duplicate branch name,
// because the document does not say names are unique, and a fake that enforced
// a rule the vendor never stated would certify a provider against a service
// that does not exist.
package fakexata

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the pgx driver
)

// descriptionPattern and descriptionMax are the document's rule for a branch
// description. Duplicated from the provider rather than imported, because the
// provider's own tests import this package and an import back would be a
// cycle; the document is the authority for both copies.
var descriptionPattern = regexp.MustCompile(`^([a-zA-Z0-9][a-zA-Z0-9\-_./: ]*)?$`)

const descriptionMax = 255

// Options configure the fake.
type Options struct {
	// AdminURL is the Postgres this creates databases on. Required.
	AdminURL string
	// Org and Project address the project the fake serves. Defaults are used
	// when empty.
	Org     string
	Project string
}

// branch is one branch the control plane pretends to hold, in the shape of the
// document's BranchMetadata, which is what a get answers.
type branch struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	ParentID    *string   `json:"parentID"`
	Region      string    `json:"region"`
	Status      struct {
		StatusType string `json:"statusType"`
		Lifecycle  struct {
			State string `json:"state"`
		} `json:"lifecycle"`
	} `json:"status"`

	// dbName is the real database on the local Postgres. Unexported, so it is
	// not serialised: the credentials endpoint hands it out, which is how the
	// real control plane hands out a host and a database name too.
	dbName string
	// pending is how many more gets report this branch as still creating.
	pending int
}

// shortBranch is a branch as a listing, a create and a rename answer it. The
// document's BranchListMetadata and BranchShortMetadata carry no status, and a
// fake that returned one would let the provider depend on a field only a get
// provides.
type shortBranch struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	ParentID    *string   `json:"parentID"`
	Region      string    `json:"region"`
}

func short(b *branch) shortBranch {
	return shortBranch{b.ID, b.Name, b.Description, b.CreatedAt, b.UpdatedAt, b.ParentID, b.Region}
}

type recordedRequest struct {
	method string
	path   string
}

// Server is a running fake control plane.
type Server struct {
	admin    string
	adminURL *url.URL
	org      string
	project  string
	http     *httptest.Server

	mu       sync.Mutex
	branches map[string]*branch
	next     int
	requests []recordedRequest
	bodies   []map[string]any
	token    string

	failMethod  string
	failPath    string
	failStatus  int
	failMessage string

	// pendingPolls is how many gets a newly created branch reports as still
	// creating before it reports ready.
	pendingPolls int

	// bytesCopied is what pg_database_size reported for every template
	// immediately before the CREATE DATABASE that copied it. It is not a
	// declaration: a non zero reading is bytes Postgres actually moved, and
	// bytes Xata's copy on write snapshot would not have moved.
	bytesCopied int64
}

// New starts the control plane and the database behind the project's root.
//
// The root branch is created eagerly, because a Xata project always has one and
// the provider finds it by looking for the branch with no parent. A fake that
// started empty would be serving a project that cannot exist.
func New(opts Options) (*Server, error) {
	if opts.AdminURL == "" {
		return nil, errors.New("fakexata: AdminURL is required")
	}
	u, err := url.Parse(opts.AdminURL)
	if err != nil {
		return nil, fmt.Errorf("fakexata: the Postgres URL does not parse: %w", err)
	}
	s := &Server{
		admin: opts.AdminURL, adminURL: u,
		org: opts.Org, project: opts.Project,
		branches: map[string]*branch{},
	}
	if s.org == "" {
		s.org = "af-test-org"
	}
	if s.project == "" {
		s.project = "af-test-project"
	}
	root := s.newBranch("main", "", "")
	root.pending = 0
	if err := s.createDatabase(root.dbName, ""); err != nil {
		return nil, fmt.Errorf("fakexata: create the database behind the project's root branch: %w", err)
	}
	s.http = httptest.NewServer(http.HandlerFunc(s.handle))
	return s, nil
}

// URL is the control plane's base URL, which is what the provider's BaseURL is
// set to.
func (s *Server) URL() string { return s.http.URL }

// Org is the organization identifier the fake serves.
func (s *Server) Org() string { return s.org }

// Project is the project identifier the fake serves.
func (s *Server) Project() string { return s.project }

// Close stops the server and drops every database it made, returning what could
// not be dropped rather than hiding it: a fake that leaked a database per run on
// a shared server is the shape of defect this repository keeps finding in its
// own instruments.
func (s *Server) Close() []error {
	s.http.Close()
	s.mu.Lock()
	names := make([]string, 0, len(s.branches))
	for _, b := range s.branches {
		names = append(names, b.dbName)
	}
	s.mu.Unlock()
	var problems []error
	for _, n := range names {
		if err := s.dropDatabase(n); err != nil {
			problems = append(problems, err)
		}
	}
	return problems
}

// SetPendingPolls makes every branch created from now on report itself as
// still creating for n gets before it reports ready. The document's lifecycle
// states include creating, and a provider that waits on one is what its
// progress reporting exists for.
func (s *Server) SetPendingPolls(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingPolls = n
}

// ResetBytesCopied zeroes the byte counter, so a reading covers one operation.
func (s *Server) ResetBytesCopied() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bytesCopied = 0
}

// BytesCopied is what the fake's branches have copied since the last reset.
func (s *Server) BytesCopied() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bytesCopied
}

// PathsSeen returns every recorded request as "METHOD path".
func (s *Server) PathsSeen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.requests))
	for _, r := range s.requests {
		out = append(out, r.method+" "+r.path)
	}
	return out
}

// Bodies returns the decoded JSON body of every write, oldest first.
func (s *Server) Bodies() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.bodies...)
}

// Token is the Authorization header of the most recent request, so that the one
// thing a fake could silently not check is checked.
func (s *Server) Token() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token
}

// FailOnce answers the next request matching method and a path containing
// pathContains with status and message instead of doing the work. It is how the
// error mapping is exercised: a provider's behaviour on a 412 is not something a
// happy path can show.
func (s *Server) FailOnce(method, pathContains string, status int, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failMethod, s.failPath, s.failStatus, s.failMessage = method, pathContains, status, message
}

// newBranch allocates a branch record and its database name. Called with the
// lock held, or before the server is serving.
func (s *Server) newBranch(name, parent, description string) *branch {
	s.next++
	b := &branch{
		ID:          fmt.Sprintf("br_%d_%d", time.Now().UnixNano(), s.next),
		Name:        name,
		Description: description,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
		Region:      "us-east-1",
		dbName:      fmt.Sprintf("af_fake_xata_%d_%d", time.Now().UnixNano()%1000000, s.next),
		pending:     s.pendingPolls,
	}
	if parent != "" {
		p := parent
		b.ParentID = &p
	}
	b.Status.StatusType = "STATUS_TYPE_HEALTHY"
	b.Status.Lifecycle.State = "ready"
	s.branches[b.ID] = b
	return b
}

// adminDB opens the local Postgres.
//
// Every caller below runs on an httptest handler goroutine, so failures are
// returned as errors and answered as 500s rather than asserted: a failure
// asserted from a handler goroutine is not reported by the test that caused it.
func (s *Server) adminDB() (*sql.DB, error) {
	return sql.Open("pgx", s.admin)
}

// createDatabase makes the real database behind a branch, with a template when
// the branch has a parent. It is a COPY, and this package's header says why that
// matters.
func (s *Server) createDatabase(name, template string) error {
	db, err := s.adminDB()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	stmt := `CREATE DATABASE "` + name + `"`
	if template != "" {
		var size int64
		if err := db.QueryRow(`SELECT pg_catalog.pg_database_size($1)`, template).Scan(&size); err != nil {
			return err
		}
		s.mu.Lock()
		s.bytesCopied += size
		s.mu.Unlock()
		// CREATE DATABASE ... TEMPLATE refuses while any session is connected
		// to the template, and the provider has just written the metadata
		// table through one that the pool may not have released yet.
		_, _ = db.Exec(
			`SELECT pg_catalog.pg_terminate_backend(pid) FROM pg_catalog.pg_stat_activity
			  WHERE datname = $1 AND pid <> pg_catalog.pg_backend_pid()`, template)
		stmt += ` TEMPLATE "` + template + `"`
	}
	_, err = db.Exec(stmt)
	return err
}

func (s *Server) dropDatabase(name string) error {
	db, err := s.adminDB()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	_, _ = db.Exec(
		`SELECT pg_catalog.pg_terminate_backend(pid) FROM pg_catalog.pg_stat_activity
		  WHERE datname = $1 AND pid <> pg_catalog.pg_backend_pid()`, name)
	if _, err := db.Exec(`DROP DATABASE IF EXISTS "` + name + `"`); err != nil {
		return fmt.Errorf("fakexata: drop %s: %w", name, err)
	}
	return nil
}

// connString is what the credentials endpoint hands out: a real URL to the real
// database behind the branch.
func (s *Server) connString(b *branch) string {
	u := *s.adminURL
	u.Path = "/" + b.dbName
	return u.String()
}

// prefix is the path every call hangs off, built the same way the client builds
// it so that a mismatch shows up as a 404 from the fake rather than a silent
// pass.
func (s *Server) prefix() string {
	return "/organizations/" + s.org + "/projects/" + s.project + "/branches"
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.token = r.Header.Get("Authorization")
	s.requests = append(s.requests, recordedRequest{r.Method, r.URL.Path})
	s.mu.Unlock()

	var body map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body != nil {
			s.mu.Lock()
			s.bodies = append(s.bodies, body)
			s.mu.Unlock()
		}
	}

	// The injected failure, checked before anything is done, because an error
	// path that ran the work first would be testing a different thing.
	s.mu.Lock()
	inject := s.failMethod != "" && s.failMethod == r.Method &&
		(s.failPath == "" || strings.Contains(r.URL.Path, s.failPath))
	status, message := s.failStatus, s.failMessage
	if inject {
		s.failMethod = ""
	}
	s.mu.Unlock()
	if inject {
		writeError(w, status, message)
		return
	}

	path := r.URL.Path
	if !strings.HasPrefix(path, s.prefix()) {
		writeError(w, http.StatusNotFound, "no such path: "+path)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(path, s.prefix()), "/")

	switch {
	case rest == "" && r.Method == http.MethodGet:
		s.list(w)
	case rest == "" && r.Method == http.MethodPost:
		s.create(w, body)
	case strings.HasSuffix(rest, "/credentials") && r.Method == http.MethodGet:
		s.credentials(w, strings.TrimSuffix(rest, "/credentials"))
	case r.Method == http.MethodGet:
		s.get(w, rest)
	case r.Method == http.MethodPatch:
		s.patch(w, rest, body)
	case r.Method == http.MethodDelete:
		s.remove(w, rest)
	default:
		writeError(w, http.StatusBadRequest, "unsupported: "+r.Method+" "+path)
	}
}

// writeError answers in the document's ErrorResponse: a message and a code, and
// no identifier.
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": "fake_error", "message": message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) list(w http.ResponseWriter) {
	s.mu.Lock()
	out := make([]shortBranch, 0, len(s.branches))
	for _, b := range s.branches {
		out = append(out, short(b))
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"branches": out})
}

func (s *Server) create(w http.ResponseWriter, body map[string]any) {
	name, _ := body["name"].(string)
	mode, _ := body["mode"].(string)
	parent, _ := body["parentID"].(string)
	description, _ := body["description"].(string)

	// The contract the document states, enforced here rather than assumed. A
	// fake that accepted a request the real service would refuse is a fake that
	// certifies a provider the vendor will reject.
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if mode != "inherit" && mode != "custom" {
		writeError(w, http.StatusBadRequest, "mode must be inherit or custom, got "+mode)
		return
	}
	if mode == "inherit" && parent == "" {
		writeError(w, http.StatusBadRequest, "mode inherit requires parentID")
		return
	}
	if len(description) > descriptionMax || !descriptionPattern.MatchString(description) {
		writeError(w, http.StatusBadRequest,
			"description must match the document's pattern and be at most 255 characters")
		return
	}

	s.mu.Lock()
	src, ok := s.branches[parent]
	if !ok {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "no such parent: "+parent)
		return
	}
	created := s.newBranch(name, parent, description)
	// Both database names are copied out UNDER the lock, because another
	// handler may be writing the map.
	createdDB, template := created.dbName, src.dbName
	response := short(created)
	s.mu.Unlock()

	if err := s.createDatabase(createdDB, template); err != nil {
		writeError(w, http.StatusInternalServerError,
			"the fake could not create the database behind the branch: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, response)
}

// get answers BranchMetadata, the one shape with a status. A branch with gets
// still pending reports the document's creating lifecycle state and a transient
// status type, which is what a provider waiting for readiness has to read.
func (s *Server) get(w http.ResponseWriter, id string) {
	s.mu.Lock()
	b, ok := s.branches[id]
	var answer branch
	if ok {
		answer = *b
		if b.pending > 0 {
			b.pending--
			answer.Status.StatusType = "STATUS_TYPE_TRANSIENT"
			answer.Status.Lifecycle.State = "creating"
		}
	}
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "no such branch: "+id)
		return
	}
	writeJSON(w, http.StatusOK, answer)
}

func (s *Server) patch(w http.ResponseWriter, id string, body map[string]any) {
	s.mu.Lock()
	b, ok := s.branches[id]
	var response shortBranch
	if ok {
		if name, has := body["name"].(string); has && name != "" {
			b.Name = name
		}
		b.UpdatedAt = time.Now().UTC()
		response = short(b)
	}
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "no such branch: "+id)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) remove(w http.ResponseWriter, id string) {
	s.mu.Lock()
	b, ok := s.branches[id]
	if ok {
		for _, other := range s.branches {
			if other.ParentID != nil && *other.ParentID == id {
				// The document states no rule about deleting a branch that has
				// children, and a real storage system cannot drop the snapshot a
				// child still reads. Answered with 400, the generic refusal
				// DELETE documents, rather than with a status the operation does
				// not list. The provider refuses first in its own code, so this
				// is a backstop that should never be reached.
				s.mu.Unlock()
				writeError(w, http.StatusBadRequest, "branch "+id+" still has children")
				return
			}
		}
		delete(s.branches, id)
	}
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "no such branch: "+id)
		return
	}
	if err := s.dropDatabase(b.dbName); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) credentials(w http.ResponseWriter, id string) {
	s.mu.Lock()
	b, ok := s.branches[id]
	var conn, dbName string
	if ok {
		conn, dbName = s.connString(b), b.dbName
	}
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "no such branch: "+id)
		return
	}
	password, _ := s.adminURL.User.Password()
	writeJSON(w, http.StatusOK, map[string]any{
		"username":         s.adminURL.User.Username(),
		"password":         password,
		"hostname":         s.adminURL.Hostname(),
		"port":             portOf(s.adminURL),
		"dbname":           dbName,
		"connectionString": conn,
	})
}

func portOf(u *url.URL) int {
	if u.Port() == "" {
		return 5432
	}
	var n int
	_, _ = fmt.Sscanf(u.Port(), "%d", &n)
	return n
}
