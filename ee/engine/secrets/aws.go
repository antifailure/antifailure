package secrets

// AWS Secrets Manager.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Without the AWS SDK, which is the largest single decision in this file and
// the one most likely to be questioned. Reading a secret is one signed POST to
// one endpoint. The SDK that does that for you brings roughly a hundred
// packages into a module whose whole purpose is holding credentials, and every
// one of them is code with access to them. Signature Version 4 is a documented
// algorithm of about a hundred lines, it is verified here against the canonical
// example AWS publishes for exactly this purpose, and it does not change.
//
// The credential chain is deliberately short: the environment, the ECS
// credential endpoint, and EC2 instance metadata. Those are what a CI runner, a
// task, and an instance actually have. What is missing is named rather than
// silently absent, because "no credentials were found" without saying which
// four places were looked in is the message this whole package exists to avoid.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// AWSConfig is what a Secrets Manager source needs.
type AWSConfig struct {
	// Region is where the secret lives. Required: Secrets Manager is regional
	// and a secret in eu-west-1 does not exist in us-east-1.
	Region string
	// Prefix is prepended to every variable name to form the secret id, so that
	// DATABASE_URL becomes antifailure/production/DATABASE_URL. Optional.
	Prefix string
	// SecretID names one secret holding every variable as a JSON document,
	// which is how these are usually organised: one secret per application with
	// the variables as its keys, because Secrets Manager charges per secret per
	// month and one per variable is forty secrets.
	//
	// When it is empty each variable is its own secret, named Prefix+name.
	SecretID string
	// Endpoint overrides the service address, for a VPC endpoint or for a test.
	Endpoint string
	// Credentials supplies the keys directly. When nil they are discovered.
	Credentials *AWSCredentials
	// Getenv is injected so a test does not have to mutate the process
	// environment, and so that af explain can resolve against a different one.
	Getenv func(string) string
}

// AWSCredentials are what a request is signed with.
type AWSCredentials struct {
	AccessKeyID     string
	SecretAccessKey string
	// SessionToken is present for temporary credentials, which is what an
	// assumed role, an ECS task and an EC2 instance all have. Its absence is
	// what distinguishes a long-lived user key.
	SessionToken string
	// Expires is when they stop working. Zero means they do not, which is only
	// true of a long-lived user key.
	Expires time.Time
	// Source names where they came from, for the message that says why a
	// request was refused.
	Source string
}

// AWSBackend reads from Secrets Manager.
type AWSBackend struct {
	cfg AWSConfig

	mu     sync.Mutex
	creds  *AWSCredentials
	cached map[string]string
	loaded bool
}

// NewAWSSecretsManager builds an AWS source, or reports what it is missing.
func NewAWSSecretsManager(cfg AWSConfig) (*Source, error) {
	if cfg.Getenv == nil {
		cfg.Getenv = os.Getenv
	}
	if strings.TrimSpace(cfg.Region) == "" {
		cfg.Region = cfg.Getenv("AWS_REGION")
		if cfg.Region == "" {
			cfg.Region = cfg.Getenv("AWS_DEFAULT_REGION")
		}
	}
	if strings.TrimSpace(cfg.Region) == "" {
		return nil, wrap(ErrNotConfigured,
			"AWS Secrets Manager needs a region (AWS_REGION); a secret is regional "+
				"and one in eu-west-1 does not exist in us-east-1")
	}
	return New(&AWSBackend{cfg: cfg, creds: cfg.Credentials}), nil
}

func (a *AWSBackend) Describe() string {
	where := "AWS Secrets Manager in " + a.cfg.Region
	switch {
	case a.cfg.SecretID != "":
		return where + " (" + a.cfg.SecretID + ")"
	case a.cfg.Prefix != "":
		return where + " (" + a.cfg.Prefix + "*)"
	default:
		return where
	}
}

// Reach checks that there are credentials to sign with.
//
// A configuration check rather than a call, and that is a deliberate trade
// rather than a shortcut. Secrets Manager has no free health endpoint: every
// probe is a signed, billed, rate-limited API call, and making one per af up to
// discover something the first real lookup discovers anyway is a cost with no
// return. What it does check is the thing that is actually usually wrong, which
// is that no credentials could be found at all, and it says which places were
// looked in.
func (a *AWSBackend) Reach(ctx context.Context) error {
	_, err := a.credentials(ctx)
	return err
}

// credentials finds or renews the keys.
func (a *AWSBackend) credentials(ctx context.Context) (AWSCredentials, error) {
	a.mu.Lock()
	current := a.creds
	a.mu.Unlock()

	// Renewed a minute early. Temporary credentials that expire between being
	// read and being used produce a rejection that a refresh would have
	// avoided, and a minute is longer than any request here takes.
	if current != nil && (current.Expires.IsZero() || time.Now().Add(time.Minute).Before(current.Expires)) {
		return *current, nil
	}

	found, err := a.discover(ctx)
	if err != nil {
		return AWSCredentials{}, err
	}
	a.mu.Lock()
	a.creds = &found
	a.mu.Unlock()
	return found, nil
}

// discover walks the credential chain, in the order AWS's own tooling does.
func (a *AWSBackend) discover(ctx context.Context) (AWSCredentials, error) {
	if id := a.cfg.Getenv("AWS_ACCESS_KEY_ID"); id != "" {
		secret := a.cfg.Getenv("AWS_SECRET_ACCESS_KEY")
		if secret == "" {
			return AWSCredentials{}, wrap(ErrNotConfigured,
				"AWS_ACCESS_KEY_ID is set and AWS_SECRET_ACCESS_KEY is not")
		}
		return AWSCredentials{
			AccessKeyID: id, SecretAccessKey: secret,
			SessionToken: a.cfg.Getenv("AWS_SESSION_TOKEN"),
			Source:       "the environment",
		}, nil
	}

	// An ECS task. The relative URI is set by the agent and the full URI form
	// is what EKS Pod Identity uses.
	if uri := a.cfg.Getenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI"); uri != "" {
		return a.fromEndpoint(ctx, "http://169.254.170.2"+uri,
			a.cfg.Getenv("AWS_CONTAINER_AUTHORIZATION_TOKEN"), "the ECS credential endpoint", 0)
	}
	if uri := a.cfg.Getenv("AWS_CONTAINER_CREDENTIALS_FULL_URI"); uri != "" {
		return a.fromEndpoint(ctx, uri,
			a.cfg.Getenv("AWS_CONTAINER_AUTHORIZATION_TOKEN"), "the container credential endpoint", 0)
	}

	if creds, err := a.fromInstanceMetadata(ctx); err == nil {
		return creds, nil
	}

	// Named rather than silently absent. A message that says only "no
	// credentials" leaves somebody guessing which of four mechanisms was meant
	// to supply them, and the answer is usually that the one they configured is
	// not one of these.
	return AWSCredentials{}, wrap(ErrNotConfigured,
		"no AWS credentials were found: AWS_ACCESS_KEY_ID is unset, no container "+
			"credential endpoint is configured, and instance metadata did not answer. "+
			"A profile in ~/.aws/credentials and a web identity token file are not read "+
			"by this source; export the keys, or use a role the runtime already provides")
}

// fromEndpoint reads credentials from the ECS or Pod Identity agent.
func (a *AWSBackend) fromEndpoint(ctx context.Context, url, token, source string, timeout time.Duration) (AWSCredentials, error) {
	headers := map[string]string{"Accept": "application/json"}
	if token != "" {
		headers["Authorization"] = token
	}
	resp, err := do(ctx, request{method: "GET", url: url, headers: headers, timeout: timeout})
	if err != nil {
		return AWSCredentials{}, fmt.Errorf("%s could not be reached: %s", source, err)
	}
	if resp.status != 200 {
		return AWSCredentials{}, fmt.Errorf("%s answered %d", source, resp.status)
	}
	var payload struct {
		AccessKeyID     string `json:"AccessKeyId"`
		SecretAccessKey string `json:"SecretAccessKey"`
		Token           string `json:"Token"`
		Expiration      string `json:"Expiration"`
	}
	if err := resp.decode(&payload); err != nil {
		return AWSCredentials{}, err
	}
	expires, _ := time.Parse(time.RFC3339, payload.Expiration)
	return AWSCredentials{
		AccessKeyID: payload.AccessKeyID, SecretAccessKey: payload.SecretAccessKey,
		SessionToken: payload.Token, Expires: expires, Source: source,
	}, nil
}

// fromInstanceMetadata reads an EC2 instance role, through IMDSv2.
//
// Version 2 only. Version 1 answers an unauthenticated GET, which is what makes
// a server-side request forgery in an application on the instance into a
// credential disclosure, and reading it here would mean this tool works on
// instances configured the way nobody should configure them.
func (a *AWSBackend) fromInstanceMetadata(ctx context.Context) (AWSCredentials, error) {
	const base = "http://169.254.169.254"
	// A second, not the shared ten. This address is link-local: on an instance
	// it answers in single-digit milliseconds, and on a laptop nothing answers
	// and the connection hangs rather than being refused. At the shared timeout
	// every af up on every machine that is not an EC2 instance would wait ten
	// seconds here to learn something it could have learned in one.
	const metadataTimeout = time.Second
	tokenResp, err := do(ctx, request{
		method: "PUT", url: base + "/latest/api/token",
		headers: map[string]string{"X-aws-ec2-metadata-token-ttl-seconds": "60"},
		timeout: metadataTimeout,
	})
	if err != nil || tokenResp.status != 200 {
		return AWSCredentials{}, fmt.Errorf("instance metadata did not answer")
	}
	imds := map[string]string{"X-aws-ec2-metadata-token": string(tokenResp.body)}

	roleResp, err := do(ctx, request{
		method: "GET", url: base + "/latest/meta-data/iam/security-credentials/",
		headers: imds, timeout: metadataTimeout,
	})
	if err != nil || roleResp.status != 200 {
		return AWSCredentials{}, fmt.Errorf("this instance has no role attached")
	}
	role := strings.TrimSpace(strings.Split(string(roleResp.body), "\n")[0])
	if role == "" {
		return AWSCredentials{}, fmt.Errorf("this instance has no role attached")
	}
	return a.fromEndpoint(ctx,
		base+"/latest/meta-data/iam/security-credentials/"+role, "",
		"the EC2 instance role "+role, metadataTimeout)
}

// Refresh discards the credentials so the next lookup finds new ones.
//
// Which is the whole mechanism for every AWS credential that can be renewed: a
// container endpoint and an instance role both hand out fresh temporary keys on
// request. Long-lived keys from the environment come back identical, and the
// second rejection is then correctly reported as a credential that is wrong
// rather than one that expired.
func (a *AWSBackend) Refresh(context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg.Credentials != nil {
		return fmt.Errorf("these credentials were supplied directly and cannot be renewed")
	}
	a.creds = nil
	a.cached, a.loaded = nil, false
	return nil
}

// Fetch reads a variable.
func (a *AWSBackend) Fetch(ctx context.Context, name string) (string, bool, error) {
	if a.cfg.SecretID == "" {
		value, found, err := a.getSecret(ctx, a.cfg.Prefix+name)
		if err != nil || !found {
			return "", false, err
		}
		return value, true, nil
	}

	a.mu.Lock()
	loaded, cached := a.loaded, a.cached
	a.mu.Unlock()
	if !loaded {
		raw, found, err := a.getSecret(ctx, a.cfg.SecretID)
		if err != nil {
			return "", false, err
		}
		cached = map[string]string{}
		if found {
			if err := json.Unmarshal([]byte(raw), &cached); err != nil {
				return "", false, fmt.Errorf(
					"the secret %s is configured as one document holding every variable "+
						"and its value is not a JSON object", a.cfg.SecretID)
			}
		}
		a.mu.Lock()
		a.cached, a.loaded = cached, true
		a.mu.Unlock()
	}
	value, ok := cached[name]
	return value, ok, nil
}

// getSecret makes the one API call this adapter needs.
func (a *AWSBackend) getSecret(ctx context.Context, id string) (string, bool, error) {
	creds, err := a.credentials(ctx)
	if err != nil {
		return "", false, err
	}

	body, err := json.Marshal(map[string]string{"SecretId": id})
	if err != nil {
		return "", false, err
	}
	endpoint := a.cfg.Endpoint
	if endpoint == "" {
		endpoint = "https://secretsmanager." + a.cfg.Region + ".amazonaws.com/"
	}

	headers := map[string]string{
		"Content-Type": "application/x-amz-json-1.1",
		"X-Amz-Target": "secretsmanager.GetSecretValue",
	}
	signed, err := signV4(sigV4Request{
		method: "POST", url: endpoint, body: body, headers: headers,
		region: a.cfg.Region, service: "secretsmanager",
		credentials: creds, now: time.Now().UTC(),
	})
	if err != nil {
		return "", false, err
	}

	resp, err := do(ctx, request{method: "POST", url: endpoint, body: body, headers: signed})
	if err != nil {
		return "", false, fmt.Errorf("cannot be reached: %s", err)
	}

	switch {
	case resp.status == 200:
	case resp.status == http.StatusBadRequest && awsErrorType(resp.body) == "ResourceNotFoundException":
		// A secret that is not there is a miss, so the chain falls through.
		// Secrets Manager reports it as a 400 with a type rather than a 404,
		// which is why the type has to be read: treating every 400 as a miss
		// would swallow a malformed request and treating it as a failure would
		// make every variable this store does not hold fatal.
		return "", false, nil
	case resp.rejected(),
		awsErrorType(resp.body) == "AccessDeniedException",
		awsErrorType(resp.body) == "ExpiredTokenException",
		awsErrorType(resp.body) == "UnrecognizedClientException":
		return "", false, wrap(ErrRejected, "AWS answered %d %s, using credentials from %s",
			resp.status, awsErrorType(resp.body), creds.Source)
	default:
		return "", false, fmt.Errorf("AWS answered %d %s", resp.status, awsErrorType(resp.body))
	}

	var payload struct {
		SecretString string `json:"SecretString"`
		SecretBinary string `json:"SecretBinary"`
	}
	if err := resp.decode(&payload); err != nil {
		return "", false, err
	}
	if payload.SecretString == "" && payload.SecretBinary != "" {
		return "", false, fmt.Errorf(
			"the secret %s holds binary rather than a string, and an environment "+
				"variable is a string", id)
	}
	return payload.SecretString, true, nil
}

// awsErrorType reads the exception name out of an error response.
//
// The name and never the message. The message can quote the request, and the
// request names the secret; the type is the part that decides what to do.
func awsErrorType(body []byte) string {
	var payload struct {
		Type string `json:"__type"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Type == "" {
		return "with no error type"
	}
	// The wire form is sometimes prefixed, as "com.amazon.coral.service#Name".
	if i := strings.LastIndexAny(payload.Type, "#."); i >= 0 {
		return payload.Type[i+1:]
	}
	return payload.Type
}

// ---------------------------------------------------------------------------
// Signature Version 4
// ---------------------------------------------------------------------------

type sigV4Request struct {
	method      string
	url         string
	body        []byte
	headers     map[string]string
	region      string
	service     string
	credentials AWSCredentials
	now         time.Time
}

// signV4 returns the headers a request needs, including Authorization.
//
// Implemented rather than imported, and verified against the canonical example
// AWS publishes for this purpose rather than against our own idea of it. The
// algorithm is fixed, published, and has not changed since 2012; the SDK that
// would supply it is a hundred packages inside the module that holds the
// credentials.
//
// The parts that are easy to get wrong, and which the test pins: the signed
// header list is sorted and lowercased and must match the canonical headers
// exactly; the payload hash is of the body even when the body is empty; and the
// session token is signed rather than merely sent, so a temporary credential
// whose token is added after signing is refused.
func signV4(req sigV4Request) (map[string]string, error) {
	host, path, query, err := splitURL(req.url)
	if err != nil {
		return nil, err
	}

	stamp := req.now.Format("20060102T150405Z")
	day := req.now.Format("20060102")

	payloadHash := sha256Hex(req.body)

	signed := map[string]string{
		"host":       host,
		"x-amz-date": stamp,
		// Signed rather than merely sent. It is optional for most services and
		// required by S3, and signing it always means one code path and one
		// thing to be right about, which is also what lets this be verified
		// against the canonical example AWS publishes, since that example is an
		// S3 request.
		"x-amz-content-sha256": payloadHash,
	}
	for k, v := range req.headers {
		signed[strings.ToLower(k)] = v
	}
	if req.credentials.SessionToken != "" {
		// Inside the signature, not merely alongside it. AWS includes this
		// header in the canonical request, so adding it afterwards produces a
		// signature over a different request and a refusal that reads as a
		// wrong secret key.
		signed["x-amz-security-token"] = req.credentials.SessionToken
	}

	names := make([]string, 0, len(signed))
	for k := range signed {
		names = append(names, k)
	}
	sort.Strings(names)

	var canonicalHeaders strings.Builder
	for _, k := range names {
		canonicalHeaders.WriteString(k)
		canonicalHeaders.WriteByte(':')
		// Values are trimmed and internal runs of spaces collapsed, which is
		// part of the specification and not tidying.
		canonicalHeaders.WriteString(strings.Join(strings.Fields(signed[k]), " "))
		canonicalHeaders.WriteByte('\n')
	}
	signedHeaders := strings.Join(names, ";")

	canonical := strings.Join([]string{
		req.method, path, query, canonicalHeaders.String(), signedHeaders, payloadHash,
	}, "\n")

	scope := strings.Join([]string{day, req.region, req.service, "aws4_request"}, "/")
	toSign := strings.Join([]string{
		"AWS4-HMAC-SHA256", stamp, scope, sha256Hex([]byte(canonical)),
	}, "\n")

	key := hmacSHA256([]byte("AWS4"+req.credentials.SecretAccessKey), day)
	key = hmacSHA256(key, req.region)
	key = hmacSHA256(key, req.service)
	key = hmacSHA256(key, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(key, toSign))

	out := map[string]string{}
	for k, v := range req.headers {
		out[k] = v
	}
	out["X-Amz-Date"] = stamp
	out["X-Amz-Content-Sha256"] = payloadHash
	if req.credentials.SessionToken != "" {
		out["X-Amz-Security-Token"] = req.credentials.SessionToken
	}
	out["Authorization"] = fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		req.credentials.AccessKeyID, scope, signedHeaders, signature)
	return out, nil
}

// splitURL returns the host, the canonical path, and the canonical query.
func splitURL(raw string) (host, path, query string, err error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", "", "", err
	}
	path = parsed.EscapedPath()
	if path == "" {
		// An empty path canonicalises to "/", and signing "" produces a
		// signature the service does not agree with.
		path = "/"
	}
	// Query parameters are sorted by name, and Encode does that.
	return parsed.Host, path, parsed.Query().Encode(), nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}
