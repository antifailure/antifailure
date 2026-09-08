package secrets

// Values the engine makes up for an environment, rather than looking up.
//
// The webhook signing secrets next door are derived from the environment
// identifier and cost nothing to compute, so they are handed over as a plain
// map. An identity is different: it is a keypair, generating one is expensive
// enough to notice, and most environments never ask for it. So this source
// holds the recipe rather than the value and runs it the first time somebody
// looks the name up.
//
// The laziness is the point rather than an optimisation. A manifest with a
// GitHub webhook path that declares no App variable would otherwise pay for a
// 2048 bit key on every af up, af explain and af webhook trigger, to hand it to
// nobody.

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"sync"
)

// GeneratedSource holds values the engine will compute on demand.
//
// One computation per name per source, cached, because a source consulted
// twice must answer the same thing twice: a key that changed between af up
// resolving it and af explain reporting it would be two identities where the
// user was told there was one.
type GeneratedSource struct {
	Label   string
	Recipes map[string]func() (string, error)

	mu     sync.Mutex
	cached map[string]string
}

// NewGeneratedSource wraps values the engine will make when asked.
func NewGeneratedSource(label string, recipes map[string]func() (string, error)) *GeneratedSource {
	return &GeneratedSource{Label: label, Recipes: recipes, cached: map[string]string{}}
}

func (g *GeneratedSource) Name() string { return g.Label }

// Available is false when there is nothing to generate, so an environment that
// needs none of this never sees the source in a "Looked in" list.
func (g *GeneratedSource) Available(context.Context) (bool, string) {
	return len(g.Recipes) > 0, ""
}

func (g *GeneratedSource) Lookup(_ context.Context, name string) (Value, bool, error) {
	recipe, ok := g.Recipes[name]
	if !ok {
		return Value{}, false, nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cached == nil {
		g.cached = map[string]string{}
	}
	if v, done := g.cached[name]; done {
		return NewFrom(v, g.Name()), true, nil
	}
	v, err := recipe()
	if err != nil {
		return Value{}, false, fmt.Errorf("generating %s: %w", name, err)
	}
	g.cached[name] = v
	return NewFrom(v, g.Name()), true, nil
}

// GitHubAppPrivateKeyEnv is the name a manifest reads the generated App
// identity under. It is GitHub's own conventional spelling with no prefix, the
// same convention the webhook signing secrets use, so that an application
// reading it under a name of its own says `from: GITHUB_APP_PRIVATE_KEY` and
// the two sides cannot drift.
const GitHubAppPrivateKeyEnv = "GITHUB_APP_PRIVATE_KEY"

// GitHubAppIdentitySourceName is how the chain refers to this source in af
// explain and in the "Looked in" list of AF-SEC-001.
const GitHubAppIdentitySourceName = "the identity this environment generates for its GitHub App"

// GenerateGitHubAppPrivateKey makes an App identity for one environment.
//
// RSA 2048 in PKCS#8, which is what GitHub issues and what Node's
// createPrivateKey reads without argument. It is a real key because the half
// configured case is refused by the applications that read it: an App id and a
// webhook secret with no key is an endpoint that verifies deliveries and can
// do nothing with them, so a placeholder string would either be rejected at
// startup or fail later with a decoder error that names the wrong problem.
//
// It is generated rather than committed for the reason this whole path exists.
// A key written into a manifest is a key in a public repository for as long as
// the file is there, and this one was: 2048 bits of RSA sat in this
// repository's own antifailure.yaml, under `value:`, base64 encoded so that
// the validator's refusal of a literal beginning with BEGIN did not see it.
//
// Fresh per environment rather than derived from the environment identifier.
// Nothing outside the environment ever needs to reproduce it, so there is
// nothing to be gained by making it predictable and something to lose.
func GenerateGitHubAppPrivateKey() (string, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), nil
}
