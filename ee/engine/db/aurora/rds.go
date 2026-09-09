// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package aurora

// The RDS query API, which is the whole of what this provider says to AWS.
//
// Eight actions, one endpoint, one signature algorithm, and no SDK. The
// reasoning is the one written over ee/engine/cloudauth: the SDK that would
// supply this brings roughly a hundred packages into a binary that also holds
// credentials, and what it would save is a form encoded POST and an XML
// unmarshal. The signing is not duplicated here; cloudauth has the only copy and
// it is checked against the example AWS publishes.
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
	"github.com/antifailure/antifailure/engine/pkg/airgap"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/antifailure/antifailure/ee/engine/cloudauth"
)

// apiVersion is the RDS query API version. It is a date and it does not move;
// AWS adds parameters to an existing version rather than publishing new ones.
const apiVersion = "2014-10-31"

// callTimeout bounds one control plane call. It is not the time a clone takes,
// which is waited for by polling; it is the time one HTTP request may take.
const callTimeout = 30 * time.Second

// maxResponse bounds what is read back. A DescribeDBClusters over a large
// account is the biggest of these and is far below this.
const maxResponse = 8 << 20

// client is the RDS control plane.
type client struct {
	region   string
	endpoint string
	chain    *cloudauth.AWSChain
	http     *http.Client

	// calls counts control plane requests, which is the measurement the
	// benchmark reports. It is a property of the provider rather than of AWS,
	// so it is honest to measure it here and it is the half of the flat branch
	// claim that can be measured without an account.
	calls atomic.Int64
}

// endpointFor is the regional RDS endpoint.
//
// Overridable, because a customer inside a VPC reaches RDS through an
// interface endpoint with a different hostname and because the conformance
// suite points this at a fake. It is signed for the configured region either
// way, so an override cannot silently move which region a request is valid in.
func endpointFor(region, override string) string {
	if override != "" {
		return strings.TrimRight(override, "/")
	}
	return "https://rds." + region + ".amazonaws.com"
}

// apiError is a refusal AWS returned, with the code it named.
//
// The code is the part that matters. "DBClusterNotFoundFault" and
// "InvalidDBClusterStateFault" mean entirely different things to a caller, and
// a provider that only kept the message would have to match on English.
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

// isCode reports whether err is an AWS refusal with the given fault code.
func isCode(err error, code string) bool {
	var e *apiError
	if !errors.As(err, &e) {
		return false
	}
	return e.Code == code
}

// The fault codes this provider makes decisions on. Every other one is
// reported as it arrived.
const (
	faultClusterNotFound    = "DBClusterNotFoundFault"
	faultInstanceNotFound   = "DBInstanceNotFoundFault"
	faultClusterExists      = "DBClusterAlreadyExistsFault"
	faultInstanceExists     = "DBInstanceAlreadyExistsFault"
	faultInvalidState       = "InvalidDBClusterStateFault"
	faultInvalidInstance    = "InvalidDBInstanceStateFault"
	faultQuotaExceeded      = "DBClusterQuotaExceededFault"
	faultSnapshotQuota      = "SnapshotQuotaExceededFault"
	faultStorageQuota       = "StorageQuotaExceededFault"
	faultAccessDenied       = "AccessDenied"
	faultInvalidRestoreTime = "InvalidRestoreFault"
)

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
		return nil, fmt.Errorf("%s: %s: %w", action, c.endpoint, unwrapURL(err))
	}
	defer func() { _ = resp.Body.Close() }()
	read, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return nil, fmt.Errorf("%s: reading the response: %w", action, err)
	}
	if resp.StatusCode >= 300 {
		return nil, parseError(action, resp.StatusCode, read)
	}
	return read, nil
}

func (c *client) httpClient() *http.Client {
	if c.http != nil {
		return c.http
	}
	// Not http.DefaultClient. The RDS control API is the outbound path by
	// which a branch is created, and the default client dials outside the
	// guard, so an air gapped installation would have reached AWS here.
	return airgap.Client(airgap.SiteAurora, 0)
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

// errorEnvelope is the shape every refusal arrives in.
type errorEnvelope struct {
	Code    string `xml:"Error>Code"`
	Message string `xml:"Error>Message"`
}

func parseError(action string, status int, body []byte) error {
	var env errorEnvelope
	if err := xml.Unmarshal(body, &env); err != nil || env.Code == "" {
		// Not the documented shape. Reported with the status rather than
		// guessed at, because an HTML error page from a proxy is a different
		// problem from a fault AWS named.
		return &apiError{Status: status, Action: action,
			Message: strings.TrimSpace(truncate(string(body), 512))}
	}
	return &apiError{Status: status, Action: action, Code: env.Code, Message: env.Message}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ---------------------------------------------------------------------------
// The eight actions
// ---------------------------------------------------------------------------

// dbCluster is what this provider reads off a cluster.
type dbCluster struct {
	Identifier     string  `xml:"DBClusterIdentifier"`
	Status         string  `xml:"Status"`
	Engine         string  `xml:"Engine"`
	EngineVersion  string  `xml:"EngineVersion"`
	MasterUsername string  `xml:"MasterUsername"`
	DatabaseName   string  `xml:"DatabaseName"`
	Endpoint       string  `xml:"Endpoint"`
	ReaderEndpoint string  `xml:"ReaderEndpoint"`
	Port           int     `xml:"Port"`
	Created        rdsTime `xml:"ClusterCreateTime"`
	// AllocatedStorage is reported in gibibytes by RDS. Aurora reports the
	// volume's size, which is what makes it the denominator of a per gigabyte
	// figure.
	AllocatedStorage int64           `xml:"AllocatedStorage"`
	Members          []clusterMember `xml:"DBClusterMembers>DBClusterMember"`
	TagList          []rdsTag        `xml:"TagList>Tag"`
}

type clusterMember struct {
	Instance string `xml:"DBInstanceIdentifier"`
	Writer   bool   `xml:"IsClusterWriter"`
}

type rdsTag struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

// tags returns the cluster's tags as a map.
func (c dbCluster) tags() map[string]string {
	out := make(map[string]string, len(c.TagList))
	for _, t := range c.TagList {
		out[t.Key] = t.Value
	}
	return out
}

// writer returns the identifier of the cluster's writer instance, or "".
func (c dbCluster) writer() string {
	for _, m := range c.Members {
		if m.Writer {
			return m.Instance
		}
	}
	// A cluster with members and no writer is a cluster mid failover. Naming
	// the first one is better than naming none, because the caller is about to
	// delete them all.
	if len(c.Members) > 0 {
		return c.Members[0].Instance
	}
	return ""
}

// rdsTime is a timestamp in the ISO 8601 form RDS returns.
type rdsTime struct{ time.Time }

func (t *rdsTime) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	var raw string
	if err := d.DecodeElement(&raw, &start); err != nil {
		return err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999Z"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			t.Time = parsed.UTC()
			return nil
		}
	}
	// A timestamp that will not parse is left zero rather than failing the
	// call. It decides a report's ordering, not whether a database exists.
	return nil
}

type dbInstance struct {
	Identifier string `xml:"DBInstanceIdentifier"`
	Status     string `xml:"DBInstanceStatus"`
	Cluster    string `xml:"DBClusterIdentifier"`
}

func (c *client) describeClusters(ctx context.Context, identifier string) ([]dbCluster, error) {
	params := url.Values{}
	if identifier != "" {
		params.Set("DBClusterIdentifier", identifier)
	}
	body, err := c.do(ctx, "DescribeDBClusters", params)
	if err != nil {
		return nil, err
	}
	var out struct {
		Clusters []dbCluster `xml:"DescribeDBClustersResult>DBClusters>DBCluster"`
		Marker   string      `xml:"DescribeDBClustersResult>Marker"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("DescribeDBClusters: %w", err)
	}
	clusters := out.Clusters
	// Paginated, and this is the one action that can exceed a page: an account
	// with more than a hundred clusters would otherwise have its inventory
	// silently truncated, and a leak detector reading a truncated inventory
	// reports resources as gone.
	for marker := out.Marker; marker != ""; {
		next := url.Values{}
		if identifier != "" {
			next.Set("DBClusterIdentifier", identifier)
		}
		next.Set("Marker", marker)
		page, err := c.do(ctx, "DescribeDBClusters", next)
		if err != nil {
			return nil, err
		}
		var more struct {
			Clusters []dbCluster `xml:"DescribeDBClustersResult>DBClusters>DBCluster"`
			Marker   string      `xml:"DescribeDBClustersResult>Marker"`
		}
		if err := xml.Unmarshal(page, &more); err != nil {
			return nil, fmt.Errorf("DescribeDBClusters: %w", err)
		}
		clusters = append(clusters, more.Clusters...)
		if more.Marker == marker {
			// A server repeating its marker would spin here for ever. Stopping
			// is the only safe answer and it is not silent: the count returned
			// is what the caller sees.
			break
		}
		marker = more.Marker
	}
	return clusters, nil
}

// describeCluster returns one cluster, or false when it does not exist.
func (c *client) describeCluster(ctx context.Context, identifier string) (dbCluster, bool, error) {
	clusters, err := c.describeClusters(ctx, identifier)
	if err != nil {
		if isCode(err, faultClusterNotFound) {
			return dbCluster{}, false, nil
		}
		return dbCluster{}, false, err
	}
	for _, cl := range clusters {
		if cl.Identifier == identifier {
			return cl, true, nil
		}
	}
	return dbCluster{}, false, nil
}

// cloneCluster is the Aurora fast clone, and it is the whole point of this
// provider.
//
// RestoreType is copy-on-write and this provider sends no other value. A clone
// shares the source's storage volume and diverges a page at a time as either
// side writes, so the call returns before any data has moved and the time it
// takes does not depend on how large the database is. The alternative RDS
// offers, full-copy, is a byte for byte copy and is what L2.3's RDS provider
// does deliberately and says so. Substituting it here when a clone was refused
// would turn a flat cost into a linear one silently, which is the exact thing
// section 10 forbids, so a refusal is returned instead.
func (c *client) cloneCluster(ctx context.Context, source, target string, tags map[string]string) (dbCluster, error) {
	params := url.Values{}
	params.Set("SourceDBClusterIdentifier", source)
	params.Set("DBClusterIdentifier", target)
	params.Set("RestoreType", "copy-on-write")
	params.Set("UseLatestRestorableTime", "true")
	addTags(params, tags)

	body, err := c.do(ctx, "RestoreDBClusterToPointInTime", params)
	if err != nil {
		return dbCluster{}, err
	}
	var out struct {
		Cluster dbCluster `xml:"RestoreDBClusterToPointInTimeResult>DBCluster"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return dbCluster{}, fmt.Errorf("RestoreDBClusterToPointInTime: %w", err)
	}
	return out.Cluster, nil
}

// createInstance adds the writer a cluster needs before anything can connect.
//
// A clone has no instances. This is the part of a branch that is NOT flat and
// not fast: the storage is there in seconds and a person cannot open a
// connection until an instance has been provisioned, which is minutes. That
// sentence belongs in the pitch rather than in a footnote, and it is why this
// provider's ExpectedBranchLatency is measured in minutes.
func (c *client) createInstance(ctx context.Context, cluster, instance, class string, tags map[string]string) error {
	params := url.Values{}
	params.Set("DBInstanceIdentifier", instance)
	params.Set("DBClusterIdentifier", cluster)
	params.Set("DBInstanceClass", class)
	params.Set("Engine", engineAuroraPostgres)
	// Never reachable from the internet. An environment's database is reached
	// from inside the VPC, and a preview database that answered the public
	// internet would be a copy of production data on an open port.
	params.Set("PubliclyAccessible", "false")
	addTags(params, tags)
	_, err := c.do(ctx, "CreateDBInstance", params)
	if err != nil && isCode(err, faultInstanceExists) {
		// Idempotent by identifier, which is what the interface requires: the
		// engine retries after a timeout and a retry that made a second
		// instance would be an orphan nothing names.
		return nil
	}
	return err
}

func (c *client) describeInstance(ctx context.Context, instance string) (dbInstance, bool, error) {
	params := url.Values{}
	params.Set("DBInstanceIdentifier", instance)
	body, err := c.do(ctx, "DescribeDBInstances", params)
	if err != nil {
		if isCode(err, faultInstanceNotFound) {
			return dbInstance{}, false, nil
		}
		return dbInstance{}, false, err
	}
	var out struct {
		Instances []dbInstance `xml:"DescribeDBInstancesResult>DBInstances>DBInstance"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return dbInstance{}, false, fmt.Errorf("DescribeDBInstances: %w", err)
	}
	for _, in := range out.Instances {
		if in.Identifier == instance {
			return in, true, nil
		}
	}
	return dbInstance{}, false, nil
}

// setMasterPassword rotates the clone's master credential.
//
// A clone inherits the source cluster's master user and its password, so
// without this every preview environment would hold production's database
// password. Rotating it means the credential a preview holds opens the preview
// and nothing else, and it costs one API call.
func (c *client) setMasterPassword(ctx context.Context, cluster string, password string) error {
	params := url.Values{}
	params.Set("DBClusterIdentifier", cluster)
	params.Set("MasterUserPassword", password)
	params.Set("ApplyImmediately", "true")
	_, err := c.do(ctx, "ModifyDBCluster", params)
	return err
}

// setTags records this provider's metadata on a cluster.
func (c *client) setTags(ctx context.Context, arn string, tags map[string]string) error {
	params := url.Values{}
	params.Set("ResourceName", arn)
	addTags(params, tags)
	_, err := c.do(ctx, "AddTagsToResource", params)
	return err
}

func (c *client) deleteInstance(ctx context.Context, instance string) error {
	params := url.Values{}
	params.Set("DBInstanceIdentifier", instance)
	params.Set("SkipFinalSnapshot", "true")
	_, err := c.do(ctx, "DeleteDBInstance", params)
	if err != nil && (isCode(err, faultInstanceNotFound) || isCode(err, faultInvalidInstance)) {
		// Already gone, or already deleting. Both are the state the caller
		// asked for, and teardown retries.
		return nil
	}
	return err
}

func (c *client) deleteCluster(ctx context.Context, cluster string) error {
	params := url.Values{}
	params.Set("DBClusterIdentifier", cluster)
	params.Set("SkipFinalSnapshot", "true")
	_, err := c.do(ctx, "DeleteDBCluster", params)
	if err != nil && isCode(err, faultClusterNotFound) {
		return nil
	}
	return err
}

// addTags writes a tag map into the query API's member form.
//
// Sorted by key, because the form is signed and a map iterated in Go's random
// order would produce a different body on every retry. That is not a
// correctness problem for AWS and it is one for anybody diffing two requests.
func addTags(params url.Values, tags map[string]string) {
	if len(tags) == 0 {
		return
	}
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		n := strconv.Itoa(i + 1)
		params.Set("Tags.member."+n+".Key", k)
		params.Set("Tags.member."+n+".Value", tags[k])
	}
}

// unwrapURL removes the *url.Error wrapper, whose Error method prints the
// whole URL including a query.
func unwrapURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return ue.Err
	}
	return err
}
