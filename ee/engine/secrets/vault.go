package secrets

// HashiCorp Vault.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Spoken to over its HTTP API rather than through the official client library,
// which is the same judgement the community edition makes about the dotenv
// format: reading one secret is one GET, and a library that parses secrets is a
// library with a great deal of access. The two calls this needs are the KV read
// and the AppRole login, both of which are a URL and a JSON document.
//
// Two authentication modes, because they are two different situations.
//
// A token is what an operator has on a workstation, and it cannot be renewed by
// us: it belongs to somebody, it may be a root token, and calling renew-self on
// it is presumptuous. So a token backend does not implement Refresher, and a
// rejection is final on the first try, which is correct rather than a
// limitation.
//
// An AppRole is what CI has, and it can be renewed, because logging in again is
// the whole mechanism. That is the case the one-refresh rule exists for: a
// long-lived process holding a token past its lease gets exactly one login and
// then reports AF-SEC-002.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
)

// VaultConfig is what a Vault source needs.
type VaultConfig struct {
	// Address is the base URL, as VAULT_ADDR: https://vault.internal:8200.
	Address string
	// Token authenticates directly. Mutually exclusive with RoleID.
	Token string
	// RoleID and SecretID authenticate through AppRole, which is what a CI
	// runner uses and what can be renewed.
	RoleID   string
	SecretID string
	// AppRolePath is where the approle auth method is mounted. Empty means
	// "approle", which is where it is unless somebody moved it.
	AppRolePath string
	// Mount is the KV secrets engine mount. Empty means "secret", the default
	// mount on a dev server and the conventional one elsewhere.
	Mount string
	// Path is the secret holding the variables, under the mount. Every declared
	// variable is a key inside that one secret, which is how these are actually
	// organised: one document per application, keys named after the variables.
	Path string
	// PathPerName reads {Path}/{NAME} instead, taking the field named by Field.
	// Some organizations keep one secret per credential because their access
	// policies are per path, and a source that could not do that would be a
	// source they cannot use.
	PathPerName bool
	// Field is the key read when PathPerName is set. Empty means "value".
	Field string
	// Namespace is the Vault Enterprise namespace, as VAULT_NAMESPACE.
	Namespace string
	// KVv1 reads the older KV engine, which has no data wrapper. Version 2 is
	// the default because it is what a current Vault mounts.
	KVv1 bool
}

// VaultBackend reads from Vault.
type VaultBackend struct {
	cfg VaultConfig

	mu    sync.Mutex
	token string
	// cached is the single secret read for the many-keys-in-one-path shape,
	// held so that resolving twenty variables is one request rather than
	// twenty. Cleared by a refresh, because a new token may see a different
	// path.
	cached map[string]string
	loaded bool

	// mixUp remembers whether this mount is being read as the wrong KV
	// version, asked once and then reported on every lookup.
	mixUpOnce sync.Once
	mixUp     error
}

// NewVault builds a Vault backend, or reports what it is missing.
//
// Refused at construction rather than at first use. A source that is registered
// and then reports "not configured" for every variable is a source in the list
// that can never answer, and the list is what somebody reads to decide where to
// put a value.
func NewVault(cfg VaultConfig) (*Source, error) {
	if strings.TrimSpace(cfg.Address) == "" {
		return nil, wrap(ErrNotConfigured, "Vault needs an address (VAULT_ADDR)")
	}
	if _, err := url.Parse(cfg.Address); err != nil {
		return nil, wrap(ErrNotConfigured, "the Vault address is not a URL")
	}
	if cfg.Token == "" && cfg.RoleID == "" {
		return nil, wrap(ErrNotConfigured,
			"Vault needs either a token (VAULT_TOKEN) or an AppRole role id and secret id")
	}
	if cfg.RoleID != "" && cfg.SecretID == "" {
		return nil, wrap(ErrNotConfigured, "the Vault AppRole role id has no secret id")
	}
	if strings.TrimSpace(cfg.Path) == "" {
		return nil, wrap(ErrNotConfigured, "Vault needs the path of the secret holding the variables")
	}

	return New(newVaultBackend(cfg)), nil
}

// newVaultBackend applies the defaults.
//
// Split from NewVault so that the defaults are one thing and the refusals are
// another. It is also what the conformance run builds, so that the suite
// exercises a backend configured exactly as the constructor configures one
// rather than a hand-assembled struct that could differ.
func newVaultBackend(cfg VaultConfig) *VaultBackend {
	cfg.Address = strings.TrimRight(cfg.Address, "/")
	if cfg.Mount == "" {
		cfg.Mount = "secret"
	}
	if cfg.AppRolePath == "" {
		cfg.AppRolePath = "approle"
	}
	if cfg.Field == "" {
		cfg.Field = "value"
	}
	return &VaultBackend{cfg: cfg, token: cfg.Token}
}

func (v *VaultBackend) Describe() string {
	where := v.cfg.Address + " (" + v.cfg.Mount + "/" + strings.Trim(v.cfg.Path, "/") + ")"
	if v.cfg.Namespace != "" {
		where = v.cfg.Address + " (" + v.cfg.Namespace + ": " + v.cfg.Mount + "/" +
			strings.Trim(v.cfg.Path, "/") + ")"
	}
	return "HashiCorp Vault at " + where
}

// Reach asks Vault whether it is up and unsealed.
//
// sys/health is unauthenticated and cheap, and it answers the two questions
// that actually stop a lookup: whether anything is listening, and whether the
// vault is sealed. A sealed Vault is reachable, answers, and can serve nothing,
// and reporting it as "connection refused" would send an operator to the
// network rather than to the unseal keys.
//
// The token is not checked here. A token that has expired is discovered on the
// first lookup and handled by the refresh rule, and checking it here would cost
// a second round trip to learn something the lookup learns for free.
func (v *VaultBackend) Reach(ctx context.Context) error {
	resp, err := do(ctx, request{
		method:  "GET",
		url:     v.cfg.Address + "/v1/sys/health",
		headers: v.headers(""),
		// standbyok and performancestandbyok stop a standby node answering 429,
		// which is a healthy node that can still serve reads.
		query: map[string]string{"standbyok": "true", "perfstandbyok": "true"},
	})
	if err != nil {
		return fmt.Errorf("cannot be reached: %s", err)
	}

	// Vault encodes its state in the status code rather than only in the body.
	switch resp.status {
	case 200, 429, 472, 473:
	case 501:
		return fmt.Errorf("is not initialised")
	case 503:
		return fmt.Errorf("is sealed")
	default:
		return fmt.Errorf("answered %d to a health check", resp.status)
	}
	return v.checkMountVersion(ctx)
}

// checkMountVersion catches the misconfiguration that is otherwise silent.
//
// Reading a KV version 2 mount as version 1 succeeds and returns the envelope
// rather than the secret, so every variable comes back absent. Reading a
// version 1 mount as version 2 asks for a path with a data/ segment that is not
// there, so it 404s, and a 404 is a miss. Both of them present as "the variable
// is not set" for a variable that is plainly there in the UI, which is a
// question somebody can spend an afternoon on.
//
// So it is asked once, at the point where the answer becomes the reason the
// source is unavailable, and AF-SEC-001 prints it. A lookup could not do this:
// it would be a second round trip per variable to detect something that is a
// property of the mount and not of the variable.
//
// Best effort, deliberately. The endpoint needs a token that can read the
// mount's metadata and a policy may not grant that, and a diagnostic that
// refuses to let the source work when it cannot run is a diagnostic that has
// become a gate. An error here means "could not tell", and could not tell is
// not a reason to stop.
func (v *VaultBackend) checkMountVersion(ctx context.Context) error {
	v.mu.Lock()
	token := v.token
	v.mu.Unlock()
	if token == "" {
		return nil
	}

	resp, err := do(ctx, request{
		method:  "GET",
		url:     v.cfg.Address + "/v1/sys/internal/ui/mounts/" + v.cfg.Mount,
		headers: v.headers(token),
	})
	if err != nil || resp.status != 200 {
		return nil
	}
	var payload struct {
		Data struct {
			Type    string            `json:"type"`
			Options map[string]string `json:"options"`
		} `json:"data"`
	}
	if resp.decode(&payload) != nil || payload.Data.Type != "kv" {
		return nil
	}

	// An absent version option means version 1: that is how a mount created
	// before version 2 existed reports itself.
	actual := payload.Data.Options["version"]
	if actual == "" {
		actual = "1"
	}
	configured := "2"
	if v.cfg.KVv1 {
		configured = "1"
	}
	if actual == configured {
		return nil
	}
	return fmt.Errorf(
		"the mount %s is KV version %s and this source is configured to read version %s, "+
			"which would report every variable as absent",
		v.cfg.Mount, actual, configured)
}

func (v *VaultBackend) headers(token string) map[string]string {
	h := map[string]string{"Accept": "application/json"}
	if token != "" {
		h["X-Vault-Token"] = token
	}
	if v.cfg.Namespace != "" {
		h["X-Vault-Namespace"] = v.cfg.Namespace
	}
	return h
}

// Fetch reads a variable.
func (v *VaultBackend) Fetch(ctx context.Context, name string) (string, bool, error) {
	if v.cfg.PathPerName {
		data, err := v.read(ctx, strings.Trim(v.cfg.Path, "/")+"/"+name)
		if err != nil {
			return "", false, err
		}
		if data == nil {
			// A path that is not there is a miss, not a failure. This is the
			// ordinary case for a variable that lives somewhere else in the
			// chain, so it must fall through rather than stop it.
			return "", false, nil
		}
		value, ok := data[v.cfg.Field]
		return value, ok, nil
	}

	data, err := v.load(ctx)
	if err != nil {
		return "", false, err
	}
	value, ok := data[name]
	return value, ok, nil
}

// load reads the one secret holding every variable, once.
func (v *VaultBackend) load(ctx context.Context) (map[string]string, error) {
	v.mu.Lock()
	loaded, cached := v.loaded, v.cached
	v.mu.Unlock()
	if loaded {
		return cached, nil
	}

	data, err := v.read(ctx, strings.Trim(v.cfg.Path, "/"))
	if err != nil {
		return nil, err
	}
	if data == nil {
		// The path does not exist. Cached as empty rather than left unloaded,
		// so twenty declared variables produce one request and one answer
		// instead of twenty identical 404s.
		data = map[string]string{}
	}

	v.mu.Lock()
	v.cached, v.loaded = data, true
	v.mu.Unlock()
	return data, nil
}

// read fetches one path and flattens it to strings.
//
// Nil data with a nil error means the path is not there.
func (v *VaultBackend) read(ctx context.Context, path string) (map[string]string, error) {
	v.mu.Lock()
	token := v.token
	v.mu.Unlock()

	full := v.cfg.Address + "/v1/" + v.cfg.Mount + "/" + path
	if !v.cfg.KVv1 {
		// KV version 2 puts reads under a data/ segment, which is the single
		// most common thing to get wrong about this API: without it the request
		// succeeds against the metadata endpoint and returns a document with no
		// secret in it.
		full = v.cfg.Address + "/v1/" + v.cfg.Mount + "/data/" + path
	}

	resp, err := do(ctx, request{method: "GET", url: full, headers: v.headers(token)})
	if err != nil {
		return nil, fmt.Errorf("cannot be reached: %s", err)
	}

	switch {
	case resp.status == 404:
		// A 404 is a miss, and it is also what reading a mount as the wrong KV
		// version looks like, because the path with the data/ segment does not
		// exist on a version 1 mount and the path without it does not exist on
		// a version 2 one. Those are the same response and they mean very
		// different things, so the wrong one is ruled out before the miss is
		// reported.
		if err := v.versionMixUp(ctx, path, token); err != nil {
			return nil, err
		}
		return nil, nil
	case resp.rejected():
		return nil, wrap(ErrRejected, "Vault answered %d: %s", resp.status, vaultErrors(resp.body))
	case resp.status != 200:
		return nil, fmt.Errorf("Vault answered %d: %s", resp.status, vaultErrors(resp.body))
	}

	var payload struct {
		Data json.RawMessage `json:"data"`
	}
	if err := resp.decode(&payload); err != nil {
		return nil, err
	}
	raw := payload.Data
	if !v.cfg.KVv1 {
		var wrapped struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(payload.Data, &wrapped); err != nil {
			return nil, fmt.Errorf("Vault answered 200 without the data envelope version 2 uses; " +
				"if this mount is KV version 1, say so in the configuration")
		}
		raw = wrapped.Data
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("the secret at %s is not a set of fields", path)
	}
	out := make(map[string]string, len(fields))
	for k, val := range fields {
		// A value written through the UI as a number or a boolean comes back as
		// one, and an environment variable is a string. Rendered rather than
		// refused: refusing would make a working Vault unusable because
		// somebody typed 5432 without quotes.
		switch t := val.(type) {
		case string:
			out[k] = t
		case nil:
			out[k] = ""
		case bool:
			out[k] = fmt.Sprintf("%t", t)
		case float64:
			// %v on a float64 renders 5432 as 5432 rather than 5.432e+03.
			out[k] = strings.TrimSuffix(fmt.Sprintf("%v", t), ".0")
		default:
			// An object or an array. Kept as its JSON, which is what an
			// application expecting a JSON-valued variable wants, and is at
			// least lossless for anything else.
			encoded, err := json.Marshal(t)
			if err != nil {
				return nil, fmt.Errorf("the field %q at %s cannot be rendered as a value", k, path)
			}
			out[k] = string(encoded)
		}
	}
	return out, nil
}

// versionMixUp reports a mount read as the wrong KV version, or nil.
//
// Reached only from a 404, and asked at most once per process no matter how
// many variables miss. That bound is the whole reason this is shaped as it is:
// a repository declaring twenty variables, most of which live in .env, produces
// twenty misses on a correctly configured Vault, and a probe per miss would
// double every one of them to diagnose something that is a property of the
// mount rather than of the variable.
//
// The verdict is cached including the negative, so a Vault that is configured
// correctly pays for exactly one extra request in the life of the process, and
// a Vault that is not says so on every lookup rather than only the first.
//
// This is the fallback for the check Reach already makes. Reach reads the
// mount's own metadata, which is better because it runs before anything is
// looked up, and a policy that does not grant sys/internal/ui/mounts makes it
// silently unavailable. This one needs no permission the read did not already
// need.
func (v *VaultBackend) versionMixUp(ctx context.Context, path, token string) error {
	v.mixUpOnce.Do(func() {
		other := v.cfg.Address + "/v1/" + v.cfg.Mount + "/" + path
		configured, actual := "2", "1"
		if v.cfg.KVv1 {
			other = v.cfg.Address + "/v1/" + v.cfg.Mount + "/data/" + path
			configured, actual = "1", "2"
		}
		resp, err := do(ctx, request{method: "GET", url: other, headers: v.headers(token)})
		if err != nil || resp.status != 200 {
			return
		}
		v.mixUp = fmt.Errorf(
			"the mount %s answers as KV version %s and this source is configured to read "+
				"version %s, so every variable would be reported as absent",
			v.cfg.Mount, actual, configured)
	})
	return v.mixUp
}

// Refresh logs in again through AppRole.
//
// Only AppRole. A token supplied by an operator is not ours to renew, so a
// token-configured backend reports that it cannot refresh and the first
// rejection is final.
func (v *VaultBackend) Refresh(ctx context.Context) error {
	if v.cfg.RoleID == "" {
		return fmt.Errorf("a Vault token cannot be renewed by this tool; " +
			"configure an AppRole for a credential that can be")
	}

	body, err := json.Marshal(map[string]string{
		"role_id": v.cfg.RoleID, "secret_id": v.cfg.SecretID,
	})
	if err != nil {
		return err
	}
	resp, err := do(ctx, request{
		method:  "POST",
		url:     v.cfg.Address + "/v1/auth/" + v.cfg.AppRolePath + "/login",
		headers: v.headers(""),
		body:    body,
	})
	if err != nil {
		return fmt.Errorf("cannot be reached: %s", err)
	}
	if resp.status != 200 {
		return wrap(ErrRejected, "the AppRole login answered %d: %s",
			resp.status, vaultErrors(resp.body))
	}

	var payload struct {
		Auth struct {
			ClientToken string `json:"client_token"`
		} `json:"auth"`
	}
	if err := resp.decode(&payload); err != nil {
		return err
	}
	if payload.Auth.ClientToken == "" {
		return fmt.Errorf("the AppRole login succeeded and returned no token")
	}

	v.mu.Lock()
	v.token = payload.Auth.ClientToken
	// The cache is dropped, because the point of a new token is that it may see
	// something the old one could not. Keeping it would make the refresh
	// pointless in exactly the case it exists for.
	v.cached, v.loaded = nil, false
	v.mu.Unlock()
	return nil
}

// Login acquires the first token when the source is configured with an AppRole.
//
// Called before registration rather than lazily, so that a role id and secret id
// that are wrong are reported once, at startup, rather than as a rejection on
// whichever variable happened to be resolved first.
func (v *VaultBackend) Login(ctx context.Context) error {
	if v.cfg.RoleID == "" {
		return nil
	}
	return v.Refresh(ctx)
}

// vaultErrors renders Vault's error document.
//
// Vault answers {"errors":["permission denied"]}, and that array is the
// sentence worth printing. The body is never printed wholesale, because the
// same endpoint returns the secret.
func vaultErrors(body []byte) string {
	var payload struct {
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || len(payload.Errors) == 0 {
		return "no reason given"
	}
	return strings.Join(payload.Errors, "; ")
}
