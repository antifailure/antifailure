// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package fakeazurepg

// The HTTP surface: the Resource Manager paths this provider calls, and
// nothing else.
//
// An unknown path answers 404 with a body naming it rather than falling
// through to a 200. A fake that answered everything would let a provider call
// a method that does not exist and pass its own suite.
//
// Every request must carry api-version, and one that does not is refused the
// way Resource Manager refuses it. That is modelled rather than ignored
// because omitting it is a real and easy mistake whose error names the
// parameter and not the operation, so a provider that dropped it would fail
// confusingly in a subscription and not at all against a permissive fake.

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer AF_FAKE_AZUREPG_TOKEN" {
		writeErr(w, http.StatusUnauthorized, "AuthenticationFailed", "an Azure identity is required")
		return
	}
	if r.URL.Query().Get("api-version") == "" && !strings.HasPrefix(r.URL.Path, "/operations/") {
		writeErr(w, http.StatusBadRequest, "MissingApiVersionParameter",
			"The api-version query parameter (?api-version=) is required for all requests.")
		return
	}
	path := strings.Trim(r.URL.Path, "/")

	if rest, ok := strings.CutPrefix(path, "operations/"); ok {
		s.mu.Lock()
		status, exists := s.ops[rest]
		s.mu.Unlock()
		if !exists {
			writeErr(w, http.StatusNotFound, "OperationNotFound", "no such operation: "+rest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": status})
		return
	}

	parts := strings.Split(path, "/")
	// subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.DBforPostgreSQL/flexibleServers[/{name}[/firewallRules/{rule}]]
	if len(parts) < 7 || parts[0] != "subscriptions" || parts[2] != "resourceGroups" {
		writeErr(w, http.StatusNotFound, "NotFound", "no such path: "+r.URL.Path)
		return
	}
	if parts[1] != s.opts.Subscription {
		writeErr(w, http.StatusNotFound, "SubscriptionNotFound",
			"this fake answers for subscription "+s.opts.Subscription)
		return
	}
	if parts[3] != s.opts.ResourceGroup {
		writeErr(w, http.StatusNotFound, "ResourceGroupNotFound",
			"this fake answers for resource group "+s.opts.ResourceGroup)
		return
	}
	if parts[5] != "Microsoft.DBforPostgreSQL" || parts[6] != "flexibleServers" {
		writeErr(w, http.StatusNotFound, "NotFound", "no such provider path: "+r.URL.Path)
		return
	}
	rest := parts[7:]
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		s.list(w)
	case len(rest) == 1 && r.Method == http.MethodGet:
		s.get(w, rest[0])
	case len(rest) == 1 && r.Method == http.MethodPut:
		s.restore(w, r, rest[0])
	case len(rest) == 1 && r.Method == http.MethodPatch:
		s.patch(w, r, rest[0])
	case len(rest) == 1 && r.Method == http.MethodDelete:
		s.delete(w, rest[0])
	case len(rest) == 3 && rest[1] == "firewallRules" && r.Method == http.MethodPut:
		s.putFirewall(w, r, rest[0], rest[2])
	case len(rest) == 2 && rest[1] == "databases" && r.Method == http.MethodGet:
		s.mu.Lock()
		server, ok := s.servers[rest[0]]
		var names []map[string]string
		if ok {
			names = []map[string]string{{"name": "postgres"}, {"name": server.Database}}
		}
		s.mu.Unlock()
		if !ok {
			writeErr(w, http.StatusNotFound, "ResourceNotFound", "the flexible server was not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"value": names})
	default:
		writeErr(w, http.StatusNotFound, "NotFound",
			"no such method: "+r.Method+" "+r.URL.Path)
	}
}

func (s *Server) render(srv *fakeServer) map[string]any {
	network := map[string]any{"publicNetworkAccess": "Enabled"}
	if srv.Subnet != "" {
		network = map[string]any{
			"delegatedSubnetResourceId":   srv.Subnet,
			"privateDnsZoneArmResourceId": srv.PrivateDNS,
			"publicNetworkAccess":         "Disabled",
		}
	}
	return map[string]any{
		"name":     srv.Name,
		"location": s.opts.Location,
		"sku":      map[string]string{"name": "Standard_B1ms", "tier": "Burstable"},
		"tags":     srv.Tags,
		"properties": map[string]any{
			"fullyQualifiedDomainName": "127.0.0.1",
			"state":                    "Ready",
			"version":                  "16",
			"administratorLogin":       srv.AdminLogin,
			"storage":                  map[string]any{"storageSizeGB": srv.StorageGB},
			"network":                  network,
		},
	}
}

func (s *Server) list(w http.ResponseWriter) {
	s.mu.Lock()
	value := make([]map[string]any, 0, len(s.servers))
	for _, srv := range s.servers {
		value = append(value, s.render(srv))
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"value": value})
}

func (s *Server) get(w http.ResponseWriter, name string) {
	s.mu.Lock()
	srv, ok := s.servers[name]
	var body map[string]any
	if ok {
		body = s.render(srv)
	}
	s.mu.Unlock()
	if !ok {
		writeErr(w, http.StatusNotFound, "ResourceNotFound",
			"the flexible server "+name+" was not found")
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// restore serves a point in time restore.
//
// It models the three Azure behaviours the package comment names: the restored
// server inherits the source's password with a separate local login, starts with
// NO firewall rules, and it is an independent copy whose bytes were moved.
func (s *Server) restore(w http.ResponseWriter, r *http.Request, name string) {
	var body struct {
		Location string `json:"location"`
		SKU      struct {
			Name string `json:"name"`
			Tier string `json:"tier"`
		} `json:"sku"`
		Tags       map[string]string `json:"tags"`
		Properties struct {
			CreateMode             string `json:"createMode"`
			SourceServerResourceID string `json:"sourceServerResourceId"`
			PointInTimeUTC         string `json:"pointInTimeUTC"`
			Network                struct {
				Subnet string `json:"delegatedSubnetResourceId"`
				DNS    string `json:"privateDnsZoneArmResourceId"`
				Public string `json:"publicNetworkAccess"`
			} `json:"network"`
		} `json:"properties"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return
	}
	if body.Properties.CreateMode != "PointInTimeRestore" {
		writeErr(w, http.StatusBadRequest, "InvalidParameterValue",
			"this fake serves only createMode PointInTimeRestore, and was asked for "+
				body.Properties.CreateMode)
		return
	}
	if body.SKU.Name != "Standard_B1ms" || body.SKU.Tier != "Burstable" {
		writeErr(w, http.StatusForbidden, "RequestDisallowedByPolicy", "the source SKU must be explicit")
		return
	}
	sourceName := body.Properties.SourceServerResourceID
	if i := strings.LastIndex(sourceName, "/"); i >= 0 {
		sourceName = sourceName[i+1:]
	}

	s.mu.Lock()
	src, ok := s.servers[sourceName]
	if !ok {
		s.mu.Unlock()
		writeErr(w, http.StatusNotFound, "ResourceNotFound",
			"the source flexible server "+sourceName+" was not found")
		return
	}
	if _, exists := s.servers[name]; exists {
		s.mu.Unlock()
		writeErr(w, http.StatusConflict, "ServerAlreadyExists",
			"the flexible server "+name+" already exists")
		return
	}
	// Microsoft: a restore cannot cross between public and private access.
	if src.Subnet != body.Properties.Network.Subnet || src.PrivateDNS != body.Properties.Network.DNS ||
		(src.Subnet != "" && body.Properties.Network.Public != "Disabled") {
		s.mu.Unlock()
		writeErr(w, http.StatusBadRequest, "InvalidParameterValue",
			"restore across public and private access is not supported")
		return
	}
	database := s.databaseFor(name)
	if err := s.copyDatabase(src.Database, database); err != nil {
		s.mu.Unlock()
		writeErr(w, http.StatusInternalServerError, "InternalServerError", err.Error())
		return
	}
	tags := map[string]string{}
	for k, v := range body.Tags {
		tags[k] = v
	}
	s.servers[name] = &fakeServer{
		Subnet:     body.Properties.Network.Subnet,
		PrivateDNS: body.Properties.Network.DNS,
		Name:       name,
		Database:   database,
		Tags:       tags,
		// Inherited from the source, which is the behaviour the provider's
		// password reset exists to undo.
		AdminLogin:    s.roleFor(name),
		AdminPassword: src.AdminPassword,
		StorageGB:     src.StorageGB,
		// EMPTY. Azure does not copy firewall rules across a restore.
		FirewallRules: map[string][2]string{},
	}
	s.restores++
	op := s.nextOp()
	s.mu.Unlock()

	w.Header().Set("Azure-AsyncOperation", "/operations/"+op)
	writeJSON(w, http.StatusCreated, nil)
}

func (s *Server) patch(w http.ResponseWriter, r *http.Request, name string) {
	var body struct {
		Tags       map[string]string `json:"tags"`
		Properties struct {
			AdministratorLoginPassword string `json:"administratorLoginPassword"`
		} `json:"properties"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return
	}
	s.mu.Lock()
	srv, ok := s.servers[name]
	if !ok {
		s.mu.Unlock()
		writeErr(w, http.StatusNotFound, "ResourceNotFound",
			"the flexible server "+name+" was not found")
		return
	}
	if body.Properties.AdministratorLoginPassword != "" {
		if err := s.ensureRole(srv.AdminLogin, body.Properties.AdministratorLoginPassword); err != nil {
			s.mu.Unlock()
			writeErr(w, http.StatusInternalServerError, "InternalServerError", err.Error())
			return
		}
		srv.AdminPassword = body.Properties.AdministratorLoginPassword
	}
	if body.Tags != nil {
		srv.Tags = body.Tags
	}
	op := s.nextOp()
	s.mu.Unlock()
	w.Header().Set("Azure-AsyncOperation", "/operations/"+op)
	writeJSON(w, http.StatusOK, nil)
}

// delete removes a server AND its backups, which Microsoft states plainly and
// which is modelled here because there is no recovery from it.
func (s *Server) delete(w http.ResponseWriter, name string) {
	s.mu.Lock()
	srv, ok := s.servers[name]
	if !ok {
		s.mu.Unlock()
		writeErr(w, http.StatusNotFound, "ResourceNotFound",
			"the flexible server "+name+" was not found")
		return
	}
	delete(s.servers, name)
	database := srv.Database
	op := s.nextOp()
	s.mu.Unlock()

	if _, err := s.admin.Exec(`DROP DATABASE IF EXISTS ` + quoteIdent(database) + ` WITH (FORCE)`); err != nil {
		writeErr(w, http.StatusInternalServerError, "InternalServerError", err.Error())
		return
	}
	if _, err := s.admin.Exec(`DROP ROLE IF EXISTS ` + quoteIdent(srv.AdminLogin)); err != nil {
		writeErr(w, http.StatusInternalServerError, "InternalServerError", err.Error())
		return
	}
	w.Header().Set("Azure-AsyncOperation", "/operations/"+op)
	writeJSON(w, http.StatusOK, nil)
}

func (s *Server) putFirewall(w http.ResponseWriter, r *http.Request, name, rule string) {
	var body struct {
		Properties struct {
			StartIPAddress string `json:"startIpAddress"`
			EndIPAddress   string `json:"endIpAddress"`
		} `json:"properties"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return
	}
	if body.Properties.StartIPAddress == "" || body.Properties.EndIPAddress == "" {
		writeErr(w, http.StatusBadRequest, "InvalidParameterValue",
			"startIpAddress and endIpAddress are both required")
		return
	}
	s.mu.Lock()
	srv, ok := s.servers[name]
	if !ok {
		s.mu.Unlock()
		writeErr(w, http.StatusNotFound, "ResourceNotFound",
			"the flexible server "+name+" was not found")
		return
	}
	srv.FirewallRules[rule] = [2]string{
		body.Properties.StartIPAddress, body.Properties.EndIPAddress,
	}
	op := s.nextOp()
	s.mu.Unlock()
	w.Header().Set("Azure-AsyncOperation", "/operations/"+op)
	writeJSON(w, http.StatusOK, nil)
}
