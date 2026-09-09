package golden

import "net/http"

// SignedRequestForTest builds and signs the request an s3 store would send for
// one object, without sending it.
//
// A seam rather than a live call, because the thing being proved is the half
// this repository wrote. Whether Cloudflare, Backblaze, DigitalOcean or Wasabi
// accept the request is theirs to answer and needs an account with each, which
// section 10 of the plan forbids a test from needing. What is checkable here
// with no account at all is that the store addresses each of them the way the
// vendor's own documentation says to: the right host, path style rather than
// virtual hosted, and a credential scope naming the vendor's region.
//
// In export_test.go, so it is compiled into the test binary and into nothing
// that ships.
func SignedRequestForTest(s Store, method, name string) (*http.Request, error) {
	store, ok := s.(*s3Store)
	if !ok {
		return nil, errNotAnS3Store
	}
	req, err := http.NewRequest(method, store.requestURL(store.key(name), nil).String(), nil)
	if err != nil {
		return nil, err
	}
	if err := store.sign(req, nil); err != nil {
		return nil, err
	}
	return req, nil
}

var errNotAnS3Store = &notAnS3Store{}

type notAnS3Store struct{}

func (*notAnS3Store) Error() string {
	return "golden: this store is not the s3 store, so there is no S3 request to build"
}

// ObjectURLForTest returns the URL a gcs store addresses one object with,
// without sending anything.
//
// A seam for the same reason SignedRequestForTest is one, and for a hazard the
// emulator cannot show. objectURL percent encodes the object name because the
// JSON API reads it as ONE path segment, so the slash in gv_01/dump.pgcustom
// has to arrive as %2F or the request addresses a resource that does not exist
// and every golden reports missing. fake-gcs-server routes the raw form to the
// same object, so the round trip suite passes either way: removing the escape
// leaves TestGCSStore green. That makes this the only place the rule is
// actually checked, and it is checked with no server at all.
func ObjectURLForTest(s Store, name string) (string, error) {
	store, ok := s.(*gcsStore)
	if !ok {
		return "", errNotAGCSStore
	}
	return store.objectURL(name, nil), nil
}

var errNotAGCSStore = &notAGCSStore{}

type notAGCSStore struct{}

func (*notAGCSStore) Error() string {
	return "golden: this store is not the gcs store, so there is no object URL to build"
}
