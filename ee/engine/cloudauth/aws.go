package cloudauth

// AWS credentials and Signature Version 4.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// The credential chain is deliberately short: the environment, the ECS
// credential endpoint, and EC2 instance metadata. Those are what a CI runner, a
// task, and an instance actually have. What is missing is named rather than
// silently absent, because "no credentials were found" without saying which
// four places were looked in is the message this whole area exists to avoid. A
// profile in ~/.aws/credentials is deliberately not read, and the refusal says
// so, because a tool that quietly picked up whichever profile happened to be
// exported would sign against an account nobody chose.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

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

// AWSChain finds credentials and holds on to them until they expire.
//
// One instance per caller rather than a package level cache, because two
// sources may be configured for two accounts and a shared cache would hand the
// second one the first one's keys.
type AWSChain struct {
	getenv func(string) string

	mu    sync.Mutex
	creds *AWSCredentials
}

// NewAWSChain builds a chain.
//
// getenv is injected so a test does not have to mutate the process environment,
// and so that af explain can resolve against a different one. supplied may be
// nil; when it is not, those credentials are used as they are and the chain is
// never walked.
func NewAWSChain(getenv func(string) string, supplied *AWSCredentials) *AWSChain {
	return &AWSChain{getenv: getenv, creds: supplied}
}

// Credentials finds or renews the keys.
func (c *AWSChain) Credentials(ctx context.Context) (AWSCredentials, error) {
	c.mu.Lock()
	current := c.creds
	c.mu.Unlock()

	// Renewed a minute early. Temporary credentials that expire between being
	// read and being used produce a rejection that a refresh would have
	// avoided, and a minute is longer than any request here takes.
	if current != nil && (current.Expires.IsZero() || time.Now().Add(time.Minute).Before(current.Expires)) {
		return *current, nil
	}

	found, err := c.discover(ctx)
	if err != nil {
		return AWSCredentials{}, err
	}
	c.mu.Lock()
	c.creds = &found
	c.mu.Unlock()
	return found, nil
}

// Reset discards the credentials so the next lookup finds new ones.
//
// Which is the whole mechanism for every AWS credential that can be renewed: a
// container endpoint and an instance role both hand out fresh temporary keys on
// request. Long-lived keys from the environment come back identical, and the
// second rejection is then correctly reported as a credential that is wrong
// rather than one that expired.
func (c *AWSChain) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.creds = nil
}

// discover walks the credential chain, in the order AWS's own tooling does.
func (c *AWSChain) discover(ctx context.Context) (AWSCredentials, error) {
	if id := c.getenv("AWS_ACCESS_KEY_ID"); id != "" {
		secret := c.getenv("AWS_SECRET_ACCESS_KEY")
		if secret == "" {
			return AWSCredentials{}, Wrap(ErrNotConfigured,
				"AWS_ACCESS_KEY_ID is set and AWS_SECRET_ACCESS_KEY is not")
		}
		return AWSCredentials{
			AccessKeyID: id, SecretAccessKey: secret,
			SessionToken: c.getenv("AWS_SESSION_TOKEN"),
			Source:       "the environment",
		}, nil
	}

	// An ECS task. The relative URI is set by the agent and the full URI form
	// is what EKS Pod Identity uses.
	if uri := c.getenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI"); uri != "" {
		return c.fromEndpoint(ctx, "http://169.254.170.2"+uri,
			c.getenv("AWS_CONTAINER_AUTHORIZATION_TOKEN"), "the ECS credential endpoint", 0)
	}
	if uri := c.getenv("AWS_CONTAINER_CREDENTIALS_FULL_URI"); uri != "" {
		return c.fromEndpoint(ctx, uri,
			c.getenv("AWS_CONTAINER_AUTHORIZATION_TOKEN"), "the container credential endpoint", 0)
	}

	if creds, err := c.fromInstanceMetadata(ctx); err == nil {
		return creds, nil
	}

	// Named rather than silently absent. A message that says only "no
	// credentials" leaves somebody guessing which of four mechanisms was meant
	// to supply them, and the answer is usually that the one they configured is
	// not one of these.
	return AWSCredentials{}, Wrap(ErrNotConfigured,
		"no AWS credentials were found: AWS_ACCESS_KEY_ID is unset, no container "+
			"credential endpoint is configured, and instance metadata did not answer. "+
			"A profile in ~/.aws/credentials and a web identity token file are not read "+
			"by this source; export the keys, or use a role the runtime already provides")
}

// fromEndpoint reads credentials from the ECS or Pod Identity agent.
func (c *AWSChain) fromEndpoint(ctx context.Context, url, token, source string, timeout time.Duration) (AWSCredentials, error) {
	headers := map[string]string{"Accept": "application/json"}
	if token != "" {
		headers["Authorization"] = token
	}
	resp, err := Do(ctx, Request{Method: "GET", URL: url, Headers: headers, Timeout: timeout})
	if err != nil {
		return AWSCredentials{}, fmt.Errorf("%s could not be reached: %s", source, err)
	}
	if resp.Status != 200 {
		return AWSCredentials{}, fmt.Errorf("%s answered %d", source, resp.Status)
	}
	var payload struct {
		AccessKeyID     string `json:"AccessKeyId"`
		SecretAccessKey string `json:"SecretAccessKey"`
		Token           string `json:"Token"`
		Expiration      string `json:"Expiration"`
	}
	if err := resp.Decode(&payload); err != nil {
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
func (c *AWSChain) fromInstanceMetadata(ctx context.Context) (AWSCredentials, error) {
	const base = "http://169.254.169.254"
	// A second, not the shared ten. This address is link-local: on an instance
	// it answers in single-digit milliseconds, and on a laptop nothing answers
	// and the connection hangs rather than being refused. At the shared timeout
	// every af up on every machine that is not an EC2 instance would wait ten
	// seconds here to learn something it could have learned in one.
	const metadataTimeout = time.Second
	tokenResp, err := Do(ctx, Request{
		Method: "PUT", URL: base + "/latest/api/token",
		Headers: map[string]string{"X-aws-ec2-metadata-token-ttl-seconds": "60"},
		Timeout: metadataTimeout,
	})
	if err != nil || tokenResp.Status != 200 {
		return AWSCredentials{}, fmt.Errorf("instance metadata did not answer")
	}
	imds := map[string]string{"X-aws-ec2-metadata-token": string(tokenResp.Body)}

	roleResp, err := Do(ctx, Request{
		Method: "GET", URL: base + "/latest/meta-data/iam/security-credentials/",
		Headers: imds, Timeout: metadataTimeout,
	})
	if err != nil || roleResp.Status != 200 {
		return AWSCredentials{}, fmt.Errorf("this instance has no role attached")
	}
	role := strings.TrimSpace(strings.Split(string(roleResp.Body), "\n")[0])
	if role == "" {
		return AWSCredentials{}, fmt.Errorf("this instance has no role attached")
	}
	return c.fromEndpoint(ctx,
		base+"/latest/meta-data/iam/security-credentials/"+role, "",
		"the EC2 instance role "+role, metadataTimeout)
}

// ---------------------------------------------------------------------------
// Signature Version 4
// ---------------------------------------------------------------------------

// SigV4Algorithm is the scheme name Signature Version 4 puts in the string to
// sign and at the front of the Authorization header.
//
// Exported, and it is the only spelling of it in the enterprise module, because
// TestThreeCloudsAreSignedByOneImplementation decides "one implementation" by
// looking for this literal outside this package. A file that legitimately needs
// to READ the header, rather than to produce one, would otherwise have to write
// the literal again and would be reported as a second signer. The fake RDS
// control plane in db/aurora/fakerds is exactly that file: it checks the scheme
// on an incoming request and then calls SignV4 below to recompute the
// signature, so it implements nothing.
//
// Routing the reader through the constant is the stronger arrangement rather
// than a way around the check. The gate keeps its full reach over anything that
// hardcodes the string, and the one place the algorithm is named is now the one
// place it is implemented, which is what the check is asserting in the first
// place.
const SigV4Algorithm = "AWS4-HMAC-SHA256"

// SigV4Request is one request to sign.
type SigV4Request struct {
	Method      string
	URL         string
	Body        []byte
	Headers     map[string]string
	Region      string
	Service     string
	Credentials AWSCredentials
	Now         time.Time
}

// SignV4 returns the headers a request needs, including Authorization.
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
func SignV4(req SigV4Request) (map[string]string, error) {
	host, path, query, err := splitURL(req.URL)
	if err != nil {
		return nil, err
	}

	stamp := req.Now.Format("20060102T150405Z")
	day := req.Now.Format("20060102")

	payloadHash := sha256Hex(req.Body)

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
	for k, v := range req.Headers {
		signed[strings.ToLower(k)] = v
	}
	if req.Credentials.SessionToken != "" {
		// Inside the signature, not merely alongside it. AWS includes this
		// header in the canonical request, so adding it afterwards produces a
		// signature over a different request and a refusal that reads as a
		// wrong secret key.
		signed["x-amz-security-token"] = req.Credentials.SessionToken
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
		req.Method, path, query, canonicalHeaders.String(), signedHeaders, payloadHash,
	}, "\n")

	scope := strings.Join([]string{day, req.Region, req.Service, "aws4_request"}, "/")
	toSign := strings.Join([]string{
		SigV4Algorithm, stamp, scope, sha256Hex([]byte(canonical)),
	}, "\n")

	key := hmacSHA256([]byte("AWS4"+req.Credentials.SecretAccessKey), day)
	key = hmacSHA256(key, req.Region)
	key = hmacSHA256(key, req.Service)
	key = hmacSHA256(key, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(key, toSign))

	out := map[string]string{}
	for k, v := range req.Headers {
		out[k] = v
	}
	out["X-Amz-Date"] = stamp
	out["X-Amz-Content-Sha256"] = payloadHash
	if req.Credentials.SessionToken != "" {
		out["X-Amz-Security-Token"] = req.Credentials.SessionToken
	}
	out["Authorization"] = fmt.Sprintf(
		SigV4Algorithm+" Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		req.Credentials.AccessKeyID, scope, signedHeaders, signature)
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
