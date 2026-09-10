// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package cloudsql

// The Cloud SQL Admin API client.
//
// Only the calls this provider makes, rather than a general client, for the
// reason aurora's rds.go gives about the RDS query API: a general client is a
// surface nobody exercises, and every unexercised field is somewhere a wrong
// value can hide until an account is attached.
//
// The one design decision worth reading is fastCloneRequest. See its comment.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/antifailure/antifailure/ee/engine/cloudauth"
	"github.com/antifailure/antifailure/engine/pkg/airgap"
)

// httpDoer is the transport, narrowed to what this client uses so a fake can
// satisfy it without implementing an http.Client.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// defaultEndpoint is the public Admin API.
const defaultEndpoint = "https://sqladmin.googleapis.com"

// adminAPI is the client.
type adminAPI struct {
	endpoint string
	project  string
	client   httpDoer
	token    func(context.Context) (string, error)
}

func newAdminAPI(opts Options) (*adminAPI, error) {
	endpoint := opts.Endpoint
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("cloudsql: %s must be an HTTPS API origin without credentials, query or fragment", EndpointVariable)
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	local := host == "localhost" || (ip != nil && ip.IsLoopback())
	if parsed.Scheme != "https" && (parsed.Scheme != "http" || !local) {
		return nil, fmt.Errorf("cloudsql: %s requires HTTPS; HTTP is permitted only for a loopback API fixture", EndpointVariable)
	}
	client := opts.HTTPClient
	if client == nil {
		guarded := airgap.Client(airgap.SiteCloudSQL, 60*time.Second)
		guarded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client = guarded
	}
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	token := opts.Token
	if token == nil {
		var account *cloudauth.GCPServiceAccount
		if path := getenv("GOOGLE_APPLICATION_CREDENTIALS"); path != "" {
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("cloudsql: reading GOOGLE_APPLICATION_CREDENTIALS: %w", err)
			}
			account, err = cloudauth.ParseGCPServiceAccount(raw)
			if err != nil {
				return nil, fmt.Errorf("cloudsql: parsing GOOGLE_APPLICATION_CREDENTIALS: %w", err)
			}
		}
		token = cloudauth.NewGCPTokenSource(account, cloudauth.ScopeGoogleCloudPlatform).Token
	}
	return &adminAPI{
		endpoint: strings.TrimSuffix(endpoint, "/"),
		project:  opts.Project,
		client:   client,
		token:    token,
	}, nil
}

// instance is the subset of the Cloud SQL instance resource this provider
// reads.
//
// A subset rather than the whole resource, and the fields that are here are
// here because something reads them. Settings.UserLabels is the ownership
// predicate, Settings.ActivationPolicy is how a golden's compute is stopped,
// and the disk fields are what decide whether a clone can be fast.
type sslCert struct {
	Cert            string `json:"cert"`
	Instance        string `json:"instance"`
	SHA1Fingerprint string `json:"sha1Fingerprint"`
}

type ipConfiguration struct {
	ServerCAMode string `json:"serverCaMode"`
}

type instance struct {
	DNSName        string   `json:"dnsName"`
	ServerCACert   sslCert  `json:"serverCaCert"`
	Name           string   `json:"name"`
	Region         string   `json:"region"`
	DatabaseVer    string   `json:"databaseVersion"`
	State          string   `json:"state"`
	IPAddresses    []ipAddr `json:"ipAddresses"`
	CreateTime     time.Time
	CreateTimeRaw  string   `json:"createTime"`
	Settings       settings `json:"settings"`
	ConnectionName string   `json:"connectionName"`
}

type ipAddr struct {
	Type      string `json:"type"`
	IPAddress string `json:"ipAddress"`
}

type databaseFlag struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type settings struct {
	IPConfiguration  ipConfiguration   `json:"ipConfiguration"`
	DatabaseFlags    []databaseFlag    `json:"databaseFlags"`
	Tier             string            `json:"tier"`
	ActivationPolicy string            `json:"activationPolicy"`
	UserLabels       map[string]string `json:"userLabels"`
	DataDiskType     string            `json:"dataDiskType"`
	DataDiskSizeGb   string            `json:"dataDiskSizeGb"`
	LocationPrefs    *locationPrefs    `json:"locationPreference,omitempty"`
}

type locationPrefs struct {
	Zone string `json:"zone,omitempty"`
}

// fastCloneRequest is the ONLY clone shape this provider will send, and the
// type is the enforcement rather than a comment asking for care.
//
// Cloud SQL picks the fast workflow or the standard one from the shape of the
// request and tells you nothing about which it chose. Three fields force the
// standard path: a preferred zone, a point in time, and disk properties that do
// not match the source. So this struct HAS NO FIELD FOR ANY OF THEM. Not an
// omitempty field left unset, which the next person can set in one line while
// every test stays green, but no field at all, so that asking for a slow clone
// does not compile.
//
// The zone is the one that earns the whole design. Google states that
// re-specifying even the SAME zone falls back to the standard workflow, so the
// request that looks most careful, pinning a branch beside its golden, is
// exactly the one that stops being a fast clone. A struct with an
// omitempty PreferredZone would have been set by somebody trying to be careful,
// silently, and the copy on write claim would have become false with no test
// able to see it.
//
// json.Marshal of this type is therefore the proof, and a test asserts on the
// marshalled bytes rather than on the Go value, because it is the bytes Google
// reads.
type fastCloneRequest struct {
	CloneContext fastCloneContext `json:"cloneContext"`
}

type fastCloneContext struct {
	Kind                    string `json:"kind"`
	DestinationInstanceName string `json:"destinationInstanceName"`
}

// newFastCloneRequest builds the only clone request this provider sends.
func newFastCloneRequest(destination string) fastCloneRequest {
	return fastCloneRequest{CloneContext: fastCloneContext{
		Kind:                    "sql#cloneContext",
		DestinationInstanceName: destination,
	}}
}

// operation is the long running operation every mutating call returns.
type operation struct {
	Name   string         `json:"name"`
	Status string         `json:"status"`
	Error  *operationErrs `json:"error,omitempty"`
}

type operationErrs struct {
	Errors []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

// err renders an operation's failure, or nil.
func (o *operation) err() error {
	if o == nil || o.Error == nil || len(o.Error.Errors) == 0 {
		return nil
	}
	first := o.Error.Errors[0]
	return fmt.Errorf("cloud sql operation failed: %s: %s", first.Code, first.Message)
}

// apiError is a non 2xx answer from the Admin API.
type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("cloud sql admin api: HTTP %d: %s: %s", e.Status, e.Code, e.Message)
}

// notFound reports whether an error is the API saying the thing is not there.
//
// Separate from the message because two callers need it for opposite reasons:
// Destroy treats it as success, and Branch treats it as the golden being gone.
func notFound(err error) bool {
	var ae *apiError
	if !asAPIError(err, &ae) {
		return false
	}
	return ae.Status == http.StatusNotFound
}

func asAPIError(err error, out **apiError) bool {
	for err != nil {
		if ae, ok := err.(*apiError); ok {
			*out = ae
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// do issues one request and decodes the answer.
func (a *adminAPI) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("cloudsql: encoding %s %s: %w", method, path, err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.endpoint+path, reader)
	if err != nil {
		return fmt.Errorf("cloudsql: building %s %s: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	token, err := a.token(ctx)
	if err != nil {
		return fmt.Errorf("cloudsql: obtaining a Google identity: %w", err)
	}
	if token == "" {
		return fmt.Errorf("cloudsql: the Google identity returned an empty access token")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := a.client.Do(req)
	if err != nil {
		return &uncertainResponseError{fmt.Errorf("cloudsql: %s %s: %w", method, path, err)}
	}
	defer func() { _ = resp.Body.Close() }()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		problem := fmt.Errorf("cloudsql: reading %s %s: %w", method, path, err)
		if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
			return &acceptedResponseError{problem}
		}
		return problem
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return decodeAPIError(resp.StatusCode, payload)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return &acceptedResponseError{fmt.Errorf("cloudsql: decoding %s %s: %w", method, path, err)}
	}
	return nil
}

// decodeAPIError turns a non 2xx body into an apiError.
//
// Tolerant on this boundary on purpose: an error body that is not the documented
// shape must still produce an error a caller can read, rather than a decode
// failure that replaces the service's own sentence with a parser's.
func decodeAPIError(status int, payload []byte) error {
	var envelope struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil || envelope.Error.Message == "" {
		return &apiError{Status: status, Code: http.StatusText(status), Message: strings.TrimSpace(string(payload))}
	}
	code := envelope.Error.Status
	if code == "" {
		code = http.StatusText(status)
	}
	return &apiError{Status: status, Code: code, Message: envelope.Error.Message}
}

func (a *adminAPI) instancePath(name string) string {
	return "/v1/projects/" + url.PathEscape(a.project) + "/instances/" + url.PathEscape(name)
}

// getInstance reads one instance.
func (a *adminAPI) getInstance(ctx context.Context, name string) (*instance, error) {
	var in instance
	if err := a.do(ctx, http.MethodGet, a.instancePath(name), nil, &in); err != nil {
		return nil, err
	}
	in.CreateTime = parseTime(in.CreateTimeRaw)
	return &in, nil
}

// listInstances reads every instance in the project.
func (a *adminAPI) listInstances(ctx context.Context) ([]instance, error) {
	var page struct {
		Items         []instance `json:"items"`
		NextPageToken string     `json:"nextPageToken"`
	}
	path := "/v1/projects/" + url.PathEscape(a.project) + "/instances"
	var all []instance
	next := ""
	for {
		p := path
		if next != "" {
			p += "?pageToken=" + url.QueryEscape(next)
		}
		page.Items = nil
		page.NextPageToken = ""
		if err := a.do(ctx, http.MethodGet, p, nil, &page); err != nil {
			return nil, err
		}
		for i := range page.Items {
			page.Items[i].CreateTime = parseTime(page.Items[i].CreateTimeRaw)
			all = append(all, page.Items[i])
		}
		if page.NextPageToken == "" {
			return all, nil
		}
		next = page.NextPageToken
	}
}

// database is one entry from an instance's databases collection.
type database struct {
	Name string `json:"name"`
}

// listDatabases reads the databases on an instance.
//
// A real Admin API call rather than an assumption that the database is called
// postgres. A Cloud SQL clone carries every database the source had, and which
// of them holds the application is not something this provider may guess: a
// caller whose data lives in a database called "app" would otherwise get a
// connection string pointing at an empty maintenance database and a branch that
// reports healthy while holding none of their rows.
func (a *adminAPI) listDatabases(ctx context.Context, instance string) ([]database, error) {
	var page struct {
		Items []database `json:"items"`
	}
	if err := a.do(ctx, http.MethodGet,
		a.instancePath(instance)+"/databases", nil, &page); err != nil {
		return nil, err
	}
	return page.Items, nil
}

// user is one entry from an instance's users collection.
type user struct {
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	DatabaseRoles []string `json:"databaseRoles"`
}

// listUsers reads the users on an instance.
//
// A real Admin API call rather than an assumption that the administrator is
// called postgres. Cloud SQL's built in PostgreSQL user usually IS postgres,
// and an instance can carry others; assuming the name would make this provider
// set a password on a user that may not be the one a branch is reached with,
// and the failure would arrive as an authentication error against a server that
// provisioned perfectly.
func (a *adminAPI) listUsers(ctx context.Context, instance string) ([]user, error) {
	var page struct {
		Items []user `json:"items"`
	}
	if err := a.do(ctx, http.MethodGet,
		a.instancePath(instance)+"/users", nil, &page); err != nil {
		return nil, err
	}
	return page.Items, nil
}

// clone issues the fast clone and returns the operation.
func (a *adminAPI) clone(ctx context.Context, source, destination string) (*operation, error) {
	var op operation
	body := newFastCloneRequest(destination)
	if err := a.do(ctx, http.MethodPost, a.instancePath(source)+"/clone", body, &op); err != nil {
		return nil, err
	}
	return &op, nil
}

// deleteInstance removes an instance.
func (a *adminAPI) deleteInstance(ctx context.Context, name string) (*operation, error) {
	var op operation
	if err := a.do(ctx, http.MethodDelete, a.instancePath(name), nil, &op); err != nil {
		return nil, err
	}
	return &op, nil
}

// patchInstance changes settings on an instance.
func (a *adminAPI) patchInstance(ctx context.Context, name string, body any) (*operation, error) {
	var op operation
	if err := a.do(ctx, http.MethodPatch, a.instancePath(name), body, &op); err != nil {
		return nil, err
	}
	return &op, nil
}

// setPassword sets a user's password on an instance.
func (a *adminAPI) setPassword(ctx context.Context, name, user, password string) (*operation, error) {
	var op operation
	body := map[string]any{"name": user, "password": password}
	path := a.instancePath(name) + "/users?name=" + url.QueryEscape(user)
	if err := a.do(ctx, http.MethodPut, path, body, &op); err != nil {
		return nil, err
	}
	return &op, nil
}

// waitForOperation polls until the operation is DONE or ctx ends.
//
// It polls rather than sleeping a fixed time, and it reports the operation's
// own error rather than a timeout, because "the clone failed because the source
// was not ready" and "this provider gave up waiting" are different facts and a
// caller acts differently on each.
func (a *adminAPI) waitForOperation(ctx context.Context, op *operation, poll time.Duration) error {
	if op == nil {
		return nil
	}
	if err := op.err(); err != nil {
		return err
	}
	if op.Status == "DONE" {
		return nil
	}
	if poll <= 0 {
		poll = 2 * time.Second
	}
	path := "/v1/projects/" + url.PathEscape(a.project) + "/operations/" + url.PathEscape(op.Name)
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("cloudsql: waiting for operation %s: %w", op.Name, ctx.Err())
		case <-ticker.C:
			var current operation
			if err := a.do(ctx, http.MethodGet, path, nil, &current); err != nil {
				return err
			}
			if err := current.err(); err != nil {
				return err
			}
			if current.Status == "DONE" {
				return nil
			}
		}
	}
}

// parseTime reads an RFC3339 timestamp, answering the zero time rather than an
// error.
//
// The zero time is right here rather than lax: createTime is used for ordering
// and for Inventory's record, and an instance whose timestamp the API rendered
// in a shape this parser does not know must still be listed. An instance
// missing from an inventory is a leak the detector cannot see, which is worse
// than one with a zero date.
func parseTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t
		}
	}
	return time.Time{}
}
