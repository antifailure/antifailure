package secrets

// Sources that something outside the engine registered.
//
// The engine's four built-in sources are all local: a shell, a file in the
// repository, an encrypted file beside it, and the workstation's keyring. An
// organization that keeps credentials in Vault or in a cloud secret manager
// needs a fifth kind, and that code cannot live here. It carries a cloud SDK's
// worth of dependency, it is licensed differently, and the module it lives in
// deliberately cannot import this package: engine/internal is unimportable from
// outside engine/..., which is the wall that makes the enterprise edition a
// separate module rather than a build tag somebody can flip.
//
// So it arrives through engine/pkg/extension, where the interface is made of
// standard library types only, and this file is the single place a plain string
// from outside becomes a redacting Value. One conversion point rather than one
// per adapter: an adapter author cannot forget to redact, because an adapter
// author never touches Value at all.

import (
	"context"

	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// Registered adapts every source in a registry into a Source.
//
// Appended to the end of the chain by every caller that builds one, so a
// registered source is asked only after each local one has declined. That is
// the same "most specific first" rule the built-in order follows: an export
// somebody typed is for this run, a .env is for this repository, and the
// company secret manager is the long-lived default those two exist to override.
// Asking the central store first would mean a developer could not try a
// different key without changing what every colleague resolves.
//
// A nil registry, and a registry with nothing in it, both return nil. That is
// the community edition, and it produces exactly the chain that existed before
// this file did.
func Registered(reg *extension.Registry) []Source {
	if reg == nil {
		return nil
	}
	registered := reg.SecretSources()
	if len(registered) == 0 {
		return nil
	}
	out := make([]Source, 0, len(registered))
	for _, src := range registered {
		out = append(out, &extensionSource{src: src})
	}
	return out
}

// extensionSource is one registered source, seen as a Source.
type extensionSource struct{ src extension.SecretSource }

func (e *extensionSource) Name() string { return e.src.Name() }

// Available passes the reason through unchanged.
//
// Unchanged, and that is deliberate. The reason is written by the adapter,
// which is the only thing that knows whether the token expired, the network is
// unreachable, or the licence lapsed, and it is printed verbatim in AF-SEC-001
// next to the source's name. Rewording it here would cost the one detail that
// makes the message worth reading.
func (e *extensionSource) Available(ctx context.Context) (bool, string) {
	return e.src.Available(ctx)
}

// Lookup wraps the plaintext in a Value tagged with the source.
//
// This is the boundary. A string goes in and a Value comes out, and from here
// on the secret cannot be printed by an fmt verb, marshalled into JSON or YAML,
// or attached to a log record without somebody having written Reveal.
func (e *extensionSource) Lookup(ctx context.Context, name string) (Value, bool, error) {
	raw, found, err := e.src.Lookup(ctx, name)
	if err != nil {
		return Value{}, false, err
	}
	if !found {
		return Value{}, false, nil
	}
	return NewFrom(raw, e.Name()), true, nil
}
