// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package fakecloudsql

// The HTTP surface: the Admin API paths this provider calls, and nothing else.
//
// An unknown path answers 404 with a body naming it rather than falling through
// to a 200. A fake that answered everything would let a provider call a method
// that does not exist and pass its own suite.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// /v1/projects/{project}/instances[/{instance}[/{verb}]]
	// /v1/projects/{project}/operations/{operation}
	if len(parts) < 4 || parts[0] != "v1" || parts[1] != "projects" {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such path: "+r.URL.Path)
		return
	}
	project := parts[2]
	if project != s.opts.Project {
		writeErr(w, http.StatusNotFound, "NOT_FOUND",
			"this fake answers for project "+s.opts.Project+", not "+project)
		return
	}
	switch parts[3] {
	case "instances":
		s.serveInstances(w, r, parts[4:])
	case "operations":
		s.serveOperations(w, parts[4:])
	default:
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such collection: "+parts[3])
	}
}

func (s *Server) serveOperations(w http.ResponseWriter, rest []string) {
	if len(rest) != 1 {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "an operation name is required")
		return
	}
	s.mu.Lock()
	op, ok := s.ops[rest[0]]
	s.mu.Unlock()
	if !ok {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such operation: "+rest[0])
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": op.Name, "status": op.Status})
}

func (s *Server) serveInstances(w http.ResponseWriter, r *http.Request, rest []string) {
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		s.list(w)
	case len(rest) == 1 && r.Method == http.MethodGet:
		s.get(w, rest[0])
	case len(rest) == 1 && r.Method == http.MethodDelete:
		s.delete(w, rest[0])
	case len(rest) == 1 && r.Method == http.MethodPatch:
		s.patch(w, r, rest[0])
	case len(rest) == 2 && rest[1] == "clone" && r.Method == http.MethodPost:
		s.clone(w, r, rest[0])
	case len(rest) == 2 && rest[1] == "users" && r.Method == http.MethodPut:
		s.setPassword(w, r, rest[0])
	case len(rest) == 2 && rest[1] == "databases" && r.Method == http.MethodGet:
		s.listDatabases(w, rest[0])
	case len(rest) == 2 && rest[1] == "users" && r.Method == http.MethodGet:
		s.listUsers(w, rest[0])
	default:
		writeErr(w, http.StatusNotFound, "NOT_FOUND",
			"no such method: "+r.Method+" "+r.URL.Path)
	}
}

func (s *Server) render(in *fakeInstance) map[string]any {
	return map[string]any{
		"kind":            "sql#instance",
		"name":            in.Name,
		"region":          in.Region,
		"databaseVersion": "POSTGRES_16",
		"state":           stateOf(in),
		"createTime":      in.CreateTime.UTC().Format(time.RFC3339Nano),
		"connectionName":  s.opts.Project + ":" + in.Region + ":" + in.Name,
		"ipAddresses": []map[string]any{
			{"type": "PRIMARY", "ipAddress": "127.0.0.1"},
		},
		"settings": map[string]any{
			"tier":               in.Tier,
			"activationPolicy":   in.ActivationPolicy,
			"userLabels":         in.UserLabels,
			"dataDiskType":       in.DiskType,
			"dataDiskSizeGb":     strconv.FormatInt(in.DiskSizeGb, 10),
			"locationPreference": map[string]any{"zone": in.Zone},
		},
	}
}

// stateOf maps the activation policy onto the state field the way Cloud SQL
// does, because a provider reading state has to see a stopped instance as
// stopped rather than as runnable.
func stateOf(in *fakeInstance) string {
	if in.ActivationPolicy == "NEVER" {
		return "STOPPED"
	}
	return "RUNNABLE"
}

func (s *Server) list(w http.ResponseWriter) {
	s.mu.Lock()
	items := make([]map[string]any, 0, len(s.instances))
	for _, in := range s.instances {
		items = append(items, s.render(in))
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"kind": "sql#instancesList", "items": items})
}

func (s *Server) get(w http.ResponseWriter, name string) {
	s.mu.Lock()
	in, ok := s.instances[name]
	var body map[string]any
	if ok {
		body = s.render(in)
	}
	s.mu.Unlock()
	if !ok {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such instance: "+name)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) delete(w http.ResponseWriter, name string) {
	s.mu.Lock()
	in, ok := s.instances[name]
	if !ok {
		s.mu.Unlock()
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such instance: "+name)
		return
	}
	delete(s.instances, name)
	database := in.Database
	op := s.nextOp("delete")
	s.mu.Unlock()

	_, _ = s.admin.Exec(`DROP DATABASE IF EXISTS ` + quoteIdent(database) + ` WITH (FORCE)`)
	writeJSON(w, http.StatusOK, map[string]any{"name": op.Name, "status": op.Status})
}

func (s *Server) patch(w http.ResponseWriter, r *http.Request, name string) {
	var body struct {
		Settings struct {
			UserLabels       map[string]string `json:"userLabels"`
			ActivationPolicy string            `json:"activationPolicy"`
			Tier             string            `json:"tier"`
		} `json:"settings"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "INVALID_ARGUMENT", "undecodable body: "+err.Error())
		return
	}
	s.mu.Lock()
	in, ok := s.instances[name]
	if !ok {
		s.mu.Unlock()
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such instance: "+name)
		return
	}
	if body.Settings.UserLabels != nil {
		in.UserLabels = body.Settings.UserLabels
	}
	if body.Settings.ActivationPolicy != "" {
		in.ActivationPolicy = body.Settings.ActivationPolicy
	}
	if body.Settings.Tier != "" {
		in.Tier = body.Settings.Tier
	}
	op := s.nextOp("patch")
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"name": op.Name, "status": op.Status})
}

func (s *Server) setPassword(w http.ResponseWriter, r *http.Request, name string) {
	var body struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "INVALID_ARGUMENT", "undecodable body: "+err.Error())
		return
	}
	s.mu.Lock()
	in, ok := s.instances[name]
	if !ok {
		s.mu.Unlock()
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such instance: "+name)
		return
	}
	role := in.Role
	if role == "" || role == "postgres" {
		role = s.roleFor(name)
	}
	_ = role
	in.Password = body.Password
	in.Role = role
	op := s.nextOp("users")
	s.mu.Unlock()

	// The REAL role, so that the provider authenticates against the real
	// Postgres with the password it just derived. See the Role field's comment.
	if err := s.ensureRole(role, body.Password); err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": op.Name, "status": op.Status})
}

// listUsers reports the administrator standing in for this instance.
//
// It is the per instance ROLE rather than the literal "postgres", which is what
// lets one shared Postgres stand in for many instances each with its own
// credential. The provider reads this collection instead of assuming the name,
// which is the behaviour this models.
func (s *Server) listUsers(w http.ResponseWriter, name string) {
	s.mu.Lock()
	in, ok := s.instances[name]
	var role string
	if ok {
		role = in.Role
		if role == "" {
			role = s.roleFor(name)
		}
	}
	s.mu.Unlock()
	if !ok {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such instance: "+name)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"kind": "sql#usersList",
		"items": []map[string]any{
			{"kind": "sql#user", "name": role, "instance": name},
		},
	})
}

// clone is the method this whole fake exists for.
//
// It applies Google's own rule for which workflow a clone request selects, and
// it counts them separately. The rule, from the clone-instance page:
//
//   - a cloneContext naming preferredZone takes the STANDARD path, even when
//     the zone named is the source's own zone,
//   - a cloneContext carrying pointInTime takes the STANDARD path,
//   - otherwise, same zone by default, the FAST path.
//
// Both paths produce the same instance here. What differs is the counter, and
// the counter is the only thing that can tell a test which one a provider
// caused. That is the point: on real Cloud SQL the two are also
// indistinguishable from the response, which is exactly why the provider must
// be built so it cannot ask for the slow one.
func (s *Server) clone(w http.ResponseWriter, r *http.Request, source string) {
	var body struct {
		CloneContext struct {
			Kind                    string `json:"kind"`
			DestinationInstanceName string `json:"destinationInstanceName"`
			PreferredZone           string `json:"preferredZone"`
			PointInTime             string `json:"pointInTime"`
		} `json:"cloneContext"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "INVALID_ARGUMENT", "undecodable body: "+err.Error())
		return
	}
	destination := body.CloneContext.DestinationInstanceName
	if destination == "" {
		writeErr(w, http.StatusBadRequest, "INVALID_ARGUMENT",
			"cloneContext.destinationInstanceName is required")
		return
	}

	s.mu.Lock()
	src, ok := s.instances[source]
	if !ok {
		s.mu.Unlock()
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such instance: "+source)
		return
	}
	if _, exists := s.instances[destination]; exists {
		s.mu.Unlock()
		writeErr(w, http.StatusConflict, "ALREADY_EXISTS",
			"instance already exists: "+destination)
		return
	}
	fast := body.CloneContext.PreferredZone == "" && body.CloneContext.PointInTime == ""
	if fast {
		s.fastClones++
	} else {
		s.standardClones++
	}
	database := s.databaseFor(destination)
	if err := s.copyDatabase(src.Database, database); err != nil {
		s.mu.Unlock()
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	// A clone carries the source's users and passwords. Modelled because the
	// provider's password reset exists precisely to undo it, and a fake that
	// started every clone with a blank password would let that reset be
	// deleted with every test still green.
	s.instances[destination] = &fakeInstance{
		Name:             destination,
		Database:         database,
		Region:           src.Region,
		Zone:             src.Zone,
		Tier:             src.Tier,
		DiskType:         src.DiskType,
		DiskSizeGb:       src.DiskSizeGb,
		ActivationPolicy: src.ActivationPolicy,
		UserLabels:       map[string]string{},
		CreateTime:       s.opts.Now().UTC(),
		// The PASSWORD is inherited, which is the Cloud SQL behaviour the
		// provider's password reset exists to undo.
		Password: src.Password,
		// The ROLE is not. On real Cloud SQL every instance carries its own
		// copy of the source's users, so two clones of one source have two
		// independent "postgres" roles that happen to share a name. Here every
		// instance is a database on ONE Postgres, so sharing the name would
		// mean sharing the ROLE, and the second branch's password reset would
		// silently change the first branch's credential. That is not a
		// modelling nicety: it is what made Branch_IsIsolatedFromOtherBranches
		// fail with a SASL authentication error against a role that existed.
		Role: s.roleFor(destination),
	}
	op := s.nextOp("clone")
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"name": op.Name, "status": op.Status})
}

// listDatabases reports the one database standing in for this instance.
//
// One rather than the several a real instance has, because in this fake an
// instance IS a database on the shared Postgres. That is what lets the provider
// discover the database name through the same Admin API call it would make
// against Google, instead of the suite needing a second code path in the
// provider to find its data.
func (s *Server) listDatabases(w http.ResponseWriter, name string) {
	s.mu.Lock()
	in, ok := s.instances[name]
	var database string
	if ok {
		database = in.Database
	}
	s.mu.Unlock()
	if !ok {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such instance: "+name)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"kind": "sql#databasesList",
		"items": []map[string]any{
			{"kind": "sql#database", "name": database, "instance": name},
		},
	})
}

// PasswordOf is what the instance's postgres role is set to, for a test that
// checks the provider actually reset it.
func (s *Server) PasswordOf(instance string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.instances[instance]
	if !ok {
		return "", false
	}
	return in.Password, true
}
