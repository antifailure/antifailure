package golden

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
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
type Store interface {
	// Name identifies the store for a message.
	Name() string
	// Put writes an object, replacing one of the same name.
	Put(ctx context.Context, name string, size int64, body io.Reader) error
	// Get opens an object. A name that is not there returns ErrNotFound, so a
	// caller can tell "no golden published yet" from "the store is broken",
	// which are the same HTTP status on more than one service.
	Get(ctx context.Context, name string) (io.ReadCloser, error)
	// List returns the objects under a prefix.
	List(ctx context.Context, prefix string) ([]Object, error)
	// Delete removes an object. Removing one that is not there succeeds,
	// because a retry after a timeout must not fail on the work it already did.
	Delete(ctx context.Context, name string) error
}

// Object is one thing in a store.
type Object struct {
	Name     string
	Size     int64
	Modified time.Time
}

// ErrNotFound is returned by Get for an object that is not there.
var ErrNotFound = fmt.Errorf("golden: no such object")

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
)

// OpenStore builds a store from the manifest's storage and storage_url.
//
// A URL rather than a set of separate settings, because every one of these
// services already has a URL form that carries the account, the container and
// the credential, and splitting it into fields would mean a manifest that
// holds a credential. The credential stays in the environment: the URL names
// the variable holding it, or carries a signature that is itself supplied
// through one.
func OpenStore(kind Kind, storageURL string, getenv func(string) string) (Store, error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
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
	default:
		return nil, fmt.Errorf(
			"golden: %q is not a storage kind; it is one of local, azure_blob, s3", kind)
	}
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
