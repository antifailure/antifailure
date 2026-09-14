// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds

// The RDS query API, which is the whole of what this provider says to AWS.
//
// Seven actions, one endpoint, one signature algorithm, and no SDK. The
// reasoning is the one written over ee/engine/cloudauth: the SDK that would
// supply this brings roughly a hundred packages into a binary that also holds
// credentials, and what it would save is a form encoded POST and an XML
// unmarshal. THE SIGNING IS NOT DUPLICATED HERE. cloudauth has the only copy,
// it is checked against the worked example AWS publishes, and a second copy
// would be a second chance to disagree with the first; the disagreement shows
// up as a 403 whose message is about a signature rather than about the line
// that produced it.
//
// The query protocol rather than the newer JSON one because RDS has only the
// query protocol. Parameters go in the body as application/x-www-form-urlencoded
// with Action and Version, lists are Name.member.1 style, and the response is
// XML. None of that is a choice this file made.

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/antifailure/antifailure/ee/engine/cloudauth"
	"github.com/antifailure/antifailure/engine/pkg/airgap"
)

// apiVersion is the RDS query API version. It is a date and it does not move;
// AWS adds parameters to an existing version rather than publishing new ones.
const apiVersion = "2014-10-31"

// callTimeout bounds one control plane call. It is not the time a restore
// takes, which is waited for by polling; it is the time one HTTP request may
// take.
const callTimeout = 30 * time.Second

// maxResponse bounds what is read back. A DescribeDBSnapshots over an account
// with a long backup history is the biggest of these and is far below this.
const maxResponse = 8 << 20

// client is the RDS control plane.
type client struct {
	region   string
	endpoint string
	chain    *cloudauth.AWSChain
	http     *http.Client

	// subnetGroup and securityGroups are the source instance's, read from it
	// at startup, and every restore is placed in them. See bindSource.
	subnetGroup    string
	securityGroups []string

	// calls counts control plane requests, which is the one number about this
	// provider's cost that is the same on every account. It is a
	// property of the provider rather than of AWS, so measuring it here is
	// honest; the seconds it turns into are not, and nothing in this package
	// pretends otherwise.
	calls atomic.Int64
}

// endpointFor is the regional RDS endpoint.
//
// Overridable, because a customer inside a VPC reaches RDS through an interface
// endpoint with a different hostname and because the conformance suite points
// this at a fake. It is signed for the configured region either way, so an
// override cannot silently move which region a request is valid in.
func endpointFor(region, override string) string {
	if override != "" {
		return strings.TrimRight(override, "/")
	}
	return "https://rds." + region + ".amazonaws.com"
}

// apiError is a refusal AWS returned, with the code it named.
//
// The code is the part that matters. "DBSnapshotNotFound" and
// "InvalidDBInstanceState" mean entirely different things to a caller, and a
// provider that only kept the message would have to match on English.
type apiError struct {
	Status  int
	Code    string
	Message string
	Action  string
}

func (e *apiError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("%s: AWS answered %d", e.Action, e.Status)
	}
	return fmt.Sprintf("%s: %s: %s", e.Action, e.Code, e.Message)
}

// The fault codes this provider makes decisions on. Every other one is
// reported as it arrived.
//
// WITHOUT the "Fault" suffix, and matched with it stripped, and that is a
// decision made under an uncertainty this repository cannot resolve.
//
// The RDS error tables publish most of these bare, as DBInstanceNotFound and
// InvalidDBInstanceState, while a handful genuinely carry the suffix on the
// wire, DBSubnetGroupNotFoundFault among them. The SDKs name every one of them
// with it, because that is the SHAPE name in the model rather than the value
// in the Code element. Nobody here has an account, so which form arrives is
// not something this package can observe, and picking one and being wrong is a
// provider that reports "AWS refused" for a golden that merely does not exist.
// So the comparison strips a trailing "Fault" from both sides and both forms
// match. That is a wider match than a real API needs and it is the direction
// the uncertainty has to fail in.
const (
	faultInstanceNotFound      = "DBInstanceNotFound"
	faultSnapshotNotFound      = "DBSnapshotNotFound"
	faultInstanceExists        = "DBInstanceAlreadyExists"
	faultSnapshotExists        = "DBSnapshotAlreadyExists"
	faultInvalidInstance       = "InvalidDBInstanceState"
	faultInvalidSnapshot       = "InvalidDBSnapshotState"
	faultInstanceQuota         = "InstanceQuotaExceeded"
	faultSnapshotQuota         = "SnapshotQuotaExceeded"
	faultStorageQuota          = "StorageQuotaExceeded"
	faultNoCapacity            = "InsufficientDBInstanceCapacity"
	faultAccessDenied          = "AccessDenied"
	faultSignatureDoesNotMatch = "SignatureDoesNotMatch"
)

// isCode reports whether err is an AWS refusal with the given fault code.
func isCode(err error, code string) bool {
	var e *apiError
	if !errors.As(err, &e) {
		return false
	}
	return normalizeFault(e.Code) == normalizeFault(code)
}

// normalizeFault removes the suffix the SDK shape names carry and the wire
// sometimes does. The comment over the fault codes says why both forms are
// accepted.
func normalizeFault(code string) string {
	return strings.TrimSuffix(code, "Fault")
}

// do signs and sends one action, and returns the response body.
func (c *client) do(ctx context.Context, action string, params url.Values) ([]byte, error) {
	creds, err := c.chain.Credentials(ctx)
	if err != nil {
		return nil, err
	}

	form := url.Values{}
	for k, v := range params {
		form[k] = v
	}
	form.Set("Action", action)
	form.Set("Version", apiVersion)
	body := []byte(encodeSorted(form))

	headers := map[string]string{
		"Content-Type": "application/x-www-form-urlencoded; charset=utf-8",
	}
	signed, err := cloudauth.SignV4(cloudauth.SigV4Request{
		Method: "POST", URL: c.endpoint, Body: body, Headers: headers,
		Region: c.region, Service: "rds", Credentials: creds, Now: time.Now().UTC(),
	})
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	for k, v := range signed {
		req.Header.Set(k, v)
	}

	c.calls.Add(1)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		// The endpoint, never the headers. An Authorization header carries a
		// signature and a key id, and a transport failure that printed the
		// request would print both.
		//
		// Uncertain rather than failed: a request whose response never
		// arrived may have been acted on, and a caller that treated it as
		// refused would either retry into a duplicate or walk away from an
		// instance that is billing. See restore in security.go.
		return nil, &uncertainResponseError{fmt.Errorf("%s: %s: %w", action, c.endpoint, unwrapURL(err))}
	}
	defer func() { _ = resp.Body.Close() }()
	read, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil, &acceptedResponseError{fmt.Errorf("%s: reading the response: %w", action, err)}
		}
		return nil, fmt.Errorf("%s: reading the response: %w", action, err)
	}
	if resp.StatusCode >= 300 {
		return nil, parseError(action, resp.StatusCode, read)
	}
	return read, nil
}

// httpClient is the client every request goes out on.
//
// Through the air gap guard, never around it. An installation sealed with
// AF_AIR_GAPPED refuses this provider at validation, and the guard is what
// makes that refusal hold even for a code path that reached here anyway: a
// default client would dial rds.amazonaws.com from inside a network whose
// operator was promised nothing leaves it.
func (c *client) httpClient() *http.Client {
	if c.http != nil {
		return c.http
	}
	return airgap.Client(airgap.SiteRDS, 0)
}

// encodeSorted encodes a form with its keys sorted.
//
// url.Values.Encode already sorts, and this exists so that the body signed and
// the body sent are produced by ONE expression rather than two that agree
// today. A signature over a different byte sequence than the one sent is a 403
// that reads as a wrong secret key, which is the failure this whole file is
// most likely to have.
func encodeSorted(form url.Values) string {
	keys := make([]string, 0, len(form))
	for k := range form {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		for _, v := range form[k] {
			if b.Len() > 0 {
				b.WriteByte('&')
			}
			b.WriteString(url.QueryEscape(k))
			b.WriteByte('=')
			b.WriteString(url.QueryEscape(v))
		}
	}
	return b.String()
}

// unwrapURL strips the *url.Error wrapper so that a transport failure reports
// the cause rather than repeating the method and the endpoint a third time.
func unwrapURL(err error) error {
	var e *url.Error
	if errors.As(err, &e) && e.Err != nil {
		return e.Err
	}
	return err
}

// parseError reads AWS's XML error document.
//
// A body that is not the documented shape still produces an apiError, with the
// status and whatever text arrived. A load balancer in front of an interface
// endpoint answers HTML, and a provider that returned "expected element type
// ErrorResponse" for that would have hidden a 503 behind an XML complaint.
func parseError(action string, status int, body []byte) error {
	var doc struct {
		XMLName xml.Name `xml:"ErrorResponse"`
		Code    string   `xml:"Error>Code"`
		Message string   `xml:"Error>Message"`
	}
	if err := xml.Unmarshal(body, &doc); err == nil && doc.Code != "" {
		return &apiError{Status: status, Code: doc.Code, Message: doc.Message, Action: action}
	}
	text := strings.TrimSpace(string(body))
	if len(text) > 400 {
		text = text[:400]
	}
	return &apiError{Status: status, Message: text, Action: action}
}

// ---------------------------------------------------------------------------
// The shapes this provider reads out of RDS
// ---------------------------------------------------------------------------

// dbInstance is one RDS DB instance, in the fields this provider uses.
//
// The shapes are AWS's published service model rather than a guess at it, and
// one of them differs from the cluster shape the aurora provider reads in a way
// that would decode silently wrong: on a DB INSTANCE, DBSubnetGroup is a
// structure holding DBSubnetGroupName, where on a DB cluster it is a plain
// string. Decoding it as a string here yields an empty subnet group and no
// error, so the refusal in bindSource is what would have caught it.
type dbInstance struct {
	ARN         string `xml:"DBInstanceArn"`
	SubnetGroup struct {
		Name string `xml:"DBSubnetGroupName"`
	} `xml:"DBSubnetGroup"`
	SecurityGroups []struct {
		ID string `xml:"VpcSecurityGroupId"`
	} `xml:"VpcSecurityGroups>VpcSecurityGroupMembership"`
	IAMEnabled         bool     `xml:"IAMDatabaseAuthenticationEnabled"`
	PubliclyAccessible bool     `xml:"PubliclyAccessible"`
	Identifier         string   `xml:"DBInstanceIdentifier"`
	Status             string   `xml:"DBInstanceStatus"`
	Engine             string   `xml:"Engine"`
	EngineVersion      string   `xml:"EngineVersion"`
	MasterUsername     string   `xml:"MasterUsername"`
	DBName             string   `xml:"DBName"`
	AllocatedStorage   int64    `xml:"AllocatedStorage"`
	InstanceCreateAt   string   `xml:"InstanceCreateTime"`
	Address            string   `xml:"Endpoint>Address"`
	Port               int      `xml:"Endpoint>Port"`
	Tags               []tagXML `xml:"TagList>Tag"`
	// PendingPassword is non empty while RDS holds a master password change it
	// has accepted and not applied. RDS reports it masked, and only its presence
	// is read: while it is there, the old credential is the one in force
	// whatever the status says. See rotation.go.
	PendingPassword string `xml:"PendingModifiedValues>MasterUserPassword"`
}

// dbSnapshot is one RDS DB snapshot, in the fields this provider uses.
type dbSnapshot struct {
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

// tagMap turns the wire's tag list into the map the provider reasons about.
func tagMap(list []tagXML) map[string]string {
	out := make(map[string]string, len(list))
	for _, t := range list {
		out[t.Key] = t.Value
	}
	return out
}

// ---------------------------------------------------------------------------
// The seven actions
// ---------------------------------------------------------------------------

// describeInstance returns one instance, and whether it exists.
//
// Missing is a value rather than an error, because every caller here has to
// distinguish "not there" from "AWS refused" and an error that had to be
// matched on a code at eight call sites would be matched wrongly at one of
// them.
func (c *client) describeInstance(ctx context.Context, id string) (dbInstance, bool, error) {
	body, err := c.do(ctx, "DescribeDBInstances", url.Values{"DBInstanceIdentifier": {id}})
	if err != nil {
		if isCode(err, faultInstanceNotFound) {
			return dbInstance{}, false, nil
		}
		return dbInstance{}, false, err
	}
	var doc struct {
		XMLName   xml.Name     `xml:"DescribeDBInstancesResponse"`
		Instances []dbInstance `xml:"DescribeDBInstancesResult>DBInstances>DBInstance"`
	}
	if err := xml.Unmarshal(body, &doc); err != nil {
		return dbInstance{}, false, fmt.Errorf("DescribeDBInstances: reading the response: %w", err)
	}
	if len(doc.Instances) == 0 {
		return dbInstance{}, false, nil
	}
	return doc.Instances[0], true, nil
}

// listInstances returns every instance in the account and region.
//
// Unfiltered, because RDS has no server side tag filter for this call: the
// Filters parameter on DescribeDBInstances takes db-instance-id, engine and a
// short list of others, and tag-key is not among them. Filtering happens here,
// on the tag list the response carries, and that is the honest place for it.
func (c *client) listInstances(ctx context.Context) ([]dbInstance, error) {
	var out []dbInstance
	marker := ""
	for {
		params := url.Values{"MaxRecords": {"100"}}
		if marker != "" {
			params.Set("Marker", marker)
		}
		body, err := c.do(ctx, "DescribeDBInstances", params)
		if err != nil {
			return nil, err
		}
		var doc struct {
			XMLName   xml.Name     `xml:"DescribeDBInstancesResponse"`
			Marker    string       `xml:"DescribeDBInstancesResult>Marker"`
			Instances []dbInstance `xml:"DescribeDBInstancesResult>DBInstances>DBInstance"`
		}
		if err := xml.Unmarshal(body, &doc); err != nil {
			return nil, fmt.Errorf("DescribeDBInstances: reading the response: %w", err)
		}
		out = append(out, doc.Instances...)
		// The marker, followed rather than ignored. An account with more than
		// a hundred instances would otherwise report a partial inventory, and
		// a partial inventory is what the leak detector compares the journal
		// against: everything past the first page would read as a resource
		// that no longer exists.
		if doc.Marker == "" {
			return out, nil
		}
		if doc.Marker == marker {
			// Refused rather than returned. A partial listing is what the
			// leak detector and the ownership checks read, and a page that
			// repeats would otherwise end the loop with every resource past
			// it reported as gone.
			return nil, fmt.Errorf("%s: the control plane repeated its pagination marker", "DescribeDB")
		}
		marker = doc.Marker
	}
}

// describeSnapshot returns one snapshot, and whether it exists.
func (c *client) describeSnapshot(ctx context.Context, id string) (dbSnapshot, bool, error) {
	body, err := c.do(ctx, "DescribeDBSnapshots", url.Values{"DBSnapshotIdentifier": {id}})
	if err != nil {
		if isCode(err, faultSnapshotNotFound) {
			return dbSnapshot{}, false, nil
		}
		return dbSnapshot{}, false, err
	}
	var doc struct {
		XMLName   xml.Name     `xml:"DescribeDBSnapshotsResponse"`
		Snapshots []dbSnapshot `xml:"DescribeDBSnapshotsResult>DBSnapshots>DBSnapshot"`
	}
	if err := xml.Unmarshal(body, &doc); err != nil {
		return dbSnapshot{}, false, fmt.Errorf("DescribeDBSnapshots: reading the response: %w", err)
	}
	if len(doc.Snapshots) == 0 {
		return dbSnapshot{}, false, nil
	}
	return doc.Snapshots[0], true, nil
}

// listSnapshots returns every MANUAL snapshot in the account and region.
//
// Manual only, and that is a filter with a reason rather than a default. RDS
// takes automated backups on its own schedule and names them rds:<instance>-
// <timestamp>; those are the customer's backups, this provider did not make
// them, and an inventory that reported them would tell the leak detector that
// the product had leaked a copy of production every night.
func (c *client) listSnapshots(ctx context.Context) ([]dbSnapshot, error) {
	var out []dbSnapshot
	marker := ""
	for {
		params := url.Values{"SnapshotType": {"manual"}, "MaxRecords": {"100"}}
		if marker != "" {
			params.Set("Marker", marker)
		}
		body, err := c.do(ctx, "DescribeDBSnapshots", params)
		if err != nil {
			return nil, err
		}
		var doc struct {
			XMLName   xml.Name     `xml:"DescribeDBSnapshotsResponse"`
			Marker    string       `xml:"DescribeDBSnapshotsResult>Marker"`
			Snapshots []dbSnapshot `xml:"DescribeDBSnapshotsResult>DBSnapshots>DBSnapshot"`
		}
		if err := xml.Unmarshal(body, &doc); err != nil {
			return nil, fmt.Errorf("DescribeDBSnapshots: reading the response: %w", err)
		}
		out = append(out, doc.Snapshots...)
		if doc.Marker == "" {
			return out, nil
		}
		if doc.Marker == marker {
			// Refused rather than returned. A partial listing is what the
			// leak detector and the ownership checks read, and a page that
			// repeats would otherwise end the loop with every resource past
			// it reported as gone.
			return nil, fmt.Errorf("%s: the control plane repeated its pagination marker", "DescribeDB")
		}
		marker = doc.Marker
	}
}

// createSnapshot takes a snapshot of an instance, tagged at creation.
//
// Tagged AT CREATION and never afterwards, which is the whole reason this
// provider needs no AddTagsToResource. Tagging a resource after the fact needs
// its ARN, an ARN carries the account number, and this provider is never told
// one; more importantly, a golden that existed for a moment before it was
// marked as a golden is a golden something could branch before it was
// verified. Here the snapshot IS the golden and it is only ever created after
// the verifier has returned, so there is no such moment.
func (c *client) createSnapshot(ctx context.Context, snapshot, instance string, tags map[string]string) (dbSnapshot, error) {
	params := url.Values{
		"DBSnapshotIdentifier": {snapshot},
		"DBInstanceIdentifier": {instance},
	}
	addTags(params, tags)
	body, err := c.do(ctx, "CreateDBSnapshot", params)
	if err != nil {
		return dbSnapshot{}, err
	}
	var doc struct {
		XMLName  xml.Name   `xml:"CreateDBSnapshotResponse"`
		Snapshot dbSnapshot `xml:"CreateDBSnapshotResult>DBSnapshot"`
	}
	if err := xml.Unmarshal(body, &doc); err != nil {
		return dbSnapshot{}, fmt.Errorf("CreateDBSnapshot: reading the response: %w", err)
	}
	return doc.Snapshot, nil
}

// restoreFromSnapshot creates an instance from a snapshot.
//
// This is the branch, and it is the call the whole provider's cost is in. A
// restore provisions a new instance and hydrates a new volume from the
// snapshot in S3, so its time is the instance's provisioning time plus the
// data, and the second term is why this provider declares CopyOnWrite false.
func (c *client) restoreFromSnapshot(ctx context.Context, instance, snapshot, class string, tags map[string]string) (dbInstance, error) {
	params := url.Values{
		"DBInstanceIdentifier": {instance},
		"DBSnapshotIdentifier": {snapshot},
		// Never reachable from the internet. A preview environment's database
		// holds masked data and masked is not public, and this is the one
		// parameter whose default AWS has changed before.
		"PubliclyAccessible": {"false"},
		// Off, because a preview database that took its own nightly backups
		// would leave snapshots behind that outlive the environment, which is
		// the exact leak this product exists to prevent.
		"BackupRetentionPeriod": {"0"},
		// Off. A restore inherits nothing about IAM authentication from the
		// request unless it is said, and an instance that accepted IAM tokens
		// would admit every principal the account grants rds-db:connect to,
		// production's included, around the derived password entirely.
		"EnableIAMDatabaseAuthentication": {"false"},
		// Production's subnet group and security groups, read from the source
		// in bindSource. Left out, RDS places the instance in the account's
		// default VPC, which is reachable from somewhere production is not.
		"DBSubnetGroupName": {c.subnetGroup},
	}
	for i, id := range c.securityGroups {
		params.Set("VpcSecurityGroupIds.VpcSecurityGroupId."+strconv.Itoa(i+1), id)
	}
	if class != "" {
		// Sent only when it was configured. RDS defaults an omitted class to
		// the one recorded in the snapshot, and an EMPTY DBInstanceClass is
		// not that default: it is a parameter with no value, which AWS
		// refuses. A provider that always sent the key would work on every
		// account that set the variable and fail on every account that did
		// not, which is the harder of the two to find.
		params.Set("DBInstanceClass", class)
	}
	addTags(params, tags)
	body, err := c.do(ctx, "RestoreDBInstanceFromDBSnapshot", params)
	if err != nil {
		return dbInstance{}, err
	}
	var doc struct {
		XMLName  xml.Name   `xml:"RestoreDBInstanceFromDBSnapshotResponse"`
		Instance dbInstance `xml:"RestoreDBInstanceFromDBSnapshotResult>DBInstance"`
	}
	if err := xml.Unmarshal(body, &doc); err != nil {
		// Accepted: the status was a success, so the instance is being
		// created, and only its description was lost.
		return dbInstance{}, &acceptedResponseError{fmt.Errorf("RestoreDBInstanceFromDBSnapshot: reading the response: %w", err)}
	}
	if doc.Instance.Identifier != instance {
		return dbInstance{}, &acceptedResponseError{fmt.Errorf("RestoreDBInstanceFromDBSnapshot: the response did not name the instance it created")}
	}
	return doc.Instance, nil
}

// rotatePassword sets an instance's master password, applied immediately.
func (c *client) rotatePassword(ctx context.Context, instance, password string) error {
	_, err := c.do(ctx, "ModifyDBInstance", url.Values{
		"DBInstanceIdentifier": {instance},
		"MasterUserPassword":   {password},
		// Immediately rather than in the maintenance window. The default is
		// the window, and a provider that took the default would hand back a
		// connection string whose password starts working next Sunday.
		"ApplyImmediately": {"true"},
		// Said again on the modify, because a snapshot taken of an instance
		// that had it enabled restores with it enabled.
		"EnableIAMDatabaseAuthentication": {"false"},
	})
	return err
}

// addTags adds tags to an existing resource, by its ARN.
//
// Used for exactly one thing: the preparation receipt on a branch instance,
// which cannot be written at creation because the instance is not prepared
// yet at creation. Every other tag is still written when its resource is
// created, for the reason createSnapshot gives.
func (c *client) addTags(ctx context.Context, arn string, tags map[string]string) error {
	if arn == "" {
		return fmt.Errorf("AddTagsToResource: the resource has no ARN")
	}
	params := url.Values{"ResourceName": {arn}}
	addTags(params, tags)
	_, err := c.do(ctx, "AddTagsToResource", params)
	return err
}

// deleteInstance removes an instance and takes no final snapshot.
//
// SkipFinalSnapshot is true and DeleteAutomatedBackups is true, and both are
// the opposite of what a production instance would want. A branch is a preview
// environment's database: a final snapshot of one is a copy of masked data
// that outlives the environment and bills for it, which is the leak this
// product exists to prevent rather than to create.
func (c *client) deleteInstance(ctx context.Context, id string) error {
	_, err := c.do(ctx, "DeleteDBInstance", url.Values{
		"DBInstanceIdentifier":   {id},
		"SkipFinalSnapshot":      {"true"},
		"DeleteAutomatedBackups": {"true"},
	})
	return err
}

// deleteSnapshot removes a snapshot.
func (c *client) deleteSnapshot(ctx context.Context, id string) error {
	_, err := c.do(ctx, "DeleteDBSnapshot", url.Values{"DBSnapshotIdentifier": {id}})
	return err
}

// addTags writes a tag map into the query API's Tags.Tag.N form.
//
// Tags.Tag.N rather than Tags.member.N, and that is AWS's model rather than a
// preference. The query protocol names a list element after the member's
// locationName when the model gives one and uses "member" only when it does
// not, and the RDS model gives TagList's member the name Tag. That is what
// botocore's QuerySerializer sends, read from its source and the RDS
// service-2.json on 2026-09-12. Whether AWS also accepts the member spelling is
// not observable without an account, and a golden IS a tagged snapshot here, so
// sending anything but what the official SDK sends would be betting the whole
// provider on that unobserved answer.
//
// Sorted, because the body is what gets signed and two encodings of one map
// would produce two signatures for one request. Go's map iteration is
// deliberately random, so an unsorted version of this would have worked on
// most runs.
func addTags(params url.Values, tags map[string]string) {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		if tags[k] == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		n := strconv.Itoa(i + 1)
		params.Set("Tags.Tag."+n+".Key", k)
		params.Set("Tags.Tag."+n+".Value", tags[k])
	}
}
