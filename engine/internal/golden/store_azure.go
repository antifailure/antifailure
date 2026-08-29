package golden

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// azureStore keeps goldens in an Azure Blob container.
//
// It speaks the REST API directly rather than through the SDK, and that is a
// deliberate trade rather than a shortcut. The three operations needed here are
// a PUT, a GET and a list, the API for them is stable and versioned, and the
// alternative is pulling the Azure SDK and its dependency tree into a binary
// that is otherwise built from four libraries. A supply chain is a thing you
// carry forever; ninety lines of net/http is not.
//
// The container URL carries a shared access signature, which is how the
// credential stays out of the manifest: storage_url names an environment
// variable, the variable holds the signed URL, and the signature can be scoped
// to one container and an expiry. Nothing here ever sees an account key.
type azureStore struct {
	base   *url.URL
	query  string
	client *http.Client
	label  string
}

// azureAPIVersion is pinned. The service keys behaviour off it, so a version
// that floats is a client whose behaviour changes without a commit.
const azureAPIVersion = "2021-08-06"

func newAzureStore(raw string) (Store, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("golden: %q is not a usable container URL: %w", redactURL(raw), err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf(
			"golden: an azure_blob storage_url is the container's https URL, and this one is %q", u.Scheme)
	}
	if strings.Trim(u.Path, "/") == "" {
		return nil, fmt.Errorf(
			"golden: %s names no container. The URL is the CONTAINER's, "+
				"https://<account>.blob.core.windows.net/<container>?<signature>", redactURL(raw))
	}
	if u.RawQuery == "" {
		return nil, fmt.Errorf(
			"golden: %s carries no shared access signature, and this store authenticates with one. "+
				"Generate a container scoped signature with read, write, delete and list, "+
				"and put the whole URL in the environment variable storage_url names", redactURL(raw))
	}
	q := u.RawQuery
	u.RawQuery = ""
	u.Path = "/" + strings.Trim(u.Path, "/")
	return &azureStore{
		base:   u,
		query:  q,
		client: &http.Client{Timeout: 30 * time.Minute},
		label:  redactURL(raw),
	}, nil
}

func (s *azureStore) Name() string { return "the Azure Blob container " + s.label }

// blobURL builds the URL for one blob, with the signature reattached.
func (s *azureStore) blobURL(name string) string {
	u := *s.base
	u.Path = s.base.Path + "/" + name
	u.RawQuery = s.query
	return u.String()
}

func (s *azureStore) Put(ctx context.Context, name string, size int64, body io.Reader) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.blobURL(name), body)
	if err != nil {
		return fmt.Errorf("golden: %w", err)
	}
	req.Header.Set("x-ms-version", azureAPIVersion)
	req.Header.Set("x-ms-blob-type", "BlockBlob")
	req.Header.Set("Content-Type", "application/octet-stream")
	if size >= 0 {
		// Set explicitly, because a PUT with an unknown length becomes a
		// chunked request and the Blob service refuses those.
		req.ContentLength = size
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("golden: uploading %s to %s: %w", name, s.label, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return s.statusError("uploading "+name, resp)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func (s *azureStore) Get(ctx context.Context, name string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.blobURL(name), nil)
	if err != nil {
		return nil, fmt.Errorf("golden: %w", err)
	}
	req.Header.Set("x-ms-version", azureAPIVersion)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("golden: reading %s from %s: %w", name, s.label, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	if resp.StatusCode/100 != 2 {
		defer func() { _ = resp.Body.Close() }()
		return nil, s.statusError("reading "+name, resp)
	}
	return resp.Body, nil
}

// blobList is the container listing, which is XML.
type blobList struct {
	Blobs struct {
		Blob []struct {
			Name       string `xml:"Name"`
			Properties struct {
				LastModified string `xml:"Last-Modified"`
				Length       int64  `xml:"Content-Length"`
			} `xml:"Properties"`
		} `xml:"Blob"`
	} `xml:"Blobs"`
	NextMarker string `xml:"NextMarker"`
}

func (s *azureStore) List(ctx context.Context, prefix string) ([]Object, error) {
	var out []Object
	marker := ""
	for {
		u := *s.base
		u.RawQuery = s.query
		q := u.Query()
		q.Set("restype", "container")
		q.Set("comp", "list")
		if prefix != "" {
			q.Set("prefix", prefix)
		}
		if marker != "" {
			q.Set("marker", marker)
		}
		u.RawQuery = q.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("golden: %w", err)
		}
		req.Header.Set("x-ms-version", azureAPIVersion)
		resp, err := s.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("golden: listing %s: %w", s.label, err)
		}
		if resp.StatusCode/100 != 2 {
			err = s.statusError("listing "+s.label, resp)
			_ = resp.Body.Close()
			return nil, err
		}
		var doc blobList
		err = xml.NewDecoder(resp.Body).Decode(&doc)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("golden: the container listing did not parse: %w", err)
		}
		for _, b := range doc.Blobs.Blob {
			modified, _ := http.ParseTime(b.Properties.LastModified)
			out = append(out, Object{
				Name: b.Name, Size: b.Properties.Length, Modified: modified.UTC(),
			})
		}
		// Paged, because a container that has been running for a year holds
		// more than one page and a client that reads the first one reports a
		// store with no goldens in it.
		if doc.NextMarker == "" {
			return out, nil
		}
		marker = doc.NextMarker
	}
}

func (s *azureStore) Delete(ctx context.Context, name string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.blobURL(name), nil)
	if err != nil {
		return fmt.Errorf("golden: %w", err)
	}
	req.Header.Set("x-ms-version", azureAPIVersion)
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("golden: removing %s: %w", name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Removing what is not there succeeds, because a retry after a timeout
	// must not fail on the work it already did.
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode/100 == 2 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return s.statusError("removing "+name, resp)
}

// statusError turns a response into a message somebody can act on.
//
// The body matters here: the Blob service explains an expired or
// wrongly scoped signature in it, and a bare "403" sends somebody looking at
// their network.
func (s *azureStore) statusError(what string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	detail := strings.TrimSpace(string(body))
	if code := resp.Header.Get("x-ms-error-code"); code != "" {
		detail = code + ": " + detail
	}
	if resp.StatusCode == http.StatusForbidden {
		detail += " (a 403 here is almost always the shared access signature: expired, " +
			"scoped to the wrong container, or missing one of read, write, delete and list)"
	}
	return fmt.Errorf("golden: %s: %s: %s", what, resp.Status, strings.TrimSpace(detail))
}

var _ = strconv.Itoa
