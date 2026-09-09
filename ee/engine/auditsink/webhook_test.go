// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package auditsink

// The webhook sink, against a receiver that verifies the signature the way a
// real one would.
//
// The dead letter file is what most of this is about. A webhook that posts once
// and gives up loses an entry every time the receiver restarts, and it loses it
// silently, because the engine deliberately does not stop for a forwarding
// failure. An audit stream with a hole in it that nobody can see is worse than
// no audit stream at all, because the first is trusted.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// receiver is an HTTPS endpoint that keeps what it was posted.
type receiver struct {
	*httptest.Server
	mu       sync.Mutex
	bodies   [][]byte
	headers  []http.Header
	statuses []int
	// answers is the status to return per request, consumed in order. When it
	// runs out the last one repeats, so a test says "fail twice then work" as
	// three entries rather than as a counter.
	answers []int
}

// recordingHTTPS starts a TLS receiver that answers one status forever.
func recordingHTTPS(t *testing.T, status int) *receiver {
	t.Helper()
	return answeringHTTPS(t, status)
}

// answeringHTTPS starts a TLS receiver answering each status in turn.
func answeringHTTPS(t *testing.T, statuses ...int) *receiver {
	t.Helper()
	r := &receiver{answers: statuses}
	r.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.bodies = append(r.bodies, body)
		r.headers = append(r.headers, req.Header.Clone())
		status := r.answers[len(r.answers)-1]
		if len(r.bodies) <= len(r.answers) {
			status = r.answers[len(r.bodies)-1]
		}
		r.statuses = append(r.statuses, status)
		r.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *receiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies)
}

func (r *receiver) taken() ([][]byte, []http.Header) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]byte(nil), r.bodies...), append([]http.Header(nil), r.headers...)
}

// newWebhook builds a sink posting at a receiver, with the backoff removed.
func newWebhook(t *testing.T, r *receiver, cfg WebhookConfig) (*Webhook, string) {
	t.Helper()
	dead := filepath.Join(t.TempDir(), "spool", "audit-dead-letter.jsonl")
	cfg.URL = r.URL + "/audit"
	cfg.DeadLetterFile = dead
	cfg.Client = r.Client()
	cfg.Now = at(forwarded)
	cfg.sleep = func(time.Duration) {}

	w, err := NewWebhook(cfg)
	require.NoError(t, err)
	return w, dead
}

// ---------------------------------------------------------------------------
// Delivery and what proves it
// ---------------------------------------------------------------------------

func TestWebhookPostsTheEntryAsOneSignedDocument(t *testing.T) {
	t.Parallel()
	r := recordingHTTPS(t, 202)
	w, dead := newWebhook(t, r, WebhookConfig{Secret: "shhh", Header: "Authorization: Bearer tok"})

	require.NoError(t, w.Write(licensed(t), entry()))
	require.Equal(t, 1, r.count())

	bodies, headers := r.taken()
	var got map[string]any
	require.NoError(t, json.Unmarshal(bodies[0], &got))
	require.Equal(t, "golden.published", got["action"])
	require.Equal(t, "acme", got["org"])

	require.Equal(t, "application/json", headers[0].Get("Content-Type"))
	require.Equal(t, "Bearer tok", headers[0].Get("Authorization"),
		"the extra header is where a bearer token goes for the SIEMs that authenticate that way")

	// The signature is over the exact bytes posted, keyed by the shared
	// secret. A receiver that accepts audit entries on an open endpoint
	// accepts audit entries from anybody, and a forged entry in an audit log
	// is worse than a missing one.
	mac := hmac.New(sha256.New, []byte("shhh"))
	mac.Write(bodies[0])
	require.Equal(t, "sha256="+hex.EncodeToString(mac.Sum(nil)),
		headers[0].Get(SignatureHeader))

	require.NoFileExists(t, dead, "a delivered entry was spooled as well as delivered")
}

func TestWebhookWithNoSecretSendsNoSignatureRatherThanAnEmptyOne(t *testing.T) {
	t.Parallel()
	// Permitted for a receiver on a private network that authenticates some
	// other way. A signature keyed by the empty string would be worse than
	// none: it verifies, so a receiver checking it would believe it had
	// authenticated something.
	r := recordingHTTPS(t, 200)
	w, _ := newWebhook(t, r, WebhookConfig{})
	require.NoError(t, w.Write(licensed(t), entry()))

	_, headers := r.taken()
	require.Empty(t, headers[0].Get(SignatureHeader))
}

func TestWebhookRetriesATransientFailureAndDoesNotSpool(t *testing.T) {
	t.Parallel()
	// The first retry covers a receiver that was rolling a deployment, which
	// is the common case and is over in milliseconds.
	r := answeringHTTPS(t, 503, 200)
	w, dead := newWebhook(t, r, WebhookConfig{})

	require.NoError(t, w.Write(licensed(t), entry()))
	require.Equal(t, 2, r.count(), "a receiver that was restarting cost an entry")
	require.NoFileExists(t, dead)
}

// ---------------------------------------------------------------------------
// The hole is a file somebody can replay, not an absence nobody can name
// ---------------------------------------------------------------------------

func TestWebhookSpoolsAnUndeliverableEntryAndSaysWhereItWent(t *testing.T) {
	t.Parallel()
	r := recordingHTTPS(t, 500)
	w, dead := newWebhook(t, r, WebhookConfig{})

	err := w.Write(licensed(t), entry())
	require.Error(t, err, "an entry that reached nobody was reported as delivered")
	require.ErrorContains(t, err, dead,
		"the operator was not told where the entry actually went")
	require.ErrorContains(t, err, "500")
	require.Equal(t, webhookAttempts, r.count())

	// The bytes on disk are the bytes the receiver would have got, so the file
	// is replayable rather than merely a note that something was lost.
	spooled, err := os.ReadFile(dead)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(spooled)), "\n")
	require.Len(t, lines, 1)

	posted, _ := r.taken()
	require.Equal(t, string(posted[0]), lines[0],
		"the spooled entry is not what the receiver would have been sent")

	// A second failure appends rather than replacing, because a file that
	// holds only the last lost entry is a file that hides an outage.
	second := entry()
	second.Action = "environment.refused"
	require.Error(t, w.Write(licensed(t), second))

	spooled, err = os.ReadFile(dead)
	require.NoError(t, err)
	lines = strings.Split(strings.TrimSpace(string(spooled)), "\n")
	require.Len(t, lines, 2, "the dead letter file was replaced rather than appended to")
	require.Contains(t, lines[1], "environment.refused")
}

func TestTheDeadLetterFileIsNotReadableByTheRestOfTheCIWorkspace(t *testing.T) {
	t.Parallel()
	// It holds audit records, and the directory it is in is frequently a CI
	// workspace that later steps can read.
	r := recordingHTTPS(t, 500)
	w, dead := newWebhook(t, r, WebhookConfig{})
	require.Error(t, w.Write(licensed(t), entry()))

	info, err := os.Stat(dead)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestWebhookReportsBothFailuresWhenTheFallbackFailsToo(t *testing.T) {
	t.Parallel()
	// The only combination in which an entry is actually lost, so it is the
	// one that has to name both halves rather than the more recent one.
	r := recordingHTTPS(t, 500)
	w, dead := newWebhook(t, r, WebhookConfig{})
	// A directory where the file belongs. Opening it for append fails.
	require.NoError(t, os.MkdirAll(dead, 0o700))

	err := w.Write(licensed(t), entry())
	require.Error(t, err)
	require.ErrorContains(t, err, "500", "the receiver's failure was dropped")
	require.ErrorContains(t, err, "dead letter file could not be written")
}

// ---------------------------------------------------------------------------
// Refused at construction rather than at the first entry
// ---------------------------------------------------------------------------

func TestWebhookRefusesPlaintextHTTP(t *testing.T) {
	t.Parallel()
	// The body is the audit record itself, so posting it over http is the
	// thing the record exists to prove is not happening.
	_, err := NewWebhook(WebhookConfig{
		URL: "http://siem.acme.example/audit", DeadLetterFile: "/tmp/dead.jsonl",
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "HTTPS only")
}

func TestWebhookWithNowhereToSpoolIsRefused(t *testing.T) {
	t.Parallel()
	// Without a dead letter file an entry that cannot be delivered is lost
	// with nothing to say so, which is the invisible hole this sink exists to
	// avoid. Accepting the configuration and losing entries quietly would be
	// the same defect one layer down.
	_, err := NewWebhook(WebhookConfig{URL: "https://siem.acme.example/audit"})
	require.Error(t, err)
	require.ErrorContains(t, err, "dead letter file")
}

func TestWebhookRefusesAHeaderThatIsNotNameAndValue(t *testing.T) {
	t.Parallel()
	_, err := NewWebhook(WebhookConfig{
		URL: "https://siem.acme.example/audit", DeadLetterFile: "/tmp/dead.jsonl",
		Header: "Bearer tok",
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "name:value")
}

func TestWebhookNameCarriesNoCredential(t *testing.T) {
	t.Parallel()
	// A webhook URL frequently carries the token in a query parameter, which
	// is how a credential ends up in an error message, in a CI log, and in a
	// screenshot of one. The name is printed at startup and in every
	// forwarding failure, so it is the string that has to be safe.
	w, err := NewWebhook(WebhookConfig{
		URL:            "https://siem.acme.example/audit?token=s3cr3t-do-not-print",
		DeadLetterFile: filepath.Join(t.TempDir(), "dead.jsonl"),
	})
	require.NoError(t, err)
	require.NotContains(t, w.Name(), "s3cr3t-do-not-print")
	require.Equal(t, "the audit webhook at https://siem.acme.example/audit", w.Name())
}

func TestWebhookDoesNotQuoteTheReceiversBodyBackIntoTheLog(t *testing.T) {
	t.Parallel()
	// A receiver's error document can echo the request, and the request is the
	// audit entry, so quoting it would print the entry into a terminal and
	// into a CI log.
	echo := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write(body)
	}))
	t.Cleanup(echo.Close)

	dead := filepath.Join(t.TempDir(), "dead.jsonl")
	w, err := NewWebhook(WebhookConfig{
		URL: echo.URL + "/audit", DeadLetterFile: dead, Client: echo.Client(),
		Now: at(forwarded), sleep: func(time.Duration) {},
	})
	require.NoError(t, err)

	err = w.Write(licensed(t), entry())
	require.Error(t, err)
	require.NotContains(t, err.Error(), "dana@acme.example",
		"the receiver echoed the entry and it was quoted into the error")
	require.ErrorContains(t, err, "400")
}
