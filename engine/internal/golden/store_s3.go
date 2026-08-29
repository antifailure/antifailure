package golden

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// s3Store keeps goldens in an S3 bucket, or in anything that speaks the same
// API, which in practice means MinIO, R2, Spaces and a dozen others.
//
// Signature Version 4 is implemented here rather than taken from the AWS SDK,
// for the reason in store_azure.go: three operations against a stable, fully
// specified protocol are not worth a dependency tree. The signing below is
// exercised against a real server rather than against a fixture, which is the
// only test worth having for a signature: a wrong one is indistinguishable
// from a right one until something rejects it.
type s3Store struct {
	endpoint  *url.URL
	bucket    string
	prefix    string
	region    string
	accessKey string
	secretKey string
	session   string
	client    *http.Client
	label     string
	// pathStyle addresses the bucket as endpoint/bucket/key rather than
	// bucket.endpoint/key. MinIO and every other self-hosted implementation
	// need it, and so does anything reached by an IP address, because a
	// hostname cannot have a bucket prefixed onto an address.
	pathStyle bool
}

// newS3Store reads s3://bucket/prefix, or a full https URL for a server that
// is not AWS.
//
// The credential never comes from the URL. It comes from the environment, by
// the names the AWS tools already use, so that a machine already set up for
// the AWS CLI needs nothing else and a manifest carries no secret.
func newS3Store(raw string, getenv func(string) string) (Store, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("golden: %q is not a usable bucket URL: %w", redactURL(raw), err)
	}

	s := &s3Store{
		client:    &http.Client{Timeout: 30 * time.Minute},
		accessKey: getenv("AWS_ACCESS_KEY_ID"),
		secretKey: getenv("AWS_SECRET_ACCESS_KEY"),
		session:   getenv("AWS_SESSION_TOKEN"),
		region:    getenv("AWS_REGION"),
	}
	if s.region == "" {
		s.region = getenv("AWS_DEFAULT_REGION")
	}
	if s.region == "" {
		s.region = "us-east-1"
	}
	if s.accessKey == "" || s.secretKey == "" {
		return nil, fmt.Errorf(
			"golden: an s3 golden store signs its requests with AWS_ACCESS_KEY_ID and " +
				"AWS_SECRET_ACCESS_KEY, and one of them is not set on this machine. " +
				"They are read from the environment rather than from the manifest, " +
				"because a manifest is committed")
	}

	switch u.Scheme {
	case "s3":
		s.bucket = u.Host
		s.prefix = strings.Trim(u.Path, "/")
		s.endpoint = &url.URL{
			Scheme: "https",
			Host:   fmt.Sprintf("s3.%s.amazonaws.com", s.region),
		}
	case "http", "https":
		// A full URL points at a server that is not AWS, and the first path
		// segment is the bucket. Path style, because that is what those
		// servers serve and because an endpoint given as an address cannot
		// take a bucket prefix.
		parts := strings.SplitN(strings.Trim(u.Path, "/"), "/", 2)
		if parts[0] == "" {
			return nil, fmt.Errorf(
				"golden: %s names no bucket. For a server that is not AWS the URL is "+
					"https://<host>/<bucket>", redactURL(raw))
		}
		s.bucket = parts[0]
		if len(parts) > 1 {
			s.prefix = strings.Trim(parts[1], "/")
		}
		s.endpoint = &url.URL{Scheme: u.Scheme, Host: u.Host}
		s.pathStyle = true
	default:
		return nil, fmt.Errorf(
			"golden: an s3 storage_url is s3://<bucket>/<prefix>, or the https URL of a "+
				"server that speaks the same API, and this one is %q", u.Scheme)
	}
	if s.bucket == "" {
		return nil, fmt.Errorf("golden: %s names no bucket", redactURL(raw))
	}
	s.label = fmt.Sprintf("s3://%s/%s", s.bucket, s.prefix)
	return s, nil
}

func (s *s3Store) Name() string { return "the bucket " + s.label }

func (s *s3Store) key(name string) string {
	if s.prefix == "" {
		return name
	}
	return s.prefix + "/" + name
}

// requestURL builds the URL for a key, in whichever addressing style this
// endpoint uses.
func (s *s3Store) requestURL(key string, query url.Values) *url.URL {
	u := *s.endpoint
	if s.pathStyle {
		u.Path = "/" + s.bucket
		if key != "" {
			u.Path += "/" + key
		}
	} else {
		u.Host = s.bucket + "." + s.endpoint.Host
		u.Path = "/"
		if key != "" {
			u.Path = "/" + key
		}
	}
	if query != nil {
		u.RawQuery = query.Encode()
	}
	return &u
}

func (s *s3Store) Put(ctx context.Context, name string, _ int64, body io.Reader) error {
	// Read into memory rather than streamed, because SigV4 signs a hash of the
	// payload and the alternative is the chunked streaming variant, which is
	// four times the protocol for a file that is already going to be held in a
	// buffer somewhere on its way to the network. A golden dump is large but
	// it is a dump of a SUBSET, which is the size this feature exists to
	// produce; a caller with a two hundred gigabyte dump has a different
	// problem and should be told rather than quietly OOM.
	buf, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("golden: reading %s to upload: %w", name, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		s.requestURL(s.key(name), nil).String(), bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("golden: %w", err)
	}
	req.ContentLength = int64(len(buf))
	req.Header.Set("Content-Type", "application/octet-stream")
	if err := s.sign(req, buf); err != nil {
		return err
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

func (s *s3Store) Get(ctx context.Context, name string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		s.requestURL(s.key(name), nil).String(), nil)
	if err != nil {
		return nil, fmt.Errorf("golden: %w", err)
	}
	if err := s.sign(req, nil); err != nil {
		return nil, err
	}
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

// s3Listing is the ListObjectsV2 response.
type s3Listing struct {
	Contents []struct {
		Key          string `xml:"Key"`
		Size         int64  `xml:"Size"`
		LastModified string `xml:"LastModified"`
	} `xml:"Contents"`
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
}

func (s *s3Store) List(ctx context.Context, prefix string) ([]Object, error) {
	var out []Object
	token := ""
	for {
		q := url.Values{}
		q.Set("list-type", "2")
		if p := s.key(prefix); p != "" {
			q.Set("prefix", p)
		}
		if token != "" {
			q.Set("continuation-token", token)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			s.requestURL("", q).String(), nil)
		if err != nil {
			return nil, fmt.Errorf("golden: %w", err)
		}
		if err := s.sign(req, nil); err != nil {
			return nil, err
		}
		resp, err := s.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("golden: listing %s: %w", s.label, err)
		}
		if resp.StatusCode/100 != 2 {
			err = s.statusError("listing "+s.label, resp)
			_ = resp.Body.Close()
			return nil, err
		}
		var doc s3Listing
		err = xml.NewDecoder(resp.Body).Decode(&doc)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("golden: the bucket listing did not parse: %w", err)
		}
		for _, c := range doc.Contents {
			name := c.Key
			if s.prefix != "" {
				name = strings.TrimPrefix(strings.TrimPrefix(name, s.prefix), "/")
			}
			modified, _ := time.Parse(time.RFC3339, c.LastModified)
			out = append(out, Object{Name: name, Size: c.Size, Modified: modified.UTC()})
		}
		// Paged for the same reason the Azure listing is: a bucket that has
		// been running for a year holds more than one page, and a client that
		// reads the first one reports a store with no goldens in it.
		if !doc.IsTruncated || doc.NextContinuationToken == "" {
			return out, nil
		}
		token = doc.NextContinuationToken
	}
}

func (s *s3Store) Delete(ctx context.Context, name string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		s.requestURL(s.key(name), nil).String(), nil)
	if err != nil {
		return fmt.Errorf("golden: %w", err)
	}
	if err := s.sign(req, nil); err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("golden: removing %s: %w", name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	// S3 answers 204 whether or not the key was there, which is the semantics
	// this interface wants anyway.
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode/100 == 2 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return s.statusError("removing "+name, resp)
}

func (s *s3Store) statusError(what string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	detail := strings.TrimSpace(string(body))
	if resp.StatusCode == http.StatusForbidden {
		detail += " (a 403 here is the signature or the policy: a wrong secret key, " +
			"a clock more than fifteen minutes out, or a key with no access to this bucket)"
	}
	return fmt.Errorf("golden: %s: %s: %s", what, resp.Status, detail)
}

// sign adds an AWS Signature Version 4 authorization header.
//
// The specification is followed rather than approximated, because every step of
// it is load bearing and a signature that is wrong in any one of them is
// rejected identically to one that is wrong in all of them:
//
//   - the payload hash is a header AND part of the signed set, which is what
//     stops a body being swapped under a signature;
//   - headers are signed lowercased, sorted, with runs of whitespace in the
//     values collapsed, so that a proxy reformatting them does not break it;
//   - the query string is canonicalised with its own escaping rules, which are
//     not url.Values.Encode's, because a space is %20 here and a plus there;
//   - the credential scope pins the date, the region and the service, so a
//     signature cannot be replayed against another region tomorrow.
func (s *s3Store) sign(req *http.Request, payload []byte) error {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateOnly := now.Format("20060102")

	sum := sha256.Sum256(payload)
	payloadHash := hex.EncodeToString(sum[:])

	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	if req.Host == "" {
		req.Host = req.URL.Host
	}
	if s.session != "" {
		req.Header.Set("x-amz-security-token", s.session)
	}

	signed, canonicalHeaders := canonicalHeaders(req)
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalPath(req.URL),
		canonicalQuery(req.URL),
		canonicalHeaders,
		signed,
		payloadHash,
	}, "\n")

	crSum := sha256.Sum256([]byte(canonicalRequest))
	scope := strings.Join([]string{dateOnly, s.region, "s3", "aws4_request"}, "/")
	toSign := strings.Join([]string{
		"AWS4-HMAC-SHA256", amzDate, scope, hex.EncodeToString(crSum[:]),
	}, "\n")

	key := hmacSHA256([]byte("AWS4"+s.secretKey), dateOnly)
	key = hmacSHA256(key, s.region)
	key = hmacSHA256(key, "s3")
	key = hmacSHA256(key, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(key, toSign))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s.accessKey, scope, signed, signature))
	return nil
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

// canonicalHeaders returns the signed header list and the canonical block.
func canonicalHeaders(req *http.Request) (string, string) {
	names := []string{"host"}
	values := map[string]string{"host": req.Host}
	for name, vs := range req.Header {
		lower := strings.ToLower(name)
		if !strings.HasPrefix(lower, "x-amz-") && lower != "content-type" {
			continue
		}
		names = append(names, lower)
		values[lower] = strings.Join(collapse(vs), ",")
	}
	sort.Strings(names)

	var block strings.Builder
	for _, n := range names {
		block.WriteString(n)
		block.WriteString(":")
		block.WriteString(values[n])
		block.WriteString("\n")
	}
	return strings.Join(names, ";"), block.String()
}

func collapse(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = strings.Join(strings.Fields(v), " ")
	}
	return out
}

// canonicalPath escapes each path segment, leaving the separators alone.
func canonicalPath(u *url.URL) string {
	path := u.EscapedPath()
	if path == "" {
		return "/"
	}
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		// Unescaped first, because the URL may already carry escapes and
		// escaping twice produces a path the server does not have.
		raw, err := url.PathUnescape(seg)
		if err != nil {
			raw = seg
		}
		segments[i] = awsEscape(raw)
	}
	return strings.Join(segments, "/")
}

// canonicalQuery sorts and escapes the query with AWS's rules.
func canonicalQuery(u *url.URL) string {
	values := u.Query()
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		vs := append([]string(nil), values[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, awsEscape(k)+"="+awsEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

// awsEscape percent encodes everything outside the unreserved set.
//
// Not url.QueryEscape, which writes a space as + and leaves some characters
// alone that this has to encode. The difference is invisible until a key with
// a space in it fails to sign.
func awsEscape(s string) string {
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.~"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if strings.IndexByte(unreserved, c) >= 0 {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}
