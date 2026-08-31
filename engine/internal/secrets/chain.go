package secrets

// The chain every local lookup uses, built in one place.
//
// It was built in three: af up assembled it in the environment orchestrator, af
// explain assembled it again in the CLI, and the comment beside the second copy
// said out loud what the risk was, that explain "has to build the identical
// chain or it answers a question about a different lookup than the one that
// will actually happen". Two copies that agree today are two copies that can
// disagree tomorrow, and the symptom is the worst kind: a command whose entire
// job is to tell you where a value will come from, telling you about somewhere
// else.
//
// So there is one constructor and the callers pass what actually differs, which
// is the repository root, how to read the environment, and which extension
// registry is in play.

import (
	"path/filepath"

	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// LocalChain returns the sources a local lookup asks, in order.
//
// Most specific first. An explicit shell export beats a file, because somebody
// who typed it meant it and is usually debugging. A file beats the encrypted
// store, because a repository's .env is checked out with the branch. The
// keyring is last of the local sources because it is the long-lived default the
// others exist to override, and anything an enterprise build registered comes
// after all of them for the same reason.
func LocalChain(root string, getenv func(string) string, reg *extension.Registry) *Chain {
	local := []Source{
		&EnvSource{
			Label: "this shell's environment",
			Getenv: func(name string) (string, bool) {
				v := getenv(name)
				return v, v != ""
			},
		},
		NewDotEnvSource(filepath.Join(root, ".env")),
		NewFileStore(
			filepath.Join(root, ".antifailure", "secrets.enc"),
			StorePassphrase(getenv),
		),
		NewKeyringSource(NewSystemKeyring(), DefaultKeyringService),
	}
	return NewChain(append(local, Registered(reg)...)...)
}
