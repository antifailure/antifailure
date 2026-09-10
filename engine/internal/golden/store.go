package golden

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// A Store is where a golden's dump and its attestation live when they live
// somewhere other than the machine that made them.
//
// The reason to have one at all is that a golden made on a laptop cannot be
// branched by a runner, and a fleet that refreshes production once per runner
// is a fleet that reads production once per runner. One machine refreshes and
// publishes; the rest pull what it published.
//
// The attestation travels beside the dump and is checked before the dump is
// used. That ordering is the whole point: a dump on its own is a database
// somebody could have put anything in, and the signed statement of what the
// verification scan found is what makes it a golden rather than a file.
//
// The interface itself lives in engine/pkg/extension, and these are aliases
// rather than a second declaration.
//
// A store is one of the things a build outside this repository may add, and an
// interface declared in an internal package is one such a build cannot name,
// let alone implement. Declaring it twice and adapting between them would work
// and would be two definitions to keep in step, which is exactly how a
// signature drifts. The alias is an alias and not a wrapper for the same
// reason engine/internal/secrets keeps the name secrets.Value for a type that
// lives in engine/pkg/secret: they are the same type to the compiler, so every
// call site, struct field and type assertion in the engine keeps working and
// no conversion exists to forget.
type Store = extension.ObjectStore

// Object is one thing in a store.
type Object = extension.StoredObject

// ErrNotFound is returned by Get for an object that is not there.
//
// The same value a store outside this module returns, because that store
// cannot import this package to name a sentinel declared here.
var ErrNotFound = extension.ErrObjectNotFound

// Kind names a storage backend, matching the manifest's values.
type Kind string

const (
	// KindLocal is a directory on this machine, or on anything mounted into
	// it. Unglamorous and the right answer for a shared runner with a volume.
	KindLocal Kind = "local"
	// KindAzureBlob is an Azure Blob container, addressed by a container URL
	// carrying a shared access signature.
	KindAzureBlob Kind = "azure_blob"
	// KindS3 is an S3 bucket, or anything that speaks the same API.
	KindS3 Kind = "s3"
	// KindGCS is a Google Cloud Storage bucket, addressed through the JSON
	// API. MIT and here rather than in ee/ for the reason its two peers are:
	// one developer with their own account and their own card is not an
	// enterprise customer.
	KindGCS Kind = "gcs"
)

// OpenStore builds a store from the manifest's storage and storage_url.
//
// A URL rather than a set of separate settings, because every one of these
// services already has a URL form that carries the account, the container and
// the credential, and splitting it into fields would mean a manifest that
// holds a credential. The credential stays in the environment: the URL names
// the variable holding it, or carries a signature that is itself supplied
// through one.
func OpenStore(
	kind Kind, storageURL string, getenv func(string) string, reg *extension.Registry,
) (Store, error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	if reg == nil {
		reg = extension.Default
	}
	raw := strings.TrimSpace(storageURL)
	if raw == "" {
		return nil, nil
	}
	// A URL written as $VARIABLE or ${VARIABLE} is read from the environment,
	// so that a container SAS or a bucket URL with a credential in it never
	// has to be committed. This is the same rule source_url_env follows, in
	// the form a URL can carry.
	if name, ok := envRef(raw); ok {
		value := getenv(name)
		if value == "" {
			return nil, fmt.Errorf(
				"golden: database.golden.storage_url names the environment variable %s "+
					"and it is not set on this machine", name)
		}
		raw = value
	}

	switch kind {
	case "", KindLocal:
		return newLocalStore(raw)
	case KindAzureBlob:
		return newAzureStore(raw)
	case KindS3:
		return newS3Store(raw, getenv)
	case KindGCS:
		return newGCSStore(raw, getenv)
	default:
		// Consulted after the built-in kinds and never before them, so a
		// registration adds a place to publish and can never take over one of
		// these. A registration under a built-in name is refused where the
		// registry is validated rather than silently losing to this switch.
		if s, ok := reg.GoldenStoreNamed(string(kind)); ok {
			store, err := s.Open(extension.ObjectStoreConfig{URL: raw, Getenv: getenv})
			if err != nil {
				return nil, err
			}
			if store == nil {
				// A nil interface here would reach every call site's nil
				// guard as non-nil and fail inside a method. The engine's own
				// providers assign, check and return an explicit nil for the
				// same reason; this is that check for a store it did not
				// write.
				return nil, fmt.Errorf(
					"golden: the registered store %q returned no store and no error", kind)
			}
			return store, nil
		}
		return nil, fmt.Errorf(
			"golden: %q is not a storage kind; it is one of %s",
			kind, strings.Join(storageKinds(reg), ", "))
	}
}

// storageKinds lists every kind this build can open, built-in and registered.
//
// The refusal says what there IS rather than only what there is not, because a
// build that registered a store and then misspelled it in the manifest is
// otherwise told the name is wrong by a message that does not mention the
// store it has.
func storageKinds(reg *extension.Registry) []string {
	out := []string{
		string(KindLocal), string(KindAzureBlob), string(KindS3), string(KindGCS),
	}
	return append(out, reg.GoldenStoreNames()...)
}

// envRef reads $NAME or ${NAME}, and reports whether the whole string was one.
func envRef(s string) (string, bool) {
	if !strings.HasPrefix(s, "$") {
		return "", false
	}
	name := strings.TrimPrefix(s, "$")
	name = strings.TrimSuffix(strings.TrimPrefix(name, "{"), "}")
	if name == "" || strings.ContainsAny(name, " /:") {
		return "", false
	}
	return name, true
}

// DumpName and AttestationName are where a version's two objects live.
//
// One directory per version, so that a listing of the store reads as a list of
// versions, and so that removing a version is removing a prefix rather than
// remembering which two names belonged together.
func DumpName(version string) string        { return version + "/dump.pgcustom" }
func AttestationName(version string) string { return version + "/attestation.json" }

// VersionsIn lists the versions a store holds, newest first.
//
// A version counts only when its attestation is there. The dump is written
// first and the attestation second, so a version that has one and not the
// other is a partial upload from a run that died, and treating it as available
// would offer somebody a database with nothing to check it against.
func VersionsIn(ctx context.Context, s Store) ([]Object, error) {
	objects, err := s.List(ctx, "")
	if err != nil {
		return nil, err
	}
	dumps := map[string]Object{}
	for _, o := range objects {
		if strings.HasSuffix(o.Name, "/dump.pgcustom") {
			dumps[strings.TrimSuffix(o.Name, "/dump.pgcustom")] = o
		}
	}
	var out []Object
	for _, o := range objects {
		if !strings.HasSuffix(o.Name, "/attestation.json") {
			continue
		}
		version := strings.TrimSuffix(o.Name, "/attestation.json")
		dump, ok := dumps[version]
		if !ok {
			continue
		}
		out = append(out, Object{Name: version, Size: dump.Size, Modified: o.Modified})
	}
	// Newest first, by the attestation's time, which is the moment the version
	// became complete.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Modified.After(out[i].Modified) ||
				(out[j].Modified.Equal(out[i].Modified) && out[j].Name > out[i].Name) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

// redactURL removes anything credential shaped from a URL so that it can go in
// a message. A shared access signature is a query string, and a bucket URL can
// carry a user info section, so both go.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "the configured storage URL"
	}
	u.RawQuery = ""
	u.User = nil
	return u.String()
}
