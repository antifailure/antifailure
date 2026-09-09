package secrets

// AWS Secrets Manager.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Without the AWS SDK, which is the largest single decision behind this file
// and the one most likely to be questioned. Reading a secret is one signed POST
// to one endpoint. The SDK that does that for you brings roughly a hundred
// packages into a module whose whole purpose is holding credentials, and every
// one of them is code with access to them. Signature Version 4 is a documented
// algorithm of about a hundred lines, it is verified against the canonical
// example AWS publishes for exactly this purpose, and it does not change.
//
// The signing and the credential chain now live in ee/engine/cloudauth,
// because a managed Postgres provider and a runtime that pulls an image sign
// the same way and must not each grow their own copy. What is left here is the
// one API call this store makes and what it does with each answer. The chain
// itself is unchanged and is still deliberately short: the environment, the ECS
// credential endpoint, and EC2 instance metadata, with what is missing named
// rather than silently absent.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/antifailure/antifailure/ee/engine/cloudauth"
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
//
// An alias rather than a second declaration, so that a caller which built one
// of these before the credential path moved to ee/engine/cloudauth still names
// the same type and there is no conversion to forget.
type AWSCredentials = cloudauth.AWSCredentials

// AWSBackend reads from Secrets Manager.
type AWSBackend struct {
	cfg   AWSConfig
	chain *cloudauth.AWSChain

	mu     sync.Mutex
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
	return New(newAWSBackend(cfg)), nil
}

// newAWSBackend wires the config to a credential chain.
func newAWSBackend(cfg AWSConfig) *AWSBackend {
	if cfg.Getenv == nil {
		cfg.Getenv = os.Getenv
	}
	return &AWSBackend{cfg: cfg, chain: cloudauth.NewAWSChain(cfg.Getenv, cfg.Credentials)}
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

// Reach finds credentials and then proves Secrets Manager itself answers.
//
// The second half is new and the argument that kept it out was wrong in a way
// worth recording, because the same argument will be made again for the next
// adapter. It said: "Secrets Manager has no free health endpoint: every probe
// is a signed, billed, rate-limited API call, and making one per af up to
// discover something the first real lookup discovers anyway is a cost with no
// return." Two of its three clauses do not survive contact with the code.
//
// It is not one call per af up. Source.Available guards the probe with a
// sync.Once, so Reach runs AT MOST ONCE PER PROCESS PER SOURCE, not once per
// lookup: twenty declared variables against this store make one probe, not
// twenty. And it need not be signed, so it is not a billed API call and it
// spends no credential. An unsigned request is answered by the service with
// MissingAuthenticationTokenException, and that answer is the entire proof
// being sought, because what is in question is whether the host is there.
//
// What the check was missing is what the first live Key Vault run found in the
// Azure adapter, and this store had it worse. Azure at least proved that
// Microsoft Entra answered; on the environment-credential path this made no
// network call whatsoever, so a wholly unreachable Secrets Manager, a VPC
// endpoint pointed at the wrong place, or a typo in Endpoint reported the
// source perfectly usable, and AF-SEC-001 listed it as a place the value could
// have come from while nothing there could be read. On the ECS and instance
// metadata paths it did make a call, to the credential endpoint, which is a
// different host from secretsmanager.<region>.amazonaws.com and proves nothing
// about it.
//
// Finding no credentials at all is still the thing most often actually wrong
// and it is still reported first, with the places that were looked in named.
//
// ANY answer from the endpoint proves it is reachable, including a refusal. The
// target header is sent so that the answer comes from Secrets Manager rather
// than from whatever else might be listening on a mistyped address.
func (a *AWSBackend) Reach(ctx context.Context) error {
	if _, err := a.chain.Credentials(ctx); err != nil {
		return err
	}
	if _, err := cloudauth.Do(ctx, cloudauth.Request{
		Method: "POST",
		URL:    a.endpoint(),
		Body:   []byte(`{"MaxResults":1}`),
		Headers: map[string]string{
			"Content-Type": "application/x-amz-json-1.1",
			"X-Amz-Target": "secretsmanager.ListSecrets",
		},
	}); err != nil {
		return fmt.Errorf("cannot be reached: %s", err)
	}
	return nil
}

// endpoint is the address this store's calls go to.
//
// Shared by Reach and the lookup so that a probe can never prove a different
// host from the one a value is read from, which would be a check that reports
// on something other than what it guards.
func (a *AWSBackend) endpoint() string {
	if a.cfg.Endpoint != "" {
		return a.cfg.Endpoint
	}
	return "https://secretsmanager." + a.cfg.Region + ".amazonaws.com/"
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
	a.chain.Reset()
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
	creds, err := a.chain.Credentials(ctx)
	if err != nil {
		return "", false, err
	}

	body, err := json.Marshal(map[string]string{"SecretId": id})
	if err != nil {
		return "", false, err
	}
	endpoint := a.endpoint()

	headers := map[string]string{
		"Content-Type": "application/x-amz-json-1.1",
		"X-Amz-Target": "secretsmanager.GetSecretValue",
	}
	signed, err := cloudauth.SignV4(cloudauth.SigV4Request{
		Method: "POST", URL: endpoint, Body: body, Headers: headers,
		Region: a.cfg.Region, Service: "secretsmanager",
		Credentials: creds, Now: time.Now().UTC(),
	})
	if err != nil {
		return "", false, err
	}

	resp, err := cloudauth.Do(ctx, cloudauth.Request{
		Method: "POST", URL: endpoint, Body: body, Headers: signed})
	if err != nil {
		return "", false, fmt.Errorf("cannot be reached: %s", err)
	}

	switch {
	case resp.Status == 200:
	case resp.Status == http.StatusBadRequest && awsErrorType(resp.Body) == "ResourceNotFoundException":
		// A secret that is not there is a miss, so the chain falls through.
		// Secrets Manager reports it as a 400 with a type rather than a 404,
		// which is why the type has to be read: treating every 400 as a miss
		// would swallow a malformed request and treating it as a failure would
		// make every variable this store does not hold fatal.
		return "", false, nil
	case resp.Rejected(),
		awsErrorType(resp.Body) == "AccessDeniedException",
		awsErrorType(resp.Body) == "ExpiredTokenException",
		awsErrorType(resp.Body) == "UnrecognizedClientException":
		return "", false, wrap(ErrRejected, "AWS answered %d %s, using credentials from %s",
			resp.Status, awsErrorType(resp.Body), creds.Source)
	default:
		return "", false, fmt.Errorf("AWS answered %d %s", resp.Status, awsErrorType(resp.Body))
	}

	var payload struct {
		SecretString string `json:"SecretString"`
		SecretBinary string `json:"SecretBinary"`
	}
	if err := resp.Decode(&payload); err != nil {
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
