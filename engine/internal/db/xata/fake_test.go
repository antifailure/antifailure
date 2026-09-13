package xata

// A FAKE CLOUD CONTROL PLANE over a REAL LOCAL POSTGRES, which is the shape
// this wave prescribes and the shape engine/internal/db/pgurl models.
//
// An httptest server speaks Xata's control API in Xata's own response shapes,
// while each branch it pretends to create is a real database on a local
// Postgres. The provider gets ONE override, BaseURL, and every other line of it
// runs unchanged: the URLs it builds, the JSON it sends, the status codes it
// maps and the connection strings it hands out are all the real ones.
//
// WHAT THIS PROVES AND WHAT IT CANNOT.
//
// It proves the provider's logic, its request shapes and its error mapping. It
// does NOT prove that Xata accepts those requests, and it cannot produce a wall
// clock number. Nobody opened a Xata account for this.
//
// It also cannot exhibit copy on write, and that limit is structural rather
// than a gap somebody could close by writing more of this file. The only way
// one local Postgres can produce a second database holding the first one's data
// is CREATE DATABASE ... TEMPLATE, which copies the files. So the suite run in
// conformance_test.go does not assert a real service, the copy on write
// behaviour reports UNPROVEN rather than timing that copy, and
// TestTheFakeControlPlaneReallyCopies reads this file's byte counter so that
// the reason is a test rather than a sentence.
//
// WHERE THE FAKE'S RULES COME FROM. Every refusal below is one the document at
// https://api.xata.tech/openapi.json states, answered with a status that
// operation documents and in its ErrorResponse shape. Where the document states
// no rule, the fake invents none: it does not refuse a duplicate branch name,
// because the document does not say names are unique, and a fake that enforced
// a rule the vendor never stated would certify a provider against a service
// that does not exist.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeBranch is one branch the control plane pretends to hold.
type fakeBranch struct {
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
	// not serialised: the control plane hands it out through the credentials
	// endpoint, which is how the real one hands out a host and a database name
	// too.
	dbName string
}

// fakeXata is the control plane.
type fakeXata struct {
	t        *testing.T
	admin    string // the local Postgres this pretends to be a cloud
	adminURL *url.URL
	org      string
	project  string

	mu       sync.Mutex
	branches map[string]*fakeBranch
	next     int

	// requests records every method and path, so a test can assert on the
	// shapes the provider sent rather than only on what came back.
	requests []recordedRequest
	// bodies records the decoded JSON body of every write, for the same
	// reason.
	bodies []map[string]any

	// failNext, when set, answers the next matching request with this status
	// and message instead of doing the work. It is how the error mapping is
	// exercised: a provider's behaviour on a 412 is not something a happy path
	// can show.
	failMethod  string
	failPath    string
	failStatus  int
	failMessage string

	// token records the bearer token the provider sent, so that the one thing
	// a fake could silently not check is checked.
	token string

	// bytesCopied is what pg_database_size reported for every template
	// immediately before the CREATE DATABASE that copied it. It is not a
	// declaration: a non zero reading is bytes Postgres actually moved, and
	// bytes Xata's copy on write snapshot would not have moved.
	bytesCopied int64

	server *httptest.Server
}

type recordedRequest struct {
	Method string
	Path   string
	Auth   string
}

// newFakeXata starts the control plane and the databases behind it.
//
// The root branch is created eagerly and holds the seed, because a Xata project
// always has one and the provider finds it by looking for the branch with no
// parent. A fake that started empty would be testing a project that cannot
// exist.
func newFakeXata(t *testing.T, admin string) *fakeXata {
	t.Helper()
	u, err := url.Parse(admin)
	require.NoError(t, err, "the local Postgres URL does not parse")

	f := &fakeXata{
		t: t, admin: admin, adminURL: u,
		org: "af-test-org", project: "af-test-project",
		branches: map[string]*fakeBranch{},
	}
	root := f.newBranch("main", "", "")
	require.NoError(t, f.createDatabase(root.dbName, ""),
		"the fake could not create the database behind the project's root branch")
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(func() {
		f.server.Close()
		f.dropEverything()
	})
	return f
}

// newBranch allocates a branch record and its database name.
func (f *fakeXata) newBranch(name, parent, description string) *fakeBranch {
	f.next++
	b := &fakeBranch{
		ID:          fmt.Sprintf("br_%d_%d", time.Now().UnixNano(), f.next),
		Name:        name,
		Description: description,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
		Region:      "us-east-1",
		dbName:      fmt.Sprintf("af_fake_xata_%d_%d", time.Now().UnixNano()%1000000, f.next),
	}
	if parent != "" {
		p := parent
		b.ParentID = &p
	}
	b.Status.StatusType = "STATUS_TYPE_HEALTHY"
	b.Status.Lifecycle.State = "ready"
	f.branches[b.ID] = b
	return b
}

// adminDB opens the local Postgres.
//
// It returns an error rather than asserting, and that is not fastidiousness.
// Every caller below runs on an httptest handler goroutine, and require's
// FailNow may only be called from the goroutine running the test: calling it
// from a handler makes the test finish without reporting the failure, which is
// a green run over a fake that did not work.
func (f *fakeXata) adminDB() (*sql.DB, error) {
	return sql.Open("pgx", f.admin)
}

// createDatabase makes the real database behind a branch.
//
// With a template when the branch has a parent, which is what makes the fake's
// branch actually carry the parent's data. It is a COPY, and this file's header
// says why that matters.
func (f *fakeXata) createDatabase(name, template string) error {
	db, err := f.adminDB()
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
		f.mu.Lock()
		f.bytesCopied += size
		f.mu.Unlock()
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

func (f *fakeXata) dropDatabase(name string) {
	db, err := f.adminDB()
	if err != nil {
		return
	}
	defer func() { _ = db.Close() }()
	_, _ = db.Exec(
		`SELECT pg_catalog.pg_terminate_backend(pid) FROM pg_catalog.pg_stat_activity
		  WHERE datname = $1 AND pid <> pg_catalog.pg_backend_pid()`, name)
	_, _ = db.Exec(`DROP DATABASE IF EXISTS "` + name + `"`)
}

func (f *fakeXata) dropEverything() {
	f.mu.Lock()
	names := make([]string, 0, len(f.branches))
	for _, b := range f.branches {
		names = append(names, b.dbName)
	}
	f.mu.Unlock()
	for _, n := range names {
		f.dropDatabase(n)
	}
}

// connString is what the credentials endpoint hands out: a real URL to the real
// database behind the branch.
func (f *fakeXata) connString(b *fakeBranch) string {
	u := *f.adminURL
	u.Path = "/" + b.dbName
	return u.String()
}

// prefix is the path every call hangs off, built the same way the client builds
// it so that a mismatch shows up as a 404 from the fake rather than as a silent
// pass.
func (f *fakeXata) prefix() string {
	return "/organizations/" + f.org + "/projects/" + f.project + "/branches"
}

func (f *fakeXata) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.token = r.Header.Get("Authorization")
	f.requests = append(f.requests, recordedRequest{r.Method, r.URL.Path, r.Header.Get("Authorization")})
	f.mu.Unlock()

	var body map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body != nil {
			f.mu.Lock()
			f.bodies = append(f.bodies, body)
			f.mu.Unlock()
		}
	}

	// The injected failure, checked before anything is done, because an error
	// path that ran the work first would be testing a different thing.
	f.mu.Lock()
	inject := f.failMethod != "" && f.failMethod == r.Method &&
		(f.failPath == "" || strings.Contains(r.URL.Path, f.failPath))
	status, message := f.failStatus, f.failMessage
	if inject {
		f.failMethod = ""
	}
	f.mu.Unlock()
	if inject {
		f.writeError(w, status, message)
		return
	}

	path := r.URL.Path
	if !strings.HasPrefix(path, f.prefix()) {
		f.writeError(w, http.StatusNotFound, "no such path: "+path)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(path, f.prefix()), "/")

	switch {
	case rest == "" && r.Method == http.MethodGet:
		f.list(w)
	case rest == "" && r.Method == http.MethodPost:
		f.create(w, body)
	case strings.HasSuffix(rest, "/credentials") && r.Method == http.MethodGet:
		f.credentials(w, strings.TrimSuffix(rest, "/credentials"))
	case r.Method == http.MethodGet:
		f.get(w, rest)
	case r.Method == http.MethodPatch:
		f.patch(w, rest, body)
	case r.Method == http.MethodDelete:
		f.remove(w, rest)
	default:
		f.writeError(w, http.StatusBadRequest, "unsupported: "+r.Method+" "+path)
	}
}

func (f *fakeXata) writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The document's ErrorResponse: a message and a code, and no identifier.
	_ = json.NewEncoder(w).Encode(map[string]string{"code": "fake_error", "message": message})
}

func (f *fakeXata) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
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

func short(b *fakeBranch) shortBranch {
	return shortBranch{b.ID, b.Name, b.Description, b.CreatedAt, b.UpdatedAt, b.ParentID, b.Region}
}

func (f *fakeXata) list(w http.ResponseWriter) {
	f.mu.Lock()
	out := make([]shortBranch, 0, len(f.branches))
	for _, b := range f.branches {
		out = append(out, short(b))
	}
	f.mu.Unlock()
	f.writeJSON(w, http.StatusOK, map[string]any{"branches": out})
}

// Reset zeroes the byte counter, so a reading covers one operation.
func (f *fakeXata) resetCounter() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bytesCopied = 0
}

// BytesCopied is what the fake's branches have copied since the last reset.
func (f *fakeXata) copied() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bytesCopied
}

func (f *fakeXata) create(w http.ResponseWriter, body map[string]any) {
	name, _ := body["name"].(string)
	mode, _ := body["mode"].(string)
	parent, _ := body["parentID"].(string)
	description, _ := body["description"].(string)

	// The contract Xata's own reference states, enforced here rather than
	// assumed. A fake that accepted a request the real service would refuse is
	// a fake that certifies a provider the vendor will reject.
	if name == "" {
		f.writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if mode != "inherit" && mode != "custom" {
		f.writeError(w, http.StatusBadRequest, "mode must be inherit or custom, got "+mode)
		return
	}
	if mode == "inherit" && parent == "" {
		f.writeError(w, http.StatusBadRequest, "mode inherit requires parentID")
		return
	}
	if len(description) > 255 || !descriptionPattern.MatchString(description) {
		f.writeError(w, http.StatusBadRequest,
			"description must match the document's pattern and be at most 255 characters")
		return
	}

	f.mu.Lock()
	src, ok := f.branches[parent]
	if !ok {
		f.mu.Unlock()
		f.writeError(w, http.StatusNotFound, "no such parent: "+parent)
		return
	}
	created := f.newBranch(name, parent, description)
	// Both database names are copied out UNDER the lock. Reading them after
	// unlocking would be a read of shared state from a handler goroutine while
	// another handler may be writing the map, which the race detector is right
	// to call a race even though nothing here mutates a name.
	createdDB, template := created.dbName, src.dbName
	response := short(created)
	f.mu.Unlock()

	if err := f.createDatabase(createdDB, template); err != nil {
		// Reported as a 500 rather than asserted, for the reason adminDB
		// records: this runs on a handler goroutine. The provider then fails
		// with the fake's own message, which is what a test reading the output
		// needs to tell "the fake broke" from "the provider is wrong".
		f.writeError(w, http.StatusInternalServerError,
			"the fake could not create the database behind the branch: "+err.Error())
		return
	}
	f.writeJSON(w, http.StatusCreated, response)
}

func (f *fakeXata) get(w http.ResponseWriter, id string) {
	f.mu.Lock()
	b, ok := f.branches[id]
	f.mu.Unlock()
	if !ok {
		f.writeError(w, http.StatusNotFound, "no such branch: "+id)
		return
	}
	f.writeJSON(w, http.StatusOK, b)
}

func (f *fakeXata) patch(w http.ResponseWriter, id string, body map[string]any) {
	f.mu.Lock()
	b, ok := f.branches[id]
	if ok {
		if name, has := body["name"].(string); has && name != "" {
			b.Name = name
		}
		b.UpdatedAt = time.Now().UTC()
	}
	f.mu.Unlock()
	if !ok {
		f.writeError(w, http.StatusNotFound, "no such branch: "+id)
		return
	}
	f.writeJSON(w, http.StatusOK, short(b))
}

func (f *fakeXata) remove(w http.ResponseWriter, id string) {
	f.mu.Lock()
	b, ok := f.branches[id]
	if ok {
		for _, other := range f.branches {
			if other.ParentID != nil && *other.ParentID == id {
				// The document states no rule about deleting a branch that
				// has children, and a real storage system cannot drop the
				// snapshot a child still reads. Answered with 400, the
				// generic refusal DELETE documents, rather than with a status
				// the operation does not list. The provider refuses first in
				// its own code, so this is a backstop that should never be
				// reached.
				f.mu.Unlock()
				f.writeError(w, http.StatusBadRequest,
					"branch "+id+" still has children")
				return
			}
		}
		delete(f.branches, id)
	}
	f.mu.Unlock()
	if !ok {
		f.writeError(w, http.StatusNotFound, "no such branch: "+id)
		return
	}
	f.dropDatabase(b.dbName)
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeXata) credentials(w http.ResponseWriter, id string) {
	f.mu.Lock()
	b, ok := f.branches[id]
	f.mu.Unlock()
	if !ok {
		f.writeError(w, http.StatusNotFound, "no such branch: "+id)
		return
	}
	user := f.adminURL.User.Username()
	password, _ := f.adminURL.User.Password()
	f.writeJSON(w, http.StatusOK, map[string]any{
		"username":         user,
		"password":         password,
		"hostname":         f.adminURL.Hostname(),
		"port":             portOf(f.adminURL),
		"dbname":           b.dbName,
		"connectionString": f.connString(b),
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

// pathsSeen returns the recorded requests as "METHOD path" strings.
func (f *fakeXata) pathsSeen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.requests))
	for _, r := range f.requests {
		out = append(out, r.Method+" "+r.Path)
	}
	return out
}

// failOnce arms one injected failure.
func (f *fakeXata) failOnce(method, pathContains string, status int, message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failMethod, f.failPath, f.failStatus, f.failMessage = method, pathContains, status, message
}
