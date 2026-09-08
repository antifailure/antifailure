package env

// The sources the engine itself answers with, built in one place.
//
// af up resolves the manifest's variables against a chain, and af explain
// reports what that chain will answer. They have to build the identical one:
// a command whose job is to say where a value will come from is worse than
// useless if it describes a different lookup than the one that happens. Two
// call sites each assembling their own list is how they drift, and this file
// exists so there is only one list.

import (
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/internal/webhook"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// EnvironmentSources returns the sources that hold values the engine makes for
// an environment, in the order they go in front of the chain.
//
// Two of them today. The webhook signing secrets, derived from the environment
// identifier, so a service declaring `from: STRIPE_WEBHOOK_SECRET` receives the
// value the sender will actually sign with. And the GitHub App identity,
// generated on demand, so a service declaring `from: GITHUB_APP_PRIVATE_KEY`
// receives a key that exists for the life of this environment and nowhere
// else.
//
// The App identity is offered under the same condition as the github signing
// secret: a webhook path declared against GitHub. That is what says the App is
// being rehearsed here, and it is one rule rather than two so the two halves of
// the same App cannot be configured apart.
func EnvironmentSources(rules []schema.EgressRule, envID string, getenv func(string) string) []secrets.Source {
	var out []secrets.Source
	if provided := webhook.Secrets(rules, envID, getenv); len(provided) > 0 {
		out = append(out, secrets.NewProvidedSource(webhook.SecretsSourceName, provided))
	}
	if recipes := gitHubAppIdentity(rules, getenv); len(recipes) > 0 {
		out = append(out, secrets.NewGeneratedSource(secrets.GitHubAppIdentitySourceName, recipes))
	}
	return out
}

// gitHubAppIdentity is the recipe for the App key, when this environment
// rehearses a GitHub App and nothing else can answer for it.
//
// A key already exported wins, and it wins by this source standing aside
// rather than by copying the value. Somebody who exported one is rehearsing
// against an App they really registered, and generating over the top of that
// would hand their application an identity GitHub has never heard of. Standing
// aside leaves the shell to answer, so af explain says the value came from this
// shell's environment, which is where it came from. Copying it would have
// af explain name this source for a value it did not make, and a report that
// says a value was generated when it was read is the kind of small lie that
// costs somebody an afternoon.
func gitHubAppIdentity(rules []schema.EgressRule, getenv func(string) string) map[string]func() (string, error) {
	if !rehearsesGitHubApp(rules) {
		return nil
	}
	if getenv != nil && getenv(secrets.GitHubAppPrivateKeyEnv) != "" {
		return nil
	}
	return map[string]func() (string, error){
		secrets.GitHubAppPrivateKeyEnv: secrets.GenerateGitHubAppPrivateKey,
	}
}

// rehearsesGitHubApp reports whether any egress rule sends GitHub's callbacks
// into this environment.
func rehearsesGitHubApp(rules []schema.EgressRule) bool {
	for _, r := range rules {
		if r.WebhookPath != "" && webhook.ForHost(r.Host) == "github" {
			return true
		}
	}
	return false
}
