package controlplane

// Minting a credential from the workflow's own identity.
//
// The environment token is the only credential this package used to accept, and
// on a CI runner nobody ever set one. Nothing in .github/ or examples/ put
// AF_CONTROL_PLANE_TOKEN anywhere, so the sink's constructor was never reached
// and a run reported nothing: the events went to the local log and the terminal
// and stopped there. The fix is not to ask every user to paste a long lived
// secret into their repository. It is to let the job prove who it is.
//
// GitHub will sign a statement about the job that is running, naming the
// repository, the commit, the workflow and the run attempt. A job with
// id-token: write asks the runner for one and the control plane verifies the
// signature against GitHub's published keys, which is the same exchange the
// report step in examples/github-workflow.yml already performs to obtain its
// callback credential. This is that mechanism pointed at the sink rather than a
// second one invented beside it.
//
// Two properties are worth stating because they are the reason this is better
// than a secret rather than merely more convenient. The credential is short
// lived and scoped, so a leaked build log costs an hour rather than an account.
// And GitHub refuses to mint an identity token for a pull request from a fork,
// which is precisely what keeps a fork's build from reporting as the upstream
// repository; the exchange simply fails there and the run carries on without a
// control plane, which is the ordinary unconfigured path.
//
// The environment token still wins when it is set. Self hosted installations
// and developer machines have no runner to ask, and a user who has pasted a
// token has said what they want. This is an addition and not a replacement.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// WorkflowAudience is the audience a workflow has to ask for.
//
// Not the default. GitHub's default audience is the repository owner's URL, and
// a token minted for that is one every workflow in the organization can obtain,
// which makes it useless as proof of anything. The control plane checks this
// exact string, so a token minted for something else is refused there rather
// than accepted as if it meant something. It matches CALLBACK_AUDIENCE in
// web/apps/api/src/github/oidc.ts and the audience the example workflow asks
// for; all three have to agree.
const WorkflowAudience = "antifailure-control-plane"

// EngineTokenPath is where a verified workflow identity is traded for a
// credential the ingestion endpoint accepts.
//
// A separate route from the report callback on purpose. The callback credential
// is bound to one commit's check generation and is stored against that row, so
// it authenticates a report about that commit and nothing else. Events are not
// about one commit, so they need a credential the engine token lookup can
// resolve, and issuing one is a different decision from issuing the other.
const EngineTokenPath = "/v1/engine/token"

// Runner environment variables. The runner sets both of these itself for any
// job with id-token: write, which is why the example workflow does not declare
// them: declaring them would read them at expression evaluation time, which is
// before the runner has set them.
const (
	identityURLVar   = "ACTIONS_ID_TOKEN_REQUEST_URL"
	identityTokenVar = "ACTIONS_ID_TOKEN_REQUEST_TOKEN"
)

// WorkflowIdentityOptions configures the exchange.
type WorkflowIdentityOptions struct {
	// Lookup reads the environment. Required.
	Lookup func(string) (string, bool)
	// BaseURL is the control plane to exchange with. Empty means the hosted
	// instance, matching the client.
	BaseURL string
	// HTTP is the transport, for tests.
	HTTP *http.Client
}

// WorkflowIdentityAvailable reports whether this process is running somewhere
// that will vouch for it.
//
// Both variables or neither. A job without id-token: write has neither, and
// half of them means something has gone wrong that a caller should not paper
// over by attempting an exchange that cannot work.
func WorkflowIdentityAvailable(lookup func(string) (string, bool)) bool {
	if lookup == nil {
		return false
	}
	endpoint, hasURL := lookup(identityURLVar)
	token, hasToken := lookup(identityTokenVar)
	return hasURL && hasToken &&
		strings.TrimSpace(endpoint) != "" && strings.TrimSpace(token) != ""
}

// TokenFromWorkflowIdentity trades this job's identity for an engine token.
//
// Two calls. The first asks the runner for a signed statement about this job,
// and the second presents it to the control plane, which verifies the signature
// against GitHub's keys and answers with a credential of its own.
//
// A failure here is not a failure of the run. The caller reports it and carries
// on without a control plane, because the observability of a thing is not the
// thing, and the commonest reason to land here is a pull request from a fork,
// where GitHub declining to mint is the security property working rather than
// a fault.
func TokenFromWorkflowIdentity(ctx context.Context, opts WorkflowIdentityOptions) (string, error) {
	if !WorkflowIdentityAvailable(opts.Lookup) {
		return "", nil
	}
	httpClient := opts.HTTP
	if httpClient == nil {
		// Bounded on purpose. This runs on the path that brings an environment
		// up, and a control plane that accepts a connection and then never
		// answers must not hold a build open indefinitely.
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	identity, err := requestWorkflowIdentity(ctx, opts.Lookup, httpClient)
	if err != nil || identity == "" {
		return "", err
	}
	return exchangeWorkflowIdentity(ctx, identity, opts.BaseURL, httpClient)
}

// requestWorkflowIdentity asks the runner for the signed statement.
func requestWorkflowIdentity(
	ctx context.Context, lookup func(string) (string, bool), httpClient *http.Client,
) (string, error) {
	rawURL, _ := lookup(identityURLVar)
	requestToken, _ := lookup(identityTokenVar)
	rawURL, requestToken = strings.TrimSpace(rawURL), strings.TrimSpace(requestToken)

	// The runner's URL already carries an api-version query, so the audience is
	// appended to it rather than starting a query of its own.
	sep := "&"
	if !strings.Contains(rawURL, "?") {
		sep = "?"
	}
	target := rawURL + sep + "audience=" + WorkflowAudience

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", fmt.Errorf("controlplane: asking the runner for a workflow identity: %w", err)
	}
	req.Header.Set("authorization", "Bearer "+requestToken)
	req.Header.Set("accept", "application/json")

	res, err := httpClient.Do(req)
	if err != nil {
		// The error names nothing from the request. The URL carries a token in
		// its query on some runners and the header carries one on all of them,
		// so neither goes into a message that ends up in a build log.
		return "", fmt.Errorf("controlplane: the runner would not answer with a workflow identity: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		// The body is not quoted. It is the runner's own error and it has been
		// known to echo the request, and this package does not put anything
		// derived from a credential into an error that gets printed.
		return "", fmt.Errorf(
			"controlplane: the runner refused to mint a workflow identity (%d). A job needs "+
				"'permissions: id-token: write' to ask for one", res.StatusCode)
	}

	var body struct {
		Value string `json:"value"`
	}
	// Bounded, because this must not read an unbounded body from something that
	// may not be the runner.
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("controlplane: reading the workflow identity: %w", err)
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", fmt.Errorf("controlplane: the runner's workflow identity was not JSON")
	}
	if strings.TrimSpace(body.Value) == "" {
		return "", fmt.Errorf(
			"controlplane: the runner returned an empty workflow identity. On a pull request " +
				"from a fork GitHub never mints one, which is deliberate")
	}
	return strings.TrimSpace(body.Value), nil
}

// exchangeWorkflowIdentity presents the identity and takes back a token.
func exchangeWorkflowIdentity(
	ctx context.Context, identity, baseURL string, httpClient *http.Client,
) (string, error) {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		base = DefaultBaseURL
	}
	target := strings.TrimRight(base, "/") + EngineTokenPath

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader("{}"))
	if err != nil {
		return "", fmt.Errorf("controlplane: %w", err)
	}
	req.Header.Set("authorization", "Bearer "+identity)
	req.Header.Set("accept", "application/json")
	req.Header.Set("content-type", "application/json")

	res, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("controlplane: could not reach the control plane to exchange this job's identity: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode == http.StatusNotFound {
		// A control plane that predates this exchange, rather than one that
		// refused it. Worth its own sentence: "refused (404)" would send
		// somebody looking for a permission problem in a repository that does
		// not have one, when what they need is to upgrade the server.
		return "", fmt.Errorf(
			"controlplane: %s does not offer the identity exchange, so this run is not reported. "+
				"A control plane older than this engine needs upgrading, or set "+
				"AF_CONTROL_PLANE_TOKEN to an engine token instead", hostOf(target))
	}
	if res.StatusCode != http.StatusOK {
		// The control plane's refusals here are written for the workflow author
		// and name what to fix, so the reason is worth carrying. The body is
		// read bounded and the token is never in it on this path, because a
		// non-200 does not carry one.
		snippet, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		if detail := reasonFrom(snippet); detail != "" {
			return "", fmt.Errorf("controlplane: this job's identity was refused: %s", detail)
		}
		return "", fmt.Errorf("controlplane: this job's identity was refused (%d)", res.StatusCode)
	}

	var body struct {
		Token string `json:"token"`
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("controlplane: reading the exchanged token: %w", err)
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		// Deliberately does not quote the body. On this path the body is the
		// one place a usable credential appears, so a parse failure that echoed
		// it would print the token it failed to read.
		return "", fmt.Errorf("controlplane: the control plane's answer to the identity exchange was not JSON")
	}
	if strings.TrimSpace(body.Token) == "" {
		return "", fmt.Errorf("controlplane: the control plane issued no token for this job's identity")
	}
	return strings.TrimSpace(body.Token), nil
}

// hostOf names the control plane without repeating the path or any query.
func hostOf(target string) string {
	if u, err := url.Parse(target); err == nil && u.Host != "" {
		return u.Host
	}
	return "the control plane"
}

// reasonFrom pulls the error sentence out of a refusal, or returns nothing.
//
// Nothing rather than the raw body: an unparseable refusal is quoted by status
// code alone, because a body this package cannot recognise is a body it cannot
// promise is free of anything worth not printing.
func reasonFrom(raw []byte) string {
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return ""
	}
	return strings.TrimSpace(body.Error)
}
