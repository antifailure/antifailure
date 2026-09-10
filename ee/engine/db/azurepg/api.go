// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package azurepg

// The Azure Resource Manager client, narrowed to the calls this provider makes.
//
// Narrow rather than general, for the reason aurora's rds.go and cloudsql's
// api.go give: a general client is a surface nobody exercises, and every
// unexercised field is somewhere a wrong value can hide until a subscription is
// attached.
//
// Two shapes here are Azure specific and both are places a provider gets it
// wrong quietly. Resource Manager answers a create or delete with 201 or 202
// and an Azure-AsyncOperation header rather than a body, so a caller that reads
// only the status code believes the server exists the moment the request is
// accepted. And every request must carry api-version; omitting it is a 400 that
// names the parameter and not the operation.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/antifailure/antifailure/ee/engine/cloudauth"
	"github.com/antifailure/antifailure/engine/pkg/airgap"
)

type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// defaultEndpoint is the public Resource Manager.
const defaultEndpoint = "https://management.azure.com"

type armAPI struct {
	endpoint      string
	subscription  string
	resourceGroup string
	client        httpDoer
	token         func(context.Context) (string, error)
}

func newARMAPI(opts Options) (*armAPI, error) {
	endpoint := opts.Endpoint
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	if _, err := url.Parse(endpoint); err != nil {
		return nil, fmt.Errorf("azurepg: %s is not a URL: %w", EndpointVariable, err)
	}
	client := opts.HTTPClient
	if client == nil {
		guarded := airgap.Client(airgap.SiteAzurePostgres, 60*time.Second)
		guarded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client = guarded
	}
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	token := opts.Token
	if token == nil {
		source := &cloudauth.AzureTokenSource{
			TenantID: getenv("AZURE_TENANT_ID"), ClientID: getenv("AZURE_CLIENT_ID"),
			ClientSecret: getenv("AZURE_CLIENT_SECRET"), Authority: getenv("AZURE_AUTHORITY_HOST"),
			Resource: defaultEndpoint,
		}
		token = source.Token
	}
	return &armAPI{
		endpoint:      strings.TrimSuffix(endpoint, "/"),
		subscription:  opts.Subscription,
		resourceGroup: opts.ResourceGroup,
		client:        client,
		token:         token,
	}, nil
}

// server is the subset of the flexible server resource this provider reads.
type server struct {
	Name       string            `json:"name"`
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags"`
	Properties serverProperties  `json:"properties"`
}

type serverProperties struct {
	FullyQualifiedDomainName string       `json:"fullyQualifiedDomainName"`
	State                    string       `json:"state"`
	Version                  string       `json:"version"`
	AdministratorLogin       string       `json:"administratorLogin"`
	CreateTimeRaw            string       `json:"earliestRestoreDate"`
	Storage                  storageProps `json:"storage"`
	Network                  networkProps `json:"network"`
}

type storageProps struct {
	StorageSizeGB int64 `json:"storageSizeGB"`
}

// networkProps is how a server's access mode is read.
//
// A server delegated to a subnet carries delegatedSubnetResourceId; one on
// public access does not. Microsoft states a restore cannot cross the two, so
// this is read to refuse the crossing rather than to report it.
type networkProps struct {
	DelegatedSubnetResourceID string `json:"delegatedSubnetResourceId,omitempty"`
	PrivateDNSZoneResourceID  string `json:"privateDnsZoneArmResourceId,omitempty"`
	PublicNetworkAccess       string `json:"publicNetworkAccess"`
}

// access reports which side of Azure's restore boundary a server is on.
func (s *server) access() Access {
	if s != nil && s.Properties.Network.DelegatedSubnetResourceID != "" {
		return AccessPrivate
	}
	return AccessPublic
}

// restoreRequest is the create body for a point in time restore.
//
// createMode is PointInTimeRestore and the source is named by its full resource
// id, which is what Resource Manager wants rather than a bare name. The
// pointInTimeUTC is the moment to recover to.
//
// The field that is NOT here is worth as much as the ones that are: there is no
// place to put firewall rules or server parameters, because Azure does not
// carry either across a restore and pretending the create call could would hide
// the post restore work this provider has to do anyway.
type restoreRequest struct {
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties restoreProps      `json:"properties"`
}

type restoreProps struct {
	CreateMode             string       `json:"createMode"`
	SourceServerResourceID string       `json:"sourceServerResourceId"`
	PointInTimeUTC         string       `json:"pointInTimeUTC"`
	Network                networkProps `json:"network"`
}

func (a *armAPI) serverResourceID(name string) string {
	return fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.DBforPostgreSQL/flexibleServers/%s",
		a.subscription, a.resourceGroup, name)
}

func (a *armAPI) serverPath(name string) string {
	return a.serverResourceID(name) + "?api-version=" + apiVersion
}

func (a *armAPI) serversPath() string {
	return fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.DBforPostgreSQL/flexibleServers?api-version=%s",
		a.subscription, a.resourceGroup, apiVersion)
}

func (a *armAPI) firewallPath(server, rule string) string {
	return a.serverResourceID(server) + "/firewallRules/" + url.PathEscape(rule) +
		"?api-version=" + apiVersion
}

// apiError is a non 2xx answer from Resource Manager.
type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("azure resource manager: HTTP %d: %s: %s", e.Status, e.Code, e.Message)
}

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

// asyncResult is what a mutating call returns.
//
// Resource Manager answers 201 or 202 with the operation's address in a header
// rather than a body, so this carries the header. An empty Poll means the call
// completed synchronously, which is what a fake and some real operations do.
type asyncResult struct {
	Poll string
}

// do issues one request.
func (a *armAPI) do(ctx context.Context, method, path string, body any, out any) (*asyncResult, error) {
	base, err := url.Parse(a.endpoint)
	if err != nil {
		return nil, fmt.Errorf("azurepg: invalid API endpoint: %w", err)
	}
	target, err := url.Parse(path)
	if err != nil {
		return nil, fmt.Errorf("azurepg: invalid API request address: %w", err)
	}
	target = base.ResolveReference(target)
	if target.Scheme != base.Scheme || target.Host != base.Host || target.User != nil {
		return nil, fmt.Errorf("azurepg: API continuation crossed the configured origin")
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("azurepg: encoding %s %s: %w", method, path, err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return nil, fmt.Errorf("azurepg: building %s %s: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	token, err := a.token(ctx)
	if err != nil {
		return nil, fmt.Errorf("azurepg: obtaining an Azure identity: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("azurepg: the Azure identity returned an empty access token")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("azurepg: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("azurepg: reading %s %s: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, decodeAPIError(resp.StatusCode, payload)
	}
	result := &asyncResult{Poll: resp.Header.Get("Azure-AsyncOperation")}
	if result.Poll == "" {
		result.Poll = resp.Header.Get("Location")
	}
	if out != nil && len(bytes.TrimSpace(payload)) > 0 {
		if err := json.Unmarshal(payload, out); err != nil {
			return nil, fmt.Errorf("azurepg: decoding %s %s: %w", method, path, err)
		}
	}
	return result, nil
}

// decodeAPIError turns a non 2xx body into an apiError.
//
// Tolerant on this boundary on purpose: Resource Manager has two error
// envelopes in the wild, the nested {"error":{...}} and a flat {"code",
// "message"}, and a body in neither shape must still produce an error a caller
// can read rather than a parser's complaint that replaces Azure's own sentence.
func decodeAPIError(status int, payload []byte) error {
	var nested struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &nested); err == nil && nested.Error.Message != "" {
		return &apiError{Status: status, Code: nested.Error.Code, Message: nested.Error.Message}
	}
	var flat struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(payload, &flat); err == nil && flat.Message != "" {
		return &apiError{Status: status, Code: flat.Code, Message: flat.Message}
	}
	return &apiError{
		Status:  status,
		Code:    http.StatusText(status),
		Message: strings.TrimSpace(string(payload)),
	}
}

// database is one entry from a server's databases collection.
type database struct {
	Name string `json:"name"`
}

// listDatabases reads the databases on a server.
//
// A real Resource Manager call rather than an assumption that the application
// lives in "postgres". A restore carries every database the source had, and
// which of them holds the application is not something this provider may guess:
// a caller whose data lives in a database called "app" would otherwise get a
// connection string pointing at an empty maintenance database and a branch that
// reports healthy while holding none of their rows.
func (a *armAPI) listDatabases(ctx context.Context, name string) ([]database, error) {
	path := a.serverResourceID(name) + "/databases?api-version=" + apiVersion
	return readPages[database](ctx, a, path)
}

func (a *armAPI) getServer(ctx context.Context, name string) (*server, error) {
	var s server
	if _, err := a.do(ctx, http.MethodGet, a.serverPath(name), nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (a *armAPI) listServers(ctx context.Context) ([]server, error) {
	return readPages[server](ctx, a, a.serversPath())
}

func readPages[T any](ctx context.Context, a *armAPI, path string) ([]T, error) {
	var items []T
	seen := map[string]bool{}
	for path != "" {
		if seen[path] || len(seen) >= 10000 {
			return nil, fmt.Errorf("azurepg: collection continuation repeated or exceeded its page limit")
		}
		seen[path] = true
		var page struct {
			Value    []json.RawMessage `json:"value"`
			NextLink string            `json:"nextLink"`
		}
		if _, err := a.do(ctx, http.MethodGet, path, nil, &page); err != nil {
			return nil, err
		}
		for i, raw := range page.Value {
			var item T
			if err := json.Unmarshal(raw, &item); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				slog.Warn("azurepg: skipping malformed collection element", "index", i)
				continue
			}
			items = append(items, item)
		}
		path = page.NextLink
	}
	return items, nil
}

// restore creates a new server from an existing one at a point in time.
func (a *armAPI) restore(ctx context.Context, source, destination, location string, access networkProps, at time.Time, tags map[string]string) (*asyncResult, error) {
	access.PublicNetworkAccess = "Enabled"
	if access.DelegatedSubnetResourceID != "" {
		access.PublicNetworkAccess = "Disabled"
	}
	body := restoreRequest{
		Location: location,
		Tags:     tags,
		Properties: restoreProps{
			CreateMode:             "PointInTimeRestore",
			SourceServerResourceID: a.serverResourceID(source),
			PointInTimeUTC:         at.UTC().Format(time.RFC3339),
			Network:                access,
		},
	}
	return a.do(ctx, http.MethodPut, a.serverPath(destination), body, nil)
}

func (a *armAPI) deleteServer(ctx context.Context, name string) (*asyncResult, error) {
	return a.do(ctx, http.MethodDelete, a.serverPath(name), nil, nil)
}

// patchServer updates a server, which is how the administrator password is
// reset and how tags are set.
func (a *armAPI) patchServer(ctx context.Context, name string, body any) (*asyncResult, error) {
	return a.do(ctx, http.MethodPatch, a.serverPath(name), body, nil)
}

// putFirewallRule creates the rule without which nobody can connect.
//
// It exists because Microsoft lists firewall rules as a POST RESTORE TASK: they
// are not copied from the source. A branch created without one is a server that
// provisioned successfully and answers nobody, and the failure surfaces as a
// connection timeout that names no firewall.
func (a *armAPI) putFirewallRule(ctx context.Context, srv, rule, start, end string) (*asyncResult, error) {
	body := map[string]any{"properties": map[string]any{
		"startIpAddress": start,
		"endIpAddress":   end,
	}}
	return a.do(ctx, http.MethodPut, a.firewallPath(srv, rule), body, nil)
}

// wait polls an async operation until it succeeds, fails, or ctx ends.
func (a *armAPI) wait(ctx context.Context, result *asyncResult, poll time.Duration) error {
	if result == nil || result.Poll == "" {
		return nil
	}
	if poll <= 0 {
		poll = 2 * time.Second
	}
	target := result.Poll
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("azurepg: waiting for operation: %w", ctx.Err())
		case <-ticker.C:
			var status struct {
				Status string `json:"status"`
				Error  *struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if _, err := a.do(ctx, http.MethodGet, target, nil, &status); err != nil {
				return err
			}
			switch status.Status {
			case "Succeeded":
				return nil
			case "Failed", "Canceled":
				if status.Error != nil {
					return fmt.Errorf("azurepg: operation %s: %s: %s",
						strings.ToLower(status.Status), status.Error.Code, status.Error.Message)
				}
				return fmt.Errorf("azurepg: operation %s with no reason given",
					strings.ToLower(status.Status))
			}
		}
	}
}
