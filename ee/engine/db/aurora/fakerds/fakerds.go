// Package fakerds is an RDS control plane that answers on localhost, backed by
// a real Postgres.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// It exists because of one line in section 10 of the plan: NO TEST MAY NEED A
// CLOUD ACCOUNT. The Aurora provider's claim is about what AWS does with a
// clone, and the way to check the provider without an AWS account is to keep
// every line of the provider and replace only the thing on the other end of
// the socket.
//
// So the control plane is fake and the DATA PLANE IS REAL. Each cluster this
// pretends to create is an actual database on an actual Postgres, and a clone
// is CREATE DATABASE ... TEMPLATE, which is a real, separate database sharing
// nothing with its parent. That matters because five of the twenty four
// conformance behaviours are claims about bytes: whether a branch holds the
// golden's rows, whether writing to one branch is visible in another, whether
// a destroyed branch is gone. A fake with no bytes can only agree with
// whatever it was told, and agreeing is the same thing as not checking.
//
// WHAT THIS PROVES AND WHAT IT DOES NOT. It proves the provider's logic, the
// shape of every request it sends, that those requests are signed correctly
// for the right region and service, and what it does with each documented
// response and each documented fault. It does NOT prove that AWS accepts these
// requests, and it CANNOT produce a wall clock number for an Aurora clone,
// because the thing being timed would be this file. Every place a number could
// be mistaken for one says so.
//
// The signature check is worth its own sentence, because it is the part most
// likely to be mistaken for circular. The ALGORITHM is proved elsewhere,
// against the worked example AWS publishes, in ee/engine/awsauth. What is
// proved here is that the provider USED it correctly for this request: the
// right region, the right service, the body it actually sent, the credentials
// it actually found, and the session token inside the signature rather than
// beside it. Those are different claims and neither covers the other.
package fakerds

import (
	"database/sql"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the pgx driver

	"github.com/antifailure/antifailure/ee/engine/awsauth"
)

// Fault is one way this control plane can be broken on purpose.
//
// A conformance suite nobody has watched fail is a list of assertions that
// might all be vacuous. These are what the self test points the suite at, one
// at a time, to require that it goes red in the named behaviour. Each one is a
// thing a real control plane could plausibly do, not an arbitrary corruption:
// a clone that is not a clone, a tag that is not recorded, a delete that does
// not delete.
type Fault string

const (
	// FaultCloneSharesItsSource makes a clone point at its source's database
	// instead of a copy. It is the deepest fault here: everything still
	// answers, the endpoints differ, the branch holds the right rows, and two
	// environments are writing to one database.
	FaultCloneSharesItsSource Fault = "clone-shares-its-source"
	// FaultCloneIsEmpty makes a clone a fresh empty database rather than a
	// copy. A branch then exists, connects, and holds nothing.
	FaultCloneIsEmpty Fault = "clone-is-empty"
	// FaultTagsAreNotRecorded makes AddTagsToResource succeed and do nothing.
	// A golden is then never published, because publishing is a tag.
	FaultTagsAreNotRecorded Fault = "tags-are-not-recorded"
	// FaultDeleteDoesNotDelete makes DeleteDBCluster succeed and keep the
	// cluster, which is how a provider leaks an environment's database.
	FaultDeleteDoesNotDelete Fault = "delete-does-not-delete"
	// FaultPasswordIsNotRotated makes ModifyDBCluster succeed and change
	// nothing, so the credential the provider derived does not open the
	// database it was derived for.
	FaultPasswordIsNotRotated Fault = "password-is-not-rotated"
	// FaultInstanceNeverBecomesAvailable holds a writer instance in creating
	// for ever, which is what a subnet with no capacity looks like.
	FaultInstanceNeverBecomesAvailable Fault = "instance-never-becomes-available"
)

// AllFaults is every fault, for a self test that must not silently stop
// covering one.
func AllFaults() []Fault {
	return []Fault{
		FaultCloneSharesItsSource,
		FaultCloneIsEmpty,
		FaultTagsAreNotRecorded,
		FaultDeleteDoesNotDelete,
		FaultPasswordIsNotRotated,
		FaultInstanceNeverBecomesAvailable,
	}
}

// Options configure the fake.
type Options struct {
	// AdminURL is the Postgres this creates databases on. Required.
	AdminURL string
	// Prefix is prepended to every database and role name, so that two runs
	// against one server cannot collide.
	Prefix string
	// Region is the region requests must be signed for.
	Region string
	// Credentials are what requests must be signed with. The fake recomputes
	// the signature and refuses one that does not match.
	Credentials awsauth.Credentials
	// Fault is the single thing broken on purpose, or empty.
	Fault Fault
}

// Server is a running fake control plane.
type Server struct {
	http   *httptest.Server
	opts   Options
	admin  *sql.DB
	region string

	mu        sync.Mutex
	clusters  map[string]*cluster
	instances map[string]*instance
	seq       int
	// calls counts requests per action, which is what the benchmark reads to
	// show that a branch's control plane work does not depend on size.
	calls map[string]int
	// copied counts the bytes this fake copied making clones. It is the fake's
	// own cost and it is reported precisely so that nobody mistakes it for
	// Aurora's: Aurora copies none of these bytes and this file copies all of
	// them.
	copied int64
	// host and port are what every cluster's endpoint points at.
	host string
	port int
	// pgUser is the Postgres superuser the fake administers with.
	pgUser string
}

type cluster struct {
	id            string
	status        string
	engine        string
	engineVersion string
	master        string
	password      string
	database      string
	storageGB     int64
	created       time.Time
	tags          map[string]string
	source        string
	// pending counts describes remaining before this becomes available, which
	// is how a provider's polling loop is actually exercised rather than
	// assumed.
	pending int
	// deleteRefusals counts how many DeleteDBCluster calls are still refused
	// because an instance is on its way out. It exercises the retry a real
	// teardown needs.
	deleteRefusals int
}

type instance struct {
	id      string
	cluster string
	status  string
	pending int
	// stuck holds the instance in creating for ever.
	stuck bool
}

// New starts a fake control plane.
func New(opts Options) (*Server, error) {
	if opts.AdminURL == "" {
		return nil, fmt.Errorf("fakerds: an AdminURL is required; the data plane is real")
	}
	if opts.Region == "" {
		opts.Region = "eu-west-1"
	}
	admin, err := sql.Open("pgx", opts.AdminURL)
	if err != nil {
		return nil, err
	}
	// One connection. Everything this does is a CREATE DATABASE or a DROP
	// DATABASE, neither of which may run inside a transaction and both of
	// which want the whole server's attention rather than a pool's.
	admin.SetMaxOpenConns(1)

	parsed, err := url.Parse(opts.AdminURL)
	if err != nil {
		_ = admin.Close()
		return nil, err
	}
	port := 5432
	if raw := parsed.Port(); raw != "" {
		port, _ = strconv.Atoi(raw)
	}
	user := "postgres"
	if parsed.User != nil && parsed.User.Username() != "" {
		user = parsed.User.Username()
	}

	s := &Server{
		opts:      opts,
		admin:     admin,
		region:    opts.Region,
		clusters:  map[string]*cluster{},
		instances: map[string]*instance{},
		calls:     map[string]int{},
		host:      parsed.Hostname(),
		port:      port,
		pgUser:    user,
	}
	s.http = httptest.NewServer(http.HandlerFunc(s.serve))
	return s, nil
}

// URL is the endpoint the provider is pointed at.
func (s *Server) URL() string { return s.http.URL }

// Region is the region every request must be signed for.
func (s *Server) Region() string { return s.region }

// Calls returns the count of requests per action.
func (s *Server) Calls() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int, len(s.calls))
	for k, v := range s.calls {
		out[k] = v
	}
	return out
}

// BytesCopied is what this fake copied, which Aurora would not have.
func (s *Server) BytesCopied() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.copied
}

// Close removes every database and role this created and stops the server.
//
// Best effort and loud about what it could not remove: this runs against a
// Postgres other suites share, and a fake that leaked a database per run would
// be the thing this repository keeps finding in its own instruments.
func (s *Server) Close() []error {
	s.http.Close()
	var problems []error
	s.mu.Lock()
	names := make([]string, 0, len(s.clusters))
	for _, c := range s.clusters {
		names = append(names, c.id)
	}
	s.mu.Unlock()
	sort.Strings(names)
	for _, name := range names {
		if err := s.dropCluster(name); err != nil {
			problems = append(problems, err)
		}
	}
	if err := s.admin.Close(); err != nil {
		problems = append(problems, err)
	}
	return problems
}

// SeedSource creates the cluster a golden is cloned from and fills it.
//
// It is the production database, as far as the provider is concerned: it is
// never written by the provider and never read over a connection by it, only
// cloned. The rows put here are what every conformance behaviour reads back
// out of a branch.
func (s *Server) SeedSource(identifier, seedSQL string) error {
	s.mu.Lock()
	database := s.nextName("db")
	s.mu.Unlock()

	if _, err := s.admin.Exec(`CREATE DATABASE ` + quoteIdent(database)); err != nil {
		return fmt.Errorf("fakerds: creating the source database: %w", err)
	}
	if seedSQL != "" {
		conn, err := s.connect(database, s.pgUser, "")
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()
		if _, err := conn.Exec(seedSQL); err != nil {
			return fmt.Errorf("fakerds: seeding the source: %w", err)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.clusters[identifier] = &cluster{
		id: identifier, status: "available",
		engine: "aurora-postgresql", engineVersion: "16.4",
		master: s.pgUser, password: s.adminPassword(),
		database: database, storageGB: 1, created: time.Now().UTC(),
		tags: map[string]string{},
	}
	return nil
}

// SetSourceStorage sets what the source cluster reports as its volume size.
//
// A benchmark that wants to show a branch costing the same at a hundred rows
// and at a terabyte needs the second number to come from somewhere, and no
// laptop is going to hold a terabyte. What it changes is ONLY the reported
// size, which is exactly the input a provider would use if it were deciding
// anything by size. Nothing here pretends the bytes exist, and the report says
// so in the row it produces.
func (s *Server) SetSourceStorage(identifier string, gb int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.clusters[identifier]; ok {
		c.storageGB = gb
	}
}

// DatabaseOf returns the local database backing a cluster, for a test that
// wants to look at the bytes directly.
func (s *Server) DatabaseOf(identifier string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.clusters[identifier]
	if !ok {
		return "", false
	}
	return c.database, true
}

// AdminURLFor is a connection to one cluster's database as the superuser.
func (s *Server) AdminURLFor(database string) string {
	parsed, _ := url.Parse(s.opts.AdminURL)
	parsed.Path = "/" + database
	return parsed.String()
}

func (s *Server) adminPassword() string {
	parsed, err := url.Parse(s.opts.AdminURL)
	if err != nil || parsed.User == nil {
		return ""
	}
	password, _ := parsed.User.Password()
	return password
}

func (s *Server) connect(database, user, password string) (*sql.DB, error) {
	parsed, err := url.Parse(s.opts.AdminURL)
	if err != nil {
		return nil, err
	}
	parsed.Path = "/" + database
	if user != "" {
		parsed.User = url.UserPassword(user, password)
	}
	conn, err := sql.Open("pgx", parsed.String())
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)
	return conn, nil
}

func (s *Server) nextName(kind string) string {
	s.seq++
	return s.opts.Prefix + kind + strconv.Itoa(s.seq)
}

// ---------------------------------------------------------------------------
// The HTTP surface
// ---------------------------------------------------------------------------

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, err := readForm(r)
	if err != nil {
		writeFault(w, http.StatusBadRequest, "MalformedQueryString", err.Error())
		return
	}
	action := body.Get("Action")

	if err := s.verifySignature(r, action); err != nil {
		// The fault code AWS itself returns for a signature that does not
		// match, so the provider meets the real thing rather than a special
		// case invented here.
		writeFault(w, http.StatusForbidden, "SignatureDoesNotMatch", err.Error())
		return
	}

	s.mu.Lock()
	s.calls[action]++
	s.mu.Unlock()

	switch action {
	case "DescribeDBClusters":
		s.describeClusters(w, body)
	case "DescribeDBInstances":
		s.describeInstances(w, body)
	case "RestoreDBClusterToPointInTime":
		s.restore(w, body)
	case "CreateDBInstance":
		s.createInstance(w, body)
	case "ModifyDBCluster":
		s.modifyCluster(w, body)
	case "AddTagsToResource":
		s.addTags(w, body)
	case "DeleteDBInstance":
		s.deleteInstance(w, body)
	case "DeleteDBCluster":
		s.deleteCluster(w, body)
	default:
		writeFault(w, http.StatusBadRequest, "InvalidAction",
			"fakerds does not implement "+action+", and this provider is not supposed to send it")
	}
}

func readForm(r *http.Request) (url.Values, error) {
	if r.Method != http.MethodPost {
		return nil, fmt.Errorf("the RDS query API takes POST, not %s", r.Method)
	}
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	return r.PostForm, nil
}

// verifySignature recomputes the request's signature and compares it.
//
// The reasoning about what this proves is at the top of the file: the
// algorithm is proved against AWS's published example in ee/engine/awsauth,
// and what is proved HERE is that the provider used it for this request, with
// this region, this service and this body. A provider that signed the empty
// string, or signed for us-east-1 while talking to eu-west-1, or attached a
// session token after signing, fails here and would fail identically at AWS.
func (s *Server) verifySignature(r *http.Request, action string) error {
	authorization := r.Header.Get("Authorization")
	if authorization == "" {
		return fmt.Errorf("%s was sent unsigned", action)
	}
	if !strings.HasPrefix(authorization, "AWS4-HMAC-SHA256 ") {
		return fmt.Errorf("%s was signed with %q rather than Signature Version 4", action, authorization)
	}
	stamp := r.Header.Get("X-Amz-Date")
	signedAt, err := time.Parse("20060102T150405Z", stamp)
	if err != nil {
		return fmt.Errorf("%s carries no usable X-Amz-Date: %q", action, stamp)
	}

	scope := "/" + signedAt.Format("20060102") + "/" + s.region + "/rds/aws4_request"
	if !strings.Contains(authorization, scope) {
		return fmt.Errorf(
			"%s was signed for a different scope than %s; the credential scope in the "+
				"request is %q", action, strings.TrimPrefix(scope, "/"), authorization)
	}

	// The headers the provider chose to sign, taken from its own SignedHeaders
	// list rather than guessed, minus the four the signer adds itself.
	headers := map[string]string{}
	for _, name := range signedHeaderNames(authorization) {
		switch name {
		case "host", "x-amz-date", "x-amz-content-sha256", "x-amz-security-token":
			continue
		}
		headers[name] = r.Header.Get(name)
	}

	body, err := rawBody(r)
	if err != nil {
		return err
	}
	expected, err := awsauth.Sign(awsauth.Request{
		Method: r.Method, URL: s.http.URL + r.URL.RequestURI(), Body: body,
		Headers: headers, Region: s.region, Service: "rds",
		Credentials: s.opts.Credentials, Now: signedAt,
	})
	if err != nil {
		return err
	}
	if expected["Authorization"] != authorization {
		return fmt.Errorf(
			"the signature on %s does not match the one these credentials produce for "+
				"this body, region and service", action)
	}
	return nil
}

// rawBody rebuilds the bytes that were signed.
//
// ParseForm has already consumed the body, and re-encoding the parsed form is
// the only way back to it. url.Values.Encode sorts, and the provider's own
// encoder sorts, which is why these agree; a provider that sent an unsorted
// body would fail here and would fail at AWS for the same reason, because the
// canonical request is over the bytes and not over the parsed form.
func rawBody(r *http.Request) ([]byte, error) {
	return []byte(r.PostForm.Encode()), nil
}

func signedHeaderNames(authorization string) []string {
	_, after, found := strings.Cut(authorization, "SignedHeaders=")
	if !found {
		return nil
	}
	list, _, _ := strings.Cut(after, ",")
	return strings.Split(strings.TrimSpace(list), ";")
}

// ---------------------------------------------------------------------------
// The actions
// ---------------------------------------------------------------------------

func (s *Server) describeClusters(w http.ResponseWriter, form url.Values) {
	identifier := form.Get("DBClusterIdentifier")

	s.mu.Lock()
	defer s.mu.Unlock()

	if identifier != "" {
		c, ok := s.clusters[identifier]
		if !ok {
			s.faultLocked(w, http.StatusNotFound, "DBClusterNotFoundFault",
				"DBCluster "+identifier+" not found")
			return
		}
		s.advance(c)
		writeXML(w, describeClustersResponse{Clusters: []clusterXML{s.render(c)}})
		return
	}

	ids := make([]string, 0, len(s.clusters))
	for id := range s.clusters {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := describeClustersResponse{}
	for _, id := range ids {
		c := s.clusters[id]
		s.advance(c)
		out.Clusters = append(out.Clusters, s.render(c))
	}
	writeXML(w, out)
}

// advance moves a cluster one step closer to available.
//
// A control plane that answered available on the first describe would let a
// provider skip its own polling loop and nobody would notice until a real
// clone took ninety seconds.
func (s *Server) advance(c *cluster) {
	if c.pending > 0 {
		c.pending--
		if c.pending == 0 {
			c.status = "available"
		}
	}
}

func (s *Server) render(c *cluster) clusterXML {
	out := clusterXML{
		Identifier: c.id, Status: c.status, Engine: c.engine,
		EngineVersion: c.engineVersion, MasterUsername: c.master,
		DatabaseName: c.database, Port: s.port,
		AllocatedStorage:  c.storageGB,
		ClusterCreateTime: c.created.Format(time.RFC3339Nano),
	}
	if c.status == "available" {
		out.Endpoint = s.host
		out.ReaderEndpoint = s.host
	}
	for _, in := range s.instancesOf(c.id) {
		out.Members = append(out.Members, memberXML{Instance: in.id, Writer: true})
	}
	keys := make([]string, 0, len(c.tags))
	for k := range c.tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out.Tags = append(out.Tags, tagXML{Key: k, Value: c.tags[k]})
	}
	return out
}

func (s *Server) instancesOf(cluster string) []*instance {
	var out []*instance
	ids := make([]string, 0, len(s.instances))
	for id := range s.instances {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if s.instances[id].cluster == cluster {
			out = append(out, s.instances[id])
		}
	}
	return out
}

func (s *Server) describeInstances(w http.ResponseWriter, form url.Values) {
	identifier := form.Get("DBInstanceIdentifier")
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.instances[identifier]
	if !ok {
		s.faultLocked(w, http.StatusNotFound, "DBInstanceNotFoundFault",
			"DBInstance "+identifier+" not found")
		return
	}
	if !in.stuck && in.pending > 0 {
		in.pending--
		if in.pending == 0 {
			in.status = "available"
		}
	}
	writeXML(w, describeInstancesResponse{Instances: []instanceXML{
		{Identifier: in.id, Status: in.status, Cluster: in.cluster},
	}})
}

// restore is the clone, and it is where the fault that matters most lives.
func (s *Server) restore(w http.ResponseWriter, form url.Values) {
	source := form.Get("SourceDBClusterIdentifier")
	target := form.Get("DBClusterIdentifier")
	restoreType := form.Get("RestoreType")

	if restoreType != "copy-on-write" {
		// Refused rather than tolerated. This provider's whole claim is that a
		// branch is a clone, and a full-copy restore reaching AWS from here
		// would be the claim quietly becoming false. A test cannot see the
		// difference in the result, only in the request, so the check is here.
		writeFault(w, http.StatusBadRequest, "InvalidParameterValue",
			"fakerds refuses RestoreType="+restoreType+
				"; the aurora provider must send copy-on-write and nothing else")
		return
	}

	s.mu.Lock()
	parent, ok := s.clusters[source]
	if !ok {
		s.faultLocked(w, http.StatusNotFound, "DBClusterNotFoundFault",
			"DBCluster "+source+" not found")
		s.mu.Unlock()
		return
	}
	if _, exists := s.clusters[target]; exists {
		s.faultLocked(w, http.StatusBadRequest, "DBClusterAlreadyExistsFault",
			"DBCluster "+target+" already exists")
		s.mu.Unlock()
		return
	}
	parentDatabase := parent.database
	parentStorage := parent.storageGB
	parentMaster := parent.master
	parentPassword := parent.password
	database := s.nextName("db")
	fault := s.opts.Fault
	s.mu.Unlock()

	switch fault {
	case FaultCloneSharesItsSource:
		// Every endpoint answers, the rows are all there, and two environments
		// are writing to one database. Nothing about the control plane looks
		// wrong.
		database = parentDatabase
	case FaultCloneIsEmpty:
		if _, err := s.admin.Exec(`CREATE DATABASE ` + quoteIdent(database)); err != nil {
			writeFault(w, http.StatusInternalServerError, "InternalFailure", err.Error())
			return
		}
	default:
		// The real thing: a server side copy that shares nothing afterwards.
		// It is the opposite of what Aurora does, and that is the honest
		// position for a fake to be in. Aurora copies nothing and diverges
		// lazily; this copies everything up front. Both produce a branch that
		// is isolated, which is the property the conformance suite checks, and
		// only one of them is flat, which is why the flat claim is measured
		// from the provider's own work and not from this file's clock.
		copied, err := s.copyDatabase(parentDatabase, database)
		if err != nil {
			writeFault(w, http.StatusInternalServerError, "InternalFailure", err.Error())
			return
		}
		s.mu.Lock()
		s.copied += copied
		s.mu.Unlock()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.clusters[target] = &cluster{
		id: target, status: "creating",
		engine: parent.engine, engineVersion: parent.engineVersion,
		master: parentMaster, password: parentPassword,
		database: database, storageGB: parentStorage,
		created: time.Now().UTC(), tags: tagsFrom(form), source: source,
		// One describe in creating, so the provider's wait actually waits.
		pending: 1,
	}
	writeXML(w, restoreResponse{Cluster: s.render(s.clusters[target])})
}

// copyDatabase is the clone, and it returns how many bytes it moved.
func (s *Server) copyDatabase(from, to string) (int64, error) {
	var size int64
	if err := s.admin.QueryRow(`SELECT pg_database_size($1)`, from).Scan(&size); err != nil {
		return 0, err
	}
	// WITH (FORCE) is not available on CREATE, so any connection still open to
	// the template refuses the copy. The provider closes every connection it
	// opens, which is what makes this work and is worth having a check on.
	_, err := s.admin.Exec(
		`CREATE DATABASE ` + quoteIdent(to) + ` TEMPLATE ` + quoteIdent(from))
	if err != nil {
		return 0, fmt.Errorf("fakerds: cloning %s: %w", from, err)
	}
	return size, nil
}

func (s *Server) createInstance(w http.ResponseWriter, form url.Values) {
	identifier := form.Get("DBInstanceIdentifier")
	clusterID := form.Get("DBClusterIdentifier")

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.clusters[clusterID]; !ok {
		s.faultLocked(w, http.StatusNotFound, "DBClusterNotFoundFault",
			"DBCluster "+clusterID+" not found")
		return
	}
	if _, exists := s.instances[identifier]; exists {
		s.faultLocked(w, http.StatusBadRequest, "DBInstanceAlreadyExistsFault",
			"DBInstance "+identifier+" already exists")
		return
	}
	s.instances[identifier] = &instance{
		id: identifier, cluster: clusterID, status: "creating", pending: 1,
		stuck: s.opts.Fault == FaultInstanceNeverBecomesAvailable,
	}
	writeXML(w, createInstanceResponse{Instance: instanceXML{
		Identifier: identifier, Status: "creating", Cluster: clusterID,
	}})
}

// modifyCluster rotates the master password, by creating a real login role.
//
// A real Aurora clone's master user IS a distinct credential from the moment
// this call lands. Standing in for that with a Postgres role of the same name
// and the same password is the closest a local server gets, and it makes the
// provider's derived password actually have to be right: if it derived a
// different one, nothing would connect.
func (s *Server) modifyCluster(w http.ResponseWriter, form url.Values) {
	identifier := form.Get("DBClusterIdentifier")
	password := form.Get("MasterUserPassword")

	s.mu.Lock()
	c, ok := s.clusters[identifier]
	if !ok {
		s.faultLocked(w, http.StatusNotFound, "DBClusterNotFoundFault",
			"DBCluster "+identifier+" not found")
		s.mu.Unlock()
		return
	}
	fault := s.opts.Fault
	role := s.opts.Prefix + "r" + strings.ReplaceAll(identifier, "-", "")
	if len(role) > 60 {
		role = role[:60]
	}
	s.mu.Unlock()

	if password != "" && fault != FaultPasswordIsNotRotated {
		// A member of the owning role rather than a superuser. Membership
		// carries the owner's privileges on its objects, which is what the
		// masking and the conformance suite's own writes need, and it leaves
		// no superuser behind on a server other suites share.
		statements := []string{
			`DROP ROLE IF EXISTS ` + quoteIdent(role),
			`CREATE ROLE ` + quoteIdent(role) + ` LOGIN INHERIT PASSWORD ` +
				quoteLiteral(password) + ` IN ROLE ` + quoteIdent(s.pgUser),
		}
		for _, statement := range statements {
			if _, err := s.admin.Exec(statement); err != nil {
				writeFault(w, http.StatusInternalServerError, "InternalFailure", err.Error())
				return
			}
		}
		s.mu.Lock()
		c.master = role
		c.password = password
		c.status = "modifying"
		c.pending = 1
		s.mu.Unlock()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	writeXML(w, modifyClusterResponse{Cluster: s.render(c)})
}

func (s *Server) addTags(w http.ResponseWriter, form url.Values) {
	name := form.Get("ResourceName")
	identifier := name
	if i := strings.LastIndex(name, ":"); i >= 0 {
		identifier = name[i+1:]
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.clusters[identifier]
	if !ok {
		s.faultLocked(w, http.StatusNotFound, "DBClusterNotFoundFault",
			"DBCluster "+identifier+" not found")
		return
	}
	if s.opts.Fault != FaultTagsAreNotRecorded {
		for k, v := range tagsFrom(form) {
			if len(v) > 256 {
				s.faultLocked(w, http.StatusBadRequest, "InvalidParameterValue",
					"the tag "+k+" is longer than the 256 characters AWS allows")
				return
			}
			c.tags[k] = v
		}
	}
	writeXML(w, emptyResponse{})
}

func (s *Server) deleteInstance(w http.ResponseWriter, form url.Values) {
	identifier := form.Get("DBInstanceIdentifier")
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.instances[identifier]
	if !ok {
		s.faultLocked(w, http.StatusNotFound, "DBInstanceNotFoundFault",
			"DBInstance "+identifier+" not found")
		return
	}
	delete(s.instances, identifier)
	if c, ok := s.clusters[in.cluster]; ok {
		// One refusal on the way out, which is what a real cluster does while
		// its last instance is still detaching. Teardown that gave up on the
		// first InvalidDBClusterStateFault would leave the expensive half of
		// an environment behind, so the retry has to be exercised.
		c.deleteRefusals = 1
	}
	writeXML(w, emptyResponse{})
}

func (s *Server) deleteCluster(w http.ResponseWriter, form url.Values) {
	identifier := form.Get("DBClusterIdentifier")

	s.mu.Lock()
	c, ok := s.clusters[identifier]
	if !ok {
		s.faultLocked(w, http.StatusNotFound, "DBClusterNotFoundFault",
			"DBCluster "+identifier+" not found")
		s.mu.Unlock()
		return
	}
	if len(s.instancesOf(identifier)) > 0 {
		s.faultLocked(w, http.StatusBadRequest, "InvalidDBClusterStateFault",
			"DBCluster "+identifier+" still has instances attached")
		s.mu.Unlock()
		return
	}
	if c.deleteRefusals > 0 {
		c.deleteRefusals--
		s.faultLocked(w, http.StatusBadRequest, "InvalidDBClusterStateFault",
			"DBCluster "+identifier+" has an instance that is still deleting")
		s.mu.Unlock()
		return
	}
	rendered := s.render(c)
	fault := s.opts.Fault
	s.mu.Unlock()

	if fault == FaultDeleteDoesNotDelete {
		writeXML(w, deleteClusterResponse{Cluster: rendered})
		return
	}
	if err := s.dropCluster(identifier); err != nil {
		writeFault(w, http.StatusInternalServerError, "InternalFailure", err.Error())
		return
	}
	writeXML(w, deleteClusterResponse{Cluster: rendered})
}

// dropCluster removes a cluster's database and its login role.
func (s *Server) dropCluster(identifier string) error {
	s.mu.Lock()
	c, ok := s.clusters[identifier]
	if !ok {
		s.mu.Unlock()
		return nil
	}
	database := c.database
	role := c.master
	shared := false
	for id, other := range s.clusters {
		if id != identifier && other.database == database {
			// Only reachable under FaultCloneSharesItsSource, and dropping the
			// database out from under the cluster still using it would turn one
			// injected fault into a second, unrelated failure.
			shared = true
		}
	}
	delete(s.clusters, identifier)
	for id, in := range s.instances {
		if in.cluster == identifier {
			delete(s.instances, id)
		}
	}
	s.mu.Unlock()

	if !shared {
		if _, err := s.admin.Exec(
			`DROP DATABASE IF EXISTS ` + quoteIdent(database) + ` WITH (FORCE)`); err != nil {
			return fmt.Errorf("fakerds: dropping %s: %w", database, err)
		}
	}
	if role != "" && role != s.pgUser {
		if _, err := s.admin.Exec(`DROP ROLE IF EXISTS ` + quoteIdent(role)); err != nil {
			return fmt.Errorf("fakerds: dropping the role for %s: %w", identifier, err)
		}
	}
	return nil
}

// tagsFrom reads the query API's Tags.member.N form back into a map.
func tagsFrom(form url.Values) map[string]string {
	out := map[string]string{}
	for i := 1; ; i++ {
		key := form.Get("Tags.member." + strconv.Itoa(i) + ".Key")
		if key == "" {
			break
		}
		out[key] = form.Get("Tags.member." + strconv.Itoa(i) + ".Value")
	}
	return out
}

// ---------------------------------------------------------------------------
// The wire shapes, which are AWS's and not ours
// ---------------------------------------------------------------------------

type describeClustersResponse struct {
	XMLName  xml.Name     `xml:"DescribeDBClustersResponse"`
	Clusters []clusterXML `xml:"DescribeDBClustersResult>DBClusters>DBCluster"`
}

type restoreResponse struct {
	XMLName xml.Name   `xml:"RestoreDBClusterToPointInTimeResponse"`
	Cluster clusterXML `xml:"RestoreDBClusterToPointInTimeResult>DBCluster"`
}

type modifyClusterResponse struct {
	XMLName xml.Name   `xml:"ModifyDBClusterResponse"`
	Cluster clusterXML `xml:"ModifyDBClusterResult>DBCluster"`
}

type deleteClusterResponse struct {
	XMLName xml.Name   `xml:"DeleteDBClusterResponse"`
	Cluster clusterXML `xml:"DeleteDBClusterResult>DBCluster"`
}

type describeInstancesResponse struct {
	XMLName   xml.Name      `xml:"DescribeDBInstancesResponse"`
	Instances []instanceXML `xml:"DescribeDBInstancesResult>DBInstances>DBInstance"`
}

type createInstanceResponse struct {
	XMLName  xml.Name    `xml:"CreateDBInstanceResponse"`
	Instance instanceXML `xml:"CreateDBInstanceResult>DBInstance"`
}

type emptyResponse struct {
	XMLName xml.Name `xml:"AddTagsToResourceResponse"`
}

type clusterXML struct {
	Identifier        string      `xml:"DBClusterIdentifier"`
	Status            string      `xml:"Status"`
	Engine            string      `xml:"Engine"`
	EngineVersion     string      `xml:"EngineVersion"`
	MasterUsername    string      `xml:"MasterUsername"`
	DatabaseName      string      `xml:"DatabaseName"`
	Endpoint          string      `xml:"Endpoint,omitempty"`
	ReaderEndpoint    string      `xml:"ReaderEndpoint,omitempty"`
	Port              int         `xml:"Port"`
	AllocatedStorage  int64       `xml:"AllocatedStorage"`
	ClusterCreateTime string      `xml:"ClusterCreateTime"`
	Members           []memberXML `xml:"DBClusterMembers>DBClusterMember"`
	Tags              []tagXML    `xml:"TagList>Tag"`
}

type memberXML struct {
	Instance string `xml:"DBInstanceIdentifier"`
	Writer   bool   `xml:"IsClusterWriter"`
}

type tagXML struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

type instanceXML struct {
	Identifier string `xml:"DBInstanceIdentifier"`
	Status     string `xml:"DBInstanceStatus"`
	Cluster    string `xml:"DBClusterIdentifier"`
}

type errorResponse struct {
	XMLName xml.Name `xml:"ErrorResponse"`
	Type    string   `xml:"Error>Type"`
	Code    string   `xml:"Error>Code"`
	Message string   `xml:"Error>Message"`
}

func writeXML(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(http.StatusOK)
	_ = xml.NewEncoder(w).Encode(payload)
}

func writeFault(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(status)
	_ = xml.NewEncoder(w).Encode(errorResponse{Type: "Sender", Code: code, Message: message})
}

// faultLocked is writeFault from a path already holding the mutex. It writes
// nothing that needs the lock and exists only so the call sites read the same.
func (s *Server) faultLocked(w http.ResponseWriter, status int, code, message string) {
	writeFault(w, status, code, message)
}

// quoteIdent quotes a Postgres identifier. Every name reaching it is generated
// by this file from a prefix and a counter, and it is quoted anyway, because
// "the input is ours" is the sentence that precedes every injection.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func quoteLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}
