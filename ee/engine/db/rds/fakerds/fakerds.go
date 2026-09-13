// Package fakerds is an RDS control plane that answers on localhost, backed by
// a real Postgres.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// It exists because of one line in section 10 of the plan: NO TEST MAY NEED A
// CLOUD ACCOUNT. The RDS provider's claim is about what AWS does with a
// snapshot and a restore, and the way to check the provider without an AWS
// account is to keep every line of the provider and replace only the thing on
// the other end of the socket.
//
// So the control plane is fake and the DATA PLANE IS REAL. Each instance this
// pretends to create is an actual database on an actual Postgres, a snapshot
// is CREATE DATABASE ... TEMPLATE of the instance's database, and a restore is
// another one of the snapshot's. That matters because several of the
// conformance behaviours are claims about bytes: whether a branch holds the
// golden's rows, whether writing to one branch is visible in another, whether
// a destroyed branch is gone. A fake with no bytes can only agree with
// whatever it was told, and agreeing is the same thing as not checking.
//
// TWO COPIES PER BRANCH, WHICH IS THE MECHANISM RATHER THAN AN ACCIDENT. A
// snapshot in RDS is a copy of the volume into S3 and a restore hydrates a new
// volume from it, so a branch of a golden costs one copy and building the
// golden cost two more. This fake makes exactly the same number of real
// copies, which is why the copy on write behaviour in the shared suite
// measures something here: branch time genuinely grows with the data, exactly
// as the provider declares.
//
// WHAT THIS PROVES AND WHAT IT DOES NOT. It proves the provider's logic, the
// shape of every request it sends, that those requests are signed correctly for
// the right region and service, and what it does with each documented response
// and each documented fault. It does NOT prove that AWS accepts these
// requests, and it CANNOT produce a wall clock number for an RDS snapshot and
// restore, because the thing being timed would be this file: this copies half
// a gibibyte between two local databases and RDS moves it through S3 while
// provisioning an instance. Every place a number could be mistaken for one
// says so.
//
// The signature check is worth its own sentence, because it is the part most
// likely to be mistaken for circular. The ALGORITHM is proved elsewhere,
// against the worked example AWS publishes, in ee/engine/cloudauth. What is
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

	"github.com/antifailure/antifailure/ee/engine/cloudauth"
)

// Fault is one way this control plane can be broken on purpose.
//
// A conformance suite nobody has watched fail is a list of assertions that
// might all be vacuous. These are what the self test points the suite at, one
// at a time, to require that it goes red in the named behaviour. Each one is a
// thing a real control plane could plausibly do, not an arbitrary corruption:
// a restore that is not a copy, a tag that is not recorded, a delete that does
// not delete.
type Fault string

const (
	// FaultRestoreSharesItsSnapshot makes a restored instance point at the
	// snapshot's own database instead of a copy of it. It is the deepest fault
	// here: everything still answers, the endpoints differ, the branch holds
	// the right rows, and two environments are writing to one database. It is
	// also what a provider claiming copy on write would look like if the claim
	// were true, which is why the copy on write behaviour refuses it.
	FaultRestoreSharesItsSnapshot Fault = "restore-shares-its-snapshot"
	// FaultRestoreIsEmpty makes a restore a fresh empty database rather than a
	// copy. A branch then exists, connects, and holds nothing.
	FaultRestoreIsEmpty Fault = "restore-is-empty"
	// FaultTagsAreNotRecorded makes CreateDBSnapshot and
	// RestoreDBInstanceFromDBSnapshot succeed and record no tags. A golden is
	// then never published, because a golden IS a tagged snapshot.
	FaultTagsAreNotRecorded Fault = "tags-are-not-recorded"
	// FaultDeleteDoesNotDelete makes DeleteDBInstance succeed and keep the
	// instance, which is how a provider leaks an environment's database.
	FaultDeleteDoesNotDelete Fault = "delete-does-not-delete"
	// FaultPasswordIsNotRotated makes ModifyDBInstance succeed and change
	// nothing, so the credential the provider derived does not open the
	// database it was derived for.
	FaultPasswordIsNotRotated Fault = "password-is-not-rotated"
	// FaultInstanceNeverBecomesAvailable holds an instance in creating for
	// ever, which is what a subnet with no capacity looks like.
	FaultInstanceNeverBecomesAvailable Fault = "instance-never-becomes-available"
)

// AllFaults is every fault, for a self test that must not silently stop
// covering one.
func AllFaults() []Fault {
	return []Fault{
		FaultRestoreSharesItsSnapshot,
		FaultRestoreIsEmpty,
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
	Credentials cloudauth.AWSCredentials
	// Fault is the single thing broken on purpose, or empty.
	Fault Fault
	// PageSize caps how many records a describe returns before it hands back a
	// marker. Zero means one page, however many there are.
	//
	// It exists so that the provider's marker following loop can be exercised
	// with three resources instead of a hundred and one. A pagination loop
	// that is never entered is a pagination loop that has never been checked,
	// and an inventory that stopped at the first page would report every
	// resource past it as already gone.
	PageSize int
	// RestoreCopiesSnapshotTags makes a restore carry the snapshot's tags onto
	// the new instance, and SnapshotCopiesInstanceTags makes a snapshot carry
	// its instance's tags. Neither is what AWS documents for a request that
	// names its own tags, and that is the point of modelling them: a provider
	// whose identity lives in tags has to stay right when a restore behaves
	// the way a cloud's restore did elsewhere, and a fake that can only copy
	// nothing cannot show the difference.
	RestoreCopiesSnapshotTags  TagCopy
	SnapshotCopiesInstanceTags TagCopy
}

// TagCopy is how a copied set of tags meets the tags the request named.
type TagCopy string

const (
	// TagCopyNone records only the request's tags, which is what AWS documents
	// for a request that names tags.
	TagCopyNone TagCopy = ""
	// TagCopyRequestWins merges the inherited tags underneath the request's,
	// so a key the request names keeps the request's value.
	TagCopyRequestWins TagCopy = "request-wins"
	// TagCopyInheritedWins merges the request's tags underneath the inherited
	// ones, so an inherited key overrides what the request named. No AWS
	// documentation describes this, and it is here as the worst case.
	TagCopyInheritedWins TagCopy = "inherited-wins"
)

// copyTags applies one TagCopy to a request's tags.
func copyTags(mode TagCopy, inherited, requested map[string]string) map[string]string {
	if mode == TagCopyNone {
		return requested
	}
	out := map[string]string{}
	first, second := inherited, requested
	if mode == TagCopyInheritedWins {
		first, second = requested, inherited
	}
	for k, v := range first {
		out[k] = v
	}
	for k, v := range second {
		out[k] = v
	}
	return out
}

// Server is a running fake control plane.
type Server struct {
	http   *httptest.Server
	opts   Options
	admin  *sql.DB
	region string

	mu        sync.Mutex
	instances map[string]*instance
	snapshots map[string]*snapshot
	// roles is every login role this fake created standing in for a rotated
	// master user. They are dropped at Close and NOT when the instance goes,
	// and that ordering is a bug this file already had.
	//
	// A role that connected and created a table owns it, the golden snapshot
	// is a TEMPLATE copy of the database holding it, and Postgres records the
	// ownership per database. So dropping the role when its instance went
	// failed for as long as any copy of that table existed anywhere, which is
	// for the whole life of the golden, and the failure arrived as a 500 from
	// a delete that had otherwise worked. Roles go last, after every database
	// is gone, when nothing can own anything any more.
	roles map[string]bool
	seq   int
	// calls counts requests per action, which is what the benchmark reads to
	// show what a branch costs the control plane.
	calls map[string]int
	// requests keeps the last form each action arrived with, so a test can
	// assert on what was SENT rather than only on what came back. Several
	// parameters are invisible in the response and decide real behaviour:
	// SkipFinalSnapshot, BackupRetentionPeriod and PubliclyAccessible among
	// them.
	requests map[string]url.Values
	// order is every action in the order it arrived, which is how a test shows
	// that a refresh snapshots before it restores and snapshots again after it
	// verifies.
	order []string
	// copied counts the bytes this fake copied. It is the fake's own cost and
	// it is reported precisely so that nobody mistakes it for RDS's: RDS moves
	// these bytes through S3 and this file moves them between two databases on
	// one disk.
	copied int64
	// copies waits for the background data copies.
	//
	// CreateDBSnapshot and RestoreDBInstanceFromDBSnapshot RETURN IMMEDIATELY
	// and do the work afterwards, because that is what RDS does and because a
	// fake that did it inside the handler would be a fake with a different
	// failure mode. A quarter of a gibibyte copied inside one HTTP request can
	// exceed the thirty second bound the provider puts on a single control
	// plane call, so the provider would report a timeout for a request that
	// was working, and the timeout is a production constant that is correct
	// for AWS. Doing it in the background also makes the provider's polling
	// loop load bearing rather than decorative: an operation that is complete
	// by the time the call returns lets a provider skip the wait and nobody
	// finds out until a real restore takes twelve minutes.
	copies sync.WaitGroup
	// host and port are what every instance's endpoint points at.
	host string
	port int
	// pgUser is the Postgres superuser the fake administers with.
	pgUser string
}

type instance struct {
	id            string
	status        string
	engine        string
	engineVersion string
	master        string
	password      string
	database      string
	class         string
	storageGB     int64
	created       time.Time
	tags          map[string]string
	// pending counts describes remaining before this becomes available, which
	// is how a provider's polling loop is actually exercised rather than
	// assumed.
	pending int
	// copying is set while the restore's data copy is still running in the
	// background. See the comment on Server.copies.
	copying bool
	// stuck holds the instance in creating for ever.
	stuck bool
	// deleteRefusals counts how many DeleteDBInstance calls are still refused
	// because the instance is busy. It exercises the retry a real teardown
	// needs.
	deleteRefusals int
	// The networking and authentication settings RDS would report.
	subnetGroup    string
	securityGroups []string
	iamEnabled     bool
	public         bool
}

// fixtureAccount is the account every ARN this fake renders names. It is
// twelve digits because the provider refuses an ARN whose account is not.
const fixtureAccount = "123456789012"

// The source instance's networking, which every restore is expected to reuse.
const (
	FixtureSubnetGroup   = "fixture-subnet-group"
	FixtureSecurityGroup = "sg-fixture"
)

type snapshot struct {
	id            string
	instance      string
	status        string
	engine        string
	engineVersion string
	master        string
	password      string
	database      string
	storageGB     int64
	created       time.Time
	tags          map[string]string
	// copying is set while the snapshot's data copy is still running in the
	// background. See the comment on Server.copies.
	copying bool
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
		instances: map[string]*instance{},
		snapshots: map[string]*snapshot{},
		roles:     map[string]bool{},
		calls:     map[string]int{},
		requests:  map[string]url.Values{},
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

// LastRequest is the form the named action last arrived with.
func (s *Server) LastRequest(action string) url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests[action]
}

// Actions is every action received, in order.
func (s *Server) Actions() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.order...)
}

// Reset forgets the call record without touching the instances, so a test can
// measure one operation rather than a whole run.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = map[string]int{}
	s.requests = map[string]url.Values{}
	s.order = nil
	s.copied = 0
}

// BytesCopied is what this fake copied. RDS would have moved the same bytes
// through S3 and this file moved them between two databases on one disk.
func (s *Server) BytesCopied() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.copied
}

// Close removes every database and role this created and stops the server.
//
// Best effort and loud about what it could not remove: this runs against a
// Postgres other suites share, and a fake that leaked a database per run would
// be the shape of defect this repository keeps finding in its own instruments.
func (s *Server) Close() []error {
	s.http.Close()
	// Before anything is dropped. A copy still running would either fail
	// against a database this is removing or recreate one after it went, and
	// either reads as a leak rather than as a race.
	s.copies.Wait()
	var problems []error

	s.mu.Lock()
	instances := make([]string, 0, len(s.instances))
	for id := range s.instances {
		instances = append(instances, id)
	}
	snapshots := make([]string, 0, len(s.snapshots))
	for id := range s.snapshots {
		snapshots = append(snapshots, id)
	}
	s.mu.Unlock()

	sort.Strings(instances)
	sort.Strings(snapshots)
	for _, id := range instances {
		if err := s.dropInstance(id); err != nil {
			problems = append(problems, err)
		}
	}
	for _, id := range snapshots {
		if err := s.dropSnapshot(id); err != nil {
			problems = append(problems, err)
		}
	}
	problems = append(problems, s.dropRoles()...)
	if err := s.admin.Close(); err != nil {
		problems = append(problems, err)
	}
	return problems
}

// SeedSource creates the instance a golden is built from and fills it.
//
// It is the production database, as far as the provider is concerned: it is
// never written by the provider and never read over a connection by it, only
// snapshotted. The rows put here are what every conformance behaviour reads
// back out of a branch.
func (s *Server) SeedSource(identifier, seedSQL string) error {
	return s.SeedSourceWithEngine(identifier, "postgres", "17.4", seedSQL)
}

// SeedSourceWithEngine is SeedSource for an instance that is not RDS for
// PostgreSQL, which is what a test pointing the provider at the wrong thing
// needs. Nothing else can produce that instance, and the refusal it triggers is
// the one that stops a flat cost quietly becoming a linear one.
func (s *Server) SeedSourceWithEngine(identifier, engine, version, seedSQL string) error {
	s.mu.Lock()
	database := s.nextName("db")
	s.mu.Unlock()

	if _, err := s.admin.Exec(`CREATE DATABASE ` + quoteIdent(database)); err != nil {
		return fmt.Errorf("fakerds: creating the source database: %w", err)
	}
	if err := s.openPublicSchema(database); err != nil {
		return err
	}
	if seedSQL != "" {
		conn, err := s.connect(database)
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
	s.instances[identifier] = &instance{
		id: identifier, status: "available",
		engine: engine, engineVersion: version,
		master: s.pgUser, password: s.adminPassword(),
		database: database,
		class:    "db.t4g.medium", storageGB: 20,
		created: time.Now().UTC(), tags: map[string]string{},
		subnetGroup: FixtureSubnetGroup, securityGroups: []string{FixtureSecurityGroup},
	}
	return nil
}

// SetStorage sets what an instance reports as its allocated storage.
//
// It changes ONLY the reported size, which is the input a provider would use
// if it were deciding anything by size. Nothing here pretends the bytes exist,
// and any report built on it says so.
func (s *Server) SetStorage(identifier string, gb int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in, ok := s.instances[identifier]; ok {
		in.storageGB = gb
	}
}

// DatabaseOf returns the local database backing an instance, for a test that
// wants to look at the bytes directly.
func (s *Server) DatabaseOf(identifier string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.instances[identifier]
	if !ok {
		return "", false
	}
	return in.database, true
}

// SnapshotDatabaseOf returns the local database backing a snapshot.
func (s *Server) SnapshotDatabaseOf(identifier string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok := s.snapshots[identifier]
	if !ok {
		return "", false
	}
	return snap.database, true
}

// AdminURLFor is a connection to one database as the superuser.
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

// connect opens one of this fake's databases as the administering user.
//
// The credential is the one in AdminURL, kept whole rather than rebuilt: taking
// the username out and putting it back with an empty password authenticates as
// nobody and fails with a message about the password rather than about the
// rebuild.
func (s *Server) connect(database string) (*sql.DB, error) {
	parsed, err := url.Parse(s.opts.AdminURL)
	if err != nil {
		return nil, err
	}
	parsed.Path = "/" + database
	conn, err := sql.Open("pgx", parsed.String())
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)
	return conn, nil
}

// openPublicSchema lets every role create in a database's public schema.
//
// Postgres 15 stopped granting CREATE on public to everybody, and a real RDS
// master user holds it through rds_superuser. The role this fake rotates a
// master password onto is an ordinary member of the administering role, so
// without this grant a masking step, a seed or the conformance suite's own
// writes would be refused for a reason that is about the fixture rather than
// about the provider. The database is the fixture's own, created a line
// earlier, and nothing outside it is touched.
func (s *Server) openPublicSchema(database string) error {
	conn, err := s.connect(database)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Exec(`GRANT USAGE, CREATE ON SCHEMA public TO PUBLIC`); err != nil {
		return fmt.Errorf("fakerds: opening the public schema of %s: %w", database, err)
	}
	return nil
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
	if version := body.Get("Version"); version != "2014-10-31" {
		writeFault(w, http.StatusBadRequest, "InvalidParameterValue",
			"the RDS query API version is 2014-10-31 and this request carried "+strconv.Quote(version))
		return
	}

	s.mu.Lock()
	s.calls[action]++
	s.requests[action] = body
	s.order = append(s.order, action)
	s.mu.Unlock()

	switch action {
	case "DescribeDBInstances":
		s.describeInstances(w, body)
	case "DescribeDBSnapshots":
		s.describeSnapshots(w, body)
	case "CreateDBSnapshot":
		s.createSnapshot(w, body)
	case "RestoreDBInstanceFromDBSnapshot":
		s.restore(w, body)
	case "ModifyDBInstance":
		s.modifyInstance(w, body)
	case "DeleteDBInstance":
		s.deleteInstance(w, body)
	case "DeleteDBSnapshot":
		s.deleteSnapshot(w, body)
	case "AddTagsToResource":
		s.addTagsToResource(w, body)
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
// The reasoning about what this proves is at the top of the file: the algorithm
// is proved against AWS's published example in ee/engine/cloudauth, and what is
// proved HERE is that the provider used it for this request, with this region,
// this service and this body. A provider that signed the empty string, or
// signed for us-east-1 while talking to eu-west-1, or attached a session token
// after signing, fails here and would fail identically at AWS.
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
	expected, err := cloudauth.SignV4(cloudauth.SigV4Request{
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

func (s *Server) describeInstances(w http.ResponseWriter, form url.Values) {
	identifier := form.Get("DBInstanceIdentifier")

	s.mu.Lock()
	defer s.mu.Unlock()

	if identifier != "" {
		in, ok := s.instances[identifier]
		if !ok {
			writeFault(w, http.StatusNotFound, "DBInstanceNotFound",
				"DBInstance "+identifier+" not found")
			return
		}
		s.advanceInstance(in)
		writeXML(w, describeInstancesResponse{Instances: []instanceXML{s.renderInstance(in)}})
		return
	}

	ids := make([]string, 0, len(s.instances))
	for id := range s.instances {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	page, marker := s.page(ids, form)
	out := describeInstancesResponse{Marker: marker}
	for _, id := range page {
		in := s.instances[id]
		s.advanceInstance(in)
		out.Instances = append(out.Instances, s.renderInstance(in))
	}
	writeXML(w, out)
}

func (s *Server) describeSnapshots(w http.ResponseWriter, form url.Values) {
	identifier := form.Get("DBSnapshotIdentifier")

	s.mu.Lock()
	defer s.mu.Unlock()

	if identifier != "" {
		snap, ok := s.snapshots[identifier]
		if !ok {
			writeFault(w, http.StatusNotFound, "DBSnapshotNotFound",
				"DBSnapshot "+identifier+" not found")
			return
		}
		s.advanceSnapshot(snap)
		writeXML(w, describeSnapshotsResponse{Snapshots: []snapshotXML{s.renderSnapshot(snap)}})
		return
	}
	if t := form.Get("SnapshotType"); t != "" && t != "manual" {
		// Every snapshot this fake holds was created by a CreateDBSnapshot
		// call, which is the definition of a manual one, so any other type is
		// an empty answer rather than a fault.
		writeXML(w, describeSnapshotsResponse{})
		return
	}

	ids := make([]string, 0, len(s.snapshots))
	for id := range s.snapshots {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	page, marker := s.page(ids, form)
	out := describeSnapshotsResponse{Marker: marker}
	for _, id := range page {
		snap := s.snapshots[id]
		s.advanceSnapshot(snap)
		out.Snapshots = append(out.Snapshots, s.renderSnapshot(snap))
	}
	writeXML(w, out)
}

// page applies the marker and the page size the way the RDS describes do.
//
// The marker is the identifier to resume AFTER, which is what RDS's own
// pagination means, and it is absent on the last page. A fake that always
// answered in one page would leave the provider's marker loop unentered on
// every run, and an inventory that stopped at the first page reports every
// resource past it as gone.
func (s *Server) page(ids []string, form url.Values) ([]string, string) {
	if marker := form.Get("Marker"); marker != "" {
		for i, id := range ids {
			if id == marker {
				ids = ids[i+1:]
				break
			}
		}
	}
	size := s.opts.PageSize
	if size <= 0 || len(ids) <= size {
		return ids, ""
	}
	page := ids[:size]
	return page, page[len(page)-1]
}

// advanceInstance moves an instance one step closer to available.
//
// A control plane that answered available on the first describe would let a
// provider skip its own polling loop and nobody would notice until a real
// restore took twelve minutes.
func (s *Server) advanceInstance(in *instance) {
	if in.stuck || in.copying || in.pending == 0 {
		return
	}
	in.pending--
	if in.pending == 0 {
		in.status = "available"
	}
}

// advanceSnapshot exists for symmetry and does nothing: a snapshot's only
// transition is the background copy finishing, which the copy itself records.
func (s *Server) advanceSnapshot(*snapshot) {}

func (s *Server) renderInstance(in *instance) instanceXML {
	out := instanceXML{
		Identifier: in.id, Status: in.status, Engine: in.engine,
		EngineVersion: in.engineVersion, MasterUsername: in.master,
		// DBName is the LOCAL database backing this instance, which is what
		// makes an endpoint address plus a database name reach the right
		// bytes. It is not the source's name carried forward: one Postgres
		// server stands in for every instance here, so the database name is
		// the only thing distinguishing them, and a fake that reported the
		// source's would have handed every branch a connection string
		// pointing at production.
		DBName: in.database, Class: in.class,
		AllocatedStorage: in.storageGB,
		InstanceCreateAt: in.created.Format(time.RFC3339Nano),
		Tags:             renderTags(in.tags),
		// The account and the networking are fixed values, and a restore
		// reports whatever subnet group and security groups it was asked for,
		// which is what lets a test see that the provider asked for the
		// source's rather than the account's defaults.
		ARN:                "arn:aws:rds:" + s.region + ":" + fixtureAccount + ":db:" + in.id,
		SubnetGroup:        subnetGroupXML{Name: in.subnetGroup},
		IAMEnabled:         in.iamEnabled,
		PubliclyAccessible: in.public,
	}
	for _, id := range in.securityGroups {
		out.SecurityGroups = append(out.SecurityGroups, securityGroupXML{ID: id})
	}
	// An address only once the instance is available, which is what RDS does:
	// an instance that is still creating has no endpoint, and a fake that
	// reported one would let a provider connect before the restore finished
	// and never find out that it cannot.
	if in.status == "available" {
		out.Address = s.host
		out.Port = s.port
	}
	return out
}

func (s *Server) renderSnapshot(snap *snapshot) snapshotXML {
	return snapshotXML{
		Identifier: snap.id, Instance: snap.instance, Status: snap.status,
		Engine: snap.engine, EngineVersion: snap.engineVersion,
		MasterUsername: snap.master, AllocatedStorage: snap.storageGB,
		Created: snap.created.Format(time.RFC3339Nano),
		Tags:    renderTags(snap.tags),
		ARN:     "arn:aws:rds:" + s.region + ":" + fixtureAccount + ":snapshot:" + snap.id,
	}
}

func renderTags(tags map[string]string) []tagXML {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]tagXML, 0, len(keys))
	for _, k := range keys {
		out = append(out, tagXML{Key: k, Value: tags[k]})
	}
	return out
}

// createSnapshot copies an instance's database, which is what a snapshot is.
//
// The call RETURNS FIRST and the copy runs afterwards. See the comment on
// Server.copies for why that is not an optimisation.
func (s *Server) createSnapshot(w http.ResponseWriter, form url.Values) {
	id := form.Get("DBSnapshotIdentifier")
	source := form.Get("DBInstanceIdentifier")

	s.mu.Lock()
	in, ok := s.instances[source]
	if !ok {
		writeFault(w, http.StatusNotFound, "DBInstanceNotFound",
			"DBInstance "+source+" not found")
		s.mu.Unlock()
		return
	}
	if _, exists := s.snapshots[id]; exists {
		writeFault(w, http.StatusBadRequest, "DBSnapshotAlreadyExists",
			"DBSnapshot "+id+" already exists")
		s.mu.Unlock()
		return
	}
	if in.status != "available" {
		writeFault(w, http.StatusBadRequest, "InvalidDBInstanceState",
			"DBInstance "+source+" is in state "+in.status+" and cannot be snapshotted")
		s.mu.Unlock()
		return
	}
	from := in.database
	copyInto := s.nextName("snap")
	snap := &snapshot{
		id: id, instance: source, status: "creating", copying: true,
		engine: in.engine, engineVersion: in.engineVersion,
		master: in.master, password: in.password,
		storageGB: in.storageGB, created: time.Now().UTC(),
		tags: s.tagsToRecord(form),
	}
	snap.tags = copyTags(s.opts.SnapshotCopiesInstanceTags, in.tags, snap.tags)
	s.snapshots[id] = snap
	rendered := s.renderSnapshot(snap)
	fault := s.opts.Fault
	s.copies.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.copies.Done()
		if fault == FaultRestoreSharesItsSnapshot {
			// The snapshot shares the instance's database rather than copying
			// it. It is the same shape of fault as the restore one below, one
			// level up: nothing about the control plane looks wrong and no
			// bytes moved.
			s.finishSnapshot(snap, from, 0, nil)
			return
		}
		copied, err := s.copyDatabase(from, copyInto)
		s.finishSnapshot(snap, copyInto, copied, err)
	}()

	writeXML(w, createSnapshotResponse{Snapshot: rendered})
}

// finishSnapshot records the end of a background copy.
//
// A failure becomes the status RDS itself reports, "failed", rather than an
// error nobody asked for: the call that started this has already returned, so
// the only place a provider can learn about it is the next describe, which is
// exactly where a real one learns about it too.
func (s *Server) finishSnapshot(snap *snapshot, database string, copied int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap.copying = false
	if err != nil {
		snap.status = "failed"
		return
	}
	snap.database = database
	s.copied += copied
	snap.status = "available"
}

// restore is the branch, and it is where the fault that matters most lives.
//
// The call RETURNS FIRST and the copy runs afterwards, for the reason on
// Server.copies. It is also the closest this fake gets to what a restore
// actually is: RDS answers immediately with an instance in "creating" and
// hydrates the volume behind it.
func (s *Server) restore(w http.ResponseWriter, form url.Values) {
	id := form.Get("DBInstanceIdentifier")
	from := form.Get("DBSnapshotIdentifier")

	s.mu.Lock()
	snap, ok := s.snapshots[from]
	if !ok {
		writeFault(w, http.StatusNotFound, "DBSnapshotNotFound",
			"DBSnapshot "+from+" not found")
		s.mu.Unlock()
		return
	}
	if _, exists := s.instances[id]; exists {
		writeFault(w, http.StatusBadRequest, "DBInstanceAlreadyExists",
			"DBInstance "+id+" already exists")
		s.mu.Unlock()
		return
	}
	if snap.status != "available" {
		writeFault(w, http.StatusBadRequest, "InvalidDBSnapshotState",
			"DBSnapshot "+from+" is in state "+snap.status)
		s.mu.Unlock()
		return
	}
	class := form.Get("DBInstanceClass")
	if class == "" {
		// What RDS does with an omitted class: the one the snapshot's instance
		// had. The provider deliberately omits the parameter rather than
		// sending it empty, and this is the behaviour that makes omitting it
		// correct.
		class = "db.t4g.medium"
	}
	source := snap.database
	target := s.nextName("db")
	fault := s.opts.Fault
	restored := &instance{
		id: id, status: "creating", copying: true,
		engine: snap.engine, engineVersion: snap.engineVersion,
		master: snap.master, password: snap.password,
		class: class, storageGB: snap.storageGB,
		created: time.Now().UTC(), tags: s.tagsToRecord(form),
		// One describe in creating after the copy finishes, so the provider's
		// wait always waits at least once even when the copy was instant.
		pending: 1,
		stuck:   fault == FaultInstanceNeverBecomesAvailable,
		// What RDS does with each omitted parameter: the account's default
		// subnet group and default security group, IAM authentication as the
		// snapshot had it, and a public address in a default VPC. Each default
		// is the unsafe one, so a provider that omitted a parameter shows up
		// as an instance a test can see is wrong.
		subnetGroup:    or(form.Get("DBSubnetGroupName"), "default"),
		securityGroups: securityGroupsFrom(form),
		iamEnabled:     form.Get("EnableIAMDatabaseAuthentication") != "false",
		public:         form.Get("PubliclyAccessible") != "false",
	}
	restored.tags = copyTags(s.opts.RestoreCopiesSnapshotTags, snap.tags, restored.tags)
	s.instances[id] = restored
	rendered := s.renderInstance(restored)
	s.copies.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.copies.Done()
		switch fault {
		case FaultRestoreSharesItsSnapshot:
			// Every endpoint answers, the rows are all there, and two
			// environments are writing to one database. Nothing about the
			// control plane looks wrong, and branching is suddenly free, which
			// is exactly what a provider wrongly declaring copy on write would
			// look like.
			s.finishInstance(restored, source, 0, nil)
		case FaultRestoreIsEmpty:
			_, err := s.admin.Exec(`CREATE DATABASE ` + quoteIdent(target))
			if err == nil {
				err = s.openPublicSchema(target)
			}
			s.finishInstance(restored, target, 0, err)
		default:
			// The real thing: a server side copy that shares nothing
			// afterwards, which is what hydrating a volume from a snapshot is.
			copied, err := s.copyDatabase(source, target)
			s.finishInstance(restored, target, copied, err)
		}
	}()

	writeXML(w, restoreResponse{Instance: rendered})
}

// finishInstance records the end of a background copy.
//
// A stuck instance stays in "creating" whatever the copy did, because that
// fault is about an instance that never comes up rather than about the data.
func (s *Server) finishInstance(in *instance, database string, copied int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in.copying = false
	if err != nil {
		in.status = "failed"
		return
	}
	in.database = database
	s.copied += copied
}

// copyDatabase copies one database into another, and returns how many bytes it
// moved.
func (s *Server) copyDatabase(from, to string) (int64, error) {
	var size int64
	if err := s.admin.QueryRow(`SELECT pg_database_size($1)`, from).Scan(&size); err != nil {
		return 0, err
	}
	// WITH (FORCE) is not available on CREATE, so any connection still open to
	// the template refuses the copy. The provider closes every connection it
	// opens, which is what makes this work and is worth having a check on.
	if _, err := s.admin.Exec(
		`CREATE DATABASE ` + quoteIdent(to) + ` TEMPLATE ` + quoteIdent(from)); err != nil {
		return 0, fmt.Errorf("fakerds: copying %s: %w", from, err)
	}
	return size, nil
}

// modifyInstance rotates the master password, by creating a real login role.
//
// A real restored instance's master user IS a distinct credential from the
// moment this call lands. Standing in for that with a Postgres role of the same
// name and the same password is the closest a local server gets, and it makes
// the provider's derived password actually have to be right: if it derived a
// different one, nothing would connect.
func (s *Server) modifyInstance(w http.ResponseWriter, form url.Values) {
	id := form.Get("DBInstanceIdentifier")
	password := form.Get("MasterUserPassword")

	s.mu.Lock()
	in, ok := s.instances[id]
	if !ok {
		writeFault(w, http.StatusNotFound, "DBInstanceNotFound",
			"DBInstance "+id+" not found")
		s.mu.Unlock()
		return
	}
	if form.Get("EnableIAMDatabaseAuthentication") == "false" {
		in.iamEnabled = false
	}
	fault := s.opts.Fault
	role := s.opts.Prefix + "r" + strings.ReplaceAll(id, "-", "")
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
			// CREATEROLE and pg_signal_backend because a real RDS master user
			// holds both, through rds_superuser, and the provider uses them to
			// close the logins a restore inherited and end their sessions.
			`CREATE ROLE ` + quoteIdent(role) + ` LOGIN CREATEROLE INHERIT PASSWORD ` +
				quoteLiteral(password) + ` IN ROLE pg_signal_backend, ` + quoteIdent(s.pgUser),
		}
		for _, statement := range statements {
			if _, err := s.admin.Exec(statement); err != nil {
				writeFault(w, http.StatusInternalServerError, "InternalFailure", err.Error())
				return
			}
		}
		s.mu.Lock()
		s.roles[role] = true
		in.master = role
		in.password = password
		// Modifying, and then available again. A provider that read the
		// password as in force the moment this call returned would connect
		// with a credential the instance does not have yet, which is what
		// ApplyImmediately does NOT mean.
		in.status = "modifying"
		in.pending = 1
		s.mu.Unlock()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	writeXML(w, modifyInstanceResponse{Instance: s.renderInstance(in)})
}

func (s *Server) deleteInstance(w http.ResponseWriter, form url.Values) {
	id := form.Get("DBInstanceIdentifier")

	s.mu.Lock()
	in, ok := s.instances[id]
	if !ok {
		writeFault(w, http.StatusNotFound, "DBInstanceNotFound",
			"DBInstance "+id+" not found")
		s.mu.Unlock()
		return
	}
	if in.copying {
		// What RDS answers for an instance it is still creating. A restore
		// returns before its volume exists, so a teardown that arrives in
		// that window meets this refusal, and the provider's delete retries on
		// it. Deleting here instead would drop a database that has no name yet.
		writeFault(w, http.StatusBadRequest, "InvalidDBInstanceState",
			"DBInstance "+id+" is being created and cannot be deleted yet")
		s.mu.Unlock()
		return
	}
	if in.deleteRefusals > 0 {
		in.deleteRefusals--
		writeFault(w, http.StatusBadRequest, "InvalidDBInstanceState",
			"DBInstance "+id+" is busy and cannot be deleted yet")
		s.mu.Unlock()
		return
	}
	rendered := s.renderInstance(in)
	fault := s.opts.Fault
	s.mu.Unlock()

	if fault == FaultDeleteDoesNotDelete {
		writeXML(w, deleteInstanceResponse{Instance: rendered})
		return
	}
	if err := s.dropInstance(id); err != nil {
		writeFault(w, http.StatusInternalServerError, "InternalFailure", err.Error())
		return
	}
	writeXML(w, deleteInstanceResponse{Instance: rendered})
}

func (s *Server) deleteSnapshot(w http.ResponseWriter, form url.Values) {
	id := form.Get("DBSnapshotIdentifier")

	s.mu.Lock()
	snap, ok := s.snapshots[id]
	if !ok {
		writeFault(w, http.StatusNotFound, "DBSnapshotNotFound",
			"DBSnapshot "+id+" not found")
		s.mu.Unlock()
		return
	}
	rendered := s.renderSnapshot(snap)
	fault := s.opts.Fault
	s.mu.Unlock()

	if fault == FaultDeleteDoesNotDelete {
		writeXML(w, deleteSnapshotResponse{Snapshot: rendered})
		return
	}
	if err := s.dropSnapshot(id); err != nil {
		writeFault(w, http.StatusInternalServerError, "InternalFailure", err.Error())
		return
	}
	writeXML(w, deleteSnapshotResponse{Snapshot: rendered})
}

// SetFault changes what is broken on purpose, after the server has started.
//
// It exists for one test that cannot be written any other way: proving the
// orphan sweep removes a candidate needs an orphan, an orphan needs a delete
// that did not happen, and removing it afterwards needs a delete that does.
// Those are two different control planes unless the fault can be turned off.
func (s *Server) SetFault(f Fault) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opts.Fault = f
}

// Retag replaces the tags on an instance or a snapshot.
//
// For the same test. The sweep decides by the created tag, and a resource that
// was created a moment ago is not old enough to be swept; moving its timestamp
// back is how a six hour cutoff is reached without waiting six hours.
func (s *Server) Retag(identifier string, tags map[string]string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := make(map[string]string, len(tags))
	for k, v := range tags {
		copied[k] = v
	}
	if in, ok := s.instances[identifier]; ok {
		in.tags = copied
		return true
	}
	if snap, ok := s.snapshots[identifier]; ok {
		snap.tags = copied
		return true
	}
	return false
}

// TagsOf is what a resource currently carries, so a test can move one field
// without inventing the rest.
func (s *Server) TagsOf(identifier string) (map[string]string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var from map[string]string
	if in, ok := s.instances[identifier]; ok {
		from = in.tags
	} else if snap, ok := s.snapshots[identifier]; ok {
		from = snap.tags
	} else {
		return nil, false
	}
	out := make(map[string]string, len(from))
	for k, v := range from {
		out[k] = v
	}
	return out, true
}

// Identifiers is every instance and every snapshot this fake holds, sorted, so
// a test can find an orphan without deriving its name.
func (s *Server) Identifiers() (instances, snapshots []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.instances {
		instances = append(instances, id)
	}
	for id := range s.snapshots {
		snapshots = append(snapshots, id)
	}
	sort.Strings(instances)
	sort.Strings(snapshots)
	return instances, snapshots
}

// RefuseNextDelete makes the next DeleteDBInstance for an identifier answer
// InvalidDBInstanceState, which is what a real instance does while it is still
// busy.
//
// A knob rather than a default, because a refusal on every delete would make
// every teardown in the suite twice as long, and the retry it exercises needs
// to be exercised once rather than everywhere.
func (s *Server) RefuseNextDelete(identifier string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in, ok := s.instances[identifier]; ok {
		in.deleteRefusals++
	}
}

// dropInstance removes an instance's database. The role it connected with
// outlives it; dropRoles takes those at Close.
func (s *Server) dropInstance(id string) error {
	s.mu.Lock()
	in, ok := s.instances[id]
	if !ok {
		s.mu.Unlock()
		return nil
	}
	database := in.database
	delete(s.instances, id)
	shared := s.sharesLocked(database)
	s.mu.Unlock()

	// A restore whose copy failed never got a database, and there is nothing
	// to drop for it.
	if shared || database == "" {
		return nil
	}
	if _, err := s.admin.Exec(
		`DROP DATABASE IF EXISTS ` + quoteIdent(database) + ` WITH (FORCE)`); err != nil {
		return fmt.Errorf("fakerds: dropping %s: %w", database, err)
	}
	return nil
}

// dropRoles removes the login roles this fake created.
//
// Last, and only from Close. The comment on Server.roles says why: a role that
// owns a table cannot be dropped while any COPY of the database holding it
// exists, and every golden is such a copy.
func (s *Server) dropRoles() []error {
	s.mu.Lock()
	names := make([]string, 0, len(s.roles))
	for role := range s.roles {
		names = append(names, role)
	}
	s.roles = map[string]bool{}
	s.mu.Unlock()
	sort.Strings(names)

	var problems []error
	for _, role := range names {
		if role == "" || role == s.pgUser {
			continue
		}
		if _, err := s.admin.Exec(`DROP ROLE IF EXISTS ` + quoteIdent(role)); err != nil {
			problems = append(problems, fmt.Errorf("fakerds: dropping the role %s: %w", role, err))
		}
	}
	return problems
}

// dropSnapshot removes a snapshot's database.
func (s *Server) dropSnapshot(id string) error {
	s.mu.Lock()
	snap, ok := s.snapshots[id]
	if !ok {
		s.mu.Unlock()
		return nil
	}
	database := snap.database
	delete(s.snapshots, id)
	shared := s.sharesLocked(database)
	s.mu.Unlock()

	if shared {
		return nil
	}
	if _, err := s.admin.Exec(
		`DROP DATABASE IF EXISTS ` + quoteIdent(database) + ` WITH (FORCE)`); err != nil {
		return fmt.Errorf("fakerds: dropping %s: %w", database, err)
	}
	return nil
}

// sharesLocked reports whether anything else still points at a database.
//
// Only reachable under FaultRestoreSharesItsSnapshot, and dropping the database
// out from under the thing still using it would turn one injected fault into a
// second, unrelated failure.
func (s *Server) sharesLocked(database string) bool {
	for _, other := range s.instances {
		if other.database == database {
			return true
		}
	}
	for _, other := range s.snapshots {
		if other.database == database {
			return true
		}
	}
	return false
}

// tagsToRecord reads the query API's Tags.Tag.N form, honouring the fault
// that records nothing.
func (s *Server) tagsToRecord(form url.Values) map[string]string {
	if s.opts.Fault == FaultTagsAreNotRecorded {
		return map[string]string{}
	}
	return tagsFrom(form)
}

// tagsFrom reads the query API's Tags.Tag.N form back into a map.
//
// Tags.Tag.N and ONLY that, which is what AWS's RDS model and botocore's query
// serializer produce, because TagList's member carries the locationName Tag.
// A fake that accepted the member spelling as well would agree with a provider
// sending the wrong one, and a golden here is a tagged snapshot, so that
// agreement would hide a provider that could never publish on AWS.
func tagsFrom(form url.Values) map[string]string {
	out := map[string]string{}
	for i := 1; ; i++ {
		key := form.Get("Tags.Tag." + strconv.Itoa(i) + ".Key")
		if key == "" {
			break
		}
		out[key] = form.Get("Tags.Tag." + strconv.Itoa(i) + ".Value")
	}
	return out
}

// securityGroupsFrom reads VpcSecurityGroupIds.VpcSecurityGroupId.N, and
// answers the account's default group when the request named none.
func securityGroupsFrom(form url.Values) []string {
	var out []string
	for i := 1; ; i++ {
		id := form.Get("VpcSecurityGroupIds.VpcSecurityGroupId." + strconv.Itoa(i))
		if id == "" {
			break
		}
		out = append(out, id)
	}
	if len(out) == 0 {
		return []string{"sg-default"}
	}
	return out
}

func or(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// addTagsToResource adds tags to an instance or a snapshot named by its ARN.
//
// By ARN, as AWS takes it, and refused for an ARN in another region or
// account, so a provider that built the ARN wrongly finds out here rather than
// against a real account.
func (s *Server) addTagsToResource(w http.ResponseWriter, form url.Values) {
	arn := form.Get("ResourceName")
	prefix := "arn:aws:rds:" + s.region + ":" + fixtureAccount + ":"
	s.mu.Lock()
	defer s.mu.Unlock()
	var target map[string]string
	switch {
	case strings.HasPrefix(arn, prefix+"db:"):
		if in := s.instances[strings.TrimPrefix(arn, prefix+"db:")]; in != nil {
			target = in.tags
		}
	case strings.HasPrefix(arn, prefix+"snapshot:"):
		if snap := s.snapshots[strings.TrimPrefix(arn, prefix+"snapshot:")]; snap != nil {
			target = snap.tags
		}
	}
	if target == nil {
		writeFault(w, http.StatusNotFound, "DBInstanceNotFound", "no resource with the ARN "+arn)
		return
	}
	if s.opts.Fault != FaultTagsAreNotRecorded {
		for k, v := range tagsFrom(form) {
			target[k] = v
		}
	}
	writeXML(w, addTagsResponse{})
}

// SetTags overwrites tags on an instance or a snapshot, for tests that forge
// or corrupt the metadata a provider reads.
func (s *Server) SetTags(identifier string, tags map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	target := map[string]string(nil)
	if in := s.instances[identifier]; in != nil {
		target = in.tags
	} else if snap := s.snapshots[identifier]; snap != nil {
		target = snap.tags
	}
	for k, v := range tags {
		if target != nil {
			target[k] = v
		}
	}
}

// SetEndpoint points every instance's reported address somewhere else, for a
// test that puts a real TLS listener in front of the Postgres.
func (s *Server) SetEndpoint(host string, port int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.host = host
	s.port = port
}

// SetInstanceSecurity changes what an instance reports about IAM
// authentication and public access, for tests of the refusals that read them.
func (s *Server) SetInstanceSecurity(identifier string, iamEnabled, public bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in := s.instances[identifier]; in != nil {
		in.iamEnabled = iamEnabled
		in.public = public
	}
}

// ---------------------------------------------------------------------------
// The wire shapes, which are AWS's and not ours
// ---------------------------------------------------------------------------

type describeInstancesResponse struct {
	XMLName   xml.Name      `xml:"DescribeDBInstancesResponse"`
	Marker    string        `xml:"DescribeDBInstancesResult>Marker,omitempty"`
	Instances []instanceXML `xml:"DescribeDBInstancesResult>DBInstances>DBInstance"`
}

type describeSnapshotsResponse struct {
	XMLName   xml.Name      `xml:"DescribeDBSnapshotsResponse"`
	Marker    string        `xml:"DescribeDBSnapshotsResult>Marker,omitempty"`
	Snapshots []snapshotXML `xml:"DescribeDBSnapshotsResult>DBSnapshots>DBSnapshot"`
}

type createSnapshotResponse struct {
	XMLName  xml.Name    `xml:"CreateDBSnapshotResponse"`
	Snapshot snapshotXML `xml:"CreateDBSnapshotResult>DBSnapshot"`
}

type restoreResponse struct {
	XMLName  xml.Name    `xml:"RestoreDBInstanceFromDBSnapshotResponse"`
	Instance instanceXML `xml:"RestoreDBInstanceFromDBSnapshotResult>DBInstance"`
}

type modifyInstanceResponse struct {
	XMLName  xml.Name    `xml:"ModifyDBInstanceResponse"`
	Instance instanceXML `xml:"ModifyDBInstanceResult>DBInstance"`
}

type deleteInstanceResponse struct {
	XMLName  xml.Name    `xml:"DeleteDBInstanceResponse"`
	Instance instanceXML `xml:"DeleteDBInstanceResult>DBInstance"`
}

type deleteSnapshotResponse struct {
	XMLName  xml.Name    `xml:"DeleteDBSnapshotResponse"`
	Snapshot snapshotXML `xml:"DeleteDBSnapshotResult>DBSnapshot"`
}

type addTagsResponse struct {
	XMLName xml.Name `xml:"AddTagsToResourceResponse"`
}

type subnetGroupXML struct {
	Name string `xml:"DBSubnetGroupName"`
}

type securityGroupXML struct {
	ID string `xml:"VpcSecurityGroupId"`
}

type instanceXML struct {
	ARN                string             `xml:"DBInstanceArn"`
	SubnetGroup        subnetGroupXML     `xml:"DBSubnetGroup"`
	SecurityGroups     []securityGroupXML `xml:"VpcSecurityGroups>VpcSecurityGroupMembership"`
	IAMEnabled         bool               `xml:"IAMDatabaseAuthenticationEnabled"`
	PubliclyAccessible bool               `xml:"PubliclyAccessible"`
	Identifier         string             `xml:"DBInstanceIdentifier"`
	Status             string             `xml:"DBInstanceStatus"`
	Engine             string             `xml:"Engine"`
	EngineVersion      string             `xml:"EngineVersion"`
	MasterUsername     string             `xml:"MasterUsername"`
	DBName             string             `xml:"DBName"`
	Class              string             `xml:"DBInstanceClass"`
	AllocatedStorage   int64              `xml:"AllocatedStorage"`
	InstanceCreateAt   string             `xml:"InstanceCreateTime"`
	Address            string             `xml:"Endpoint>Address,omitempty"`
	Port               int                `xml:"Endpoint>Port,omitempty"`
	Tags               []tagXML           `xml:"TagList>Tag"`
}

type snapshotXML struct {
	ARN              string   `xml:"DBSnapshotArn"`
	Identifier       string   `xml:"DBSnapshotIdentifier"`
	Instance         string   `xml:"DBInstanceIdentifier"`
	Status           string   `xml:"Status"`
	Engine           string   `xml:"Engine"`
	EngineVersion    string   `xml:"EngineVersion"`
	MasterUsername   string   `xml:"MasterUsername"`
	AllocatedStorage int64    `xml:"AllocatedStorage"`
	Created          string   `xml:"SnapshotCreateTime"`
	Tags             []tagXML `xml:"TagList>Tag"`
}

type tagXML struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
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

// quoteIdent quotes a Postgres identifier. Every name reaching it is generated
// by this file from a prefix and a counter, and it is quoted anyway, because
// "the input is ours" is the sentence that precedes every injection.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func quoteLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}
