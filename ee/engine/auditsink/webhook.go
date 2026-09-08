package auditsink

// An HTTPS webhook, with retry and a dead letter file.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// The dead letter file is the whole reason this is more than twenty lines, and
// it is the difference between a forwarder and a claim of one.
//
// A webhook that posts once and gives up loses an entry every time the receiver
// restarts, and it loses it silently, because the engine deliberately does not
// stop for a forwarding failure. An audit stream with a hole in it that nobody
// can see is worse than no audit stream at all: the first is trusted. So the
// contract here is that an entry which cannot be delivered is written to a file
// on disk, in the same JSON the receiver would have got, and the file is
// appended to and flushed before Write returns. A hole is then a file somebody
// can replay rather than an absence nobody can name.
//
// # Why the retry is bounded and short
//
// This runs inside `af up` and `af down`, in front of a developer or in a CI
// job. Retrying for a minute would mean a SIEM restart adds a minute to every
// environment somebody creates, which is how a security feature becomes the
// thing a team turns off. Three attempts over roughly a second and a half, then
// the file. The file is not a degraded mode, it is the design: it is what makes
// the short retry safe.
//
// # What is signed and why
//
// The body carries an HMAC-SHA256 signature over the exact bytes posted, keyed
// by a shared secret, in the same shape GitHub and Stripe use. A receiver that
// accepts audit entries on an open endpoint accepts audit entries from anybody,
// and a forged entry in an audit log is worse than a missing one for the same
// reason a guessed timestamp is.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// webhookAttempts is how many times one entry is posted before it is spooled.
//
// Three, over 200ms then 600ms of backoff. The first retry covers a receiver
// that was rolling a deployment, which is the common case and is over in
// milliseconds. Anything a second and a half does not fix is an outage, and an
// outage is what the dead letter file is for.
const webhookAttempts = 3

// webhookBackoff is the first pause between attempts. It triples each time.
const webhookBackoff = 200 * time.Millisecond

// webhookTimeout bounds one POST.
const webhookTimeout = 5 * time.Second

// SignatureHeader carries the HMAC over the body.
//
// The name is ours rather than a vendor's, because a receiver written for this
// reads a documented header and one written for something else should not
// accidentally believe it verified something it did not.
const SignatureHeader = "Af-Audit-Signature"

// WebhookConfig is what an HTTPS audit webhook needs.
type WebhookConfig struct {
	// URL is where entries are posted. HTTPS only.
	URL string
	// Secret keys the HMAC over the body. Empty means the header is not sent,
	// which is permitted for a receiver on a private network that authenticates
	// some other way, and is worth a line in af doctor rather than a refusal.
	Secret string
	// Header is an extra header sent on every request, as name:value. This is
	// where a bearer token goes for the several SIEMs that authenticate that
	// way. Optional.
	Header string
	// DeadLetterFile is where an entry goes when every attempt failed. Required:
	// see the note at the top of this file for why a webhook with nowhere to
	// spool is a forwarder that loses entries silently.
	DeadLetterFile string
	// Client is injected for tests. Nil means one with the timeout above.
	Client *http.Client
	// Now is injected for tests. Nil means the wall clock.
	Now func() time.Time
	// sleep is injected so a test does not spend the backoff. Nil means
	// time.Sleep, and it is unexported so an installation cannot turn the
	// backoff off.
	sleep func(time.Duration)
}

// Webhook posts audit entries to an HTTPS endpoint.
type Webhook struct {
	cfg        WebhookConfig
	client     *http.Client
	now        func() time.Time
	sleep      func(time.Duration)
	headerName string
	headerVal  string
}

// NewWebhook builds a webhook sink, or reports what it is missing.
func NewWebhook(cfg WebhookConfig) (*Webhook, error) {
	raw := strings.TrimSpace(cfg.URL)
	if raw == "" {
		return nil, fmt.Errorf("a webhook sink needs a URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%q is not a usable webhook URL: %w", raw, err)
	}
	if u.Scheme != "https" {
		// Refused rather than downgraded, for the reason the syslog sink
		// refuses plaintext: the body is a record of who was given a copy of
		// production, and posting that over http is the thing the record exists
		// to prove is not happening.
		return nil, fmt.Errorf(
			"the webhook URL is %s and this sink is HTTPS only, because the body is the audit "+
				"record itself", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("%q names no host", raw)
	}

	spool := strings.TrimSpace(cfg.DeadLetterFile)
	if spool == "" {
		return nil, fmt.Errorf(
			"a webhook sink needs a dead letter file. Without one an entry that cannot be " +
				"delivered is lost with nothing to say so, and an audit stream with an invisible " +
				"hole in it is worse than none, because it is trusted")
	}
	if err := os.MkdirAll(filepath.Dir(spool), 0o700); err != nil {
		return nil, fmt.Errorf("the dead letter directory for %s cannot be made: %w", spool, err)
	}
	cfg.DeadLetterFile = spool

	w := &Webhook{
		cfg:    cfg,
		client: cfg.Client,
		now:    clockOf(cfg.Now),
		sleep:  cfg.sleep,
	}
	if w.client == nil {
		w.client = &http.Client{Timeout: webhookTimeout}
	}
	if w.sleep == nil {
		w.sleep = time.Sleep
	}
	if h := strings.TrimSpace(cfg.Header); h != "" {
		name, value, found := strings.Cut(h, ":")
		if !found || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("a webhook header is written as name:value")
		}
		w.headerName = strings.TrimSpace(name)
		w.headerVal = strings.TrimSpace(value)
	}
	return w, nil
}

// Name identifies the sink, without its query string.
//
// A webhook URL frequently carries the token in a query parameter, which is how
// a credential ends up in an error message, in a CI log, and in a screenshot of
// one. The scheme and host are enough to say which destination failed.
func (w *Webhook) Name() string {
	u, err := url.Parse(w.cfg.URL)
	if err != nil {
		return "the audit webhook"
	}
	return "the audit webhook at " + u.Scheme + "://" + u.Host + u.EscapedPath()
}

// Write posts one entry, spooling it on failure.
//
// The error it returns after spooling is the truth of what happened rather than
// a failure the caller must act on: the engine reports it through progress and
// carries on, which is what the interface's contract requires. Returning nil
// after spooling would be a lie in the direction that matters, because an
// operator whose SIEM has been unreachable for a day would see nothing at all.
func (w *Webhook) Write(ctx context.Context, entry extension.AuditEntry) error {
	if !permitted(ctx) {
		return nil
	}
	body, err := encode(entry, w.now())
	if err != nil {
		return err
	}

	var last error
	for attempt := range webhookAttempts {
		if attempt > 0 {
			pause := webhookBackoff * time.Duration(intPow(3, attempt-1))
			w.sleep(pause)
		}
		last = w.post(ctx, body)
		if last == nil {
			return nil
		}
		if ctx.Err() != nil {
			// The run is over. Spool rather than retrying into a cancelled
			// context, because every remaining attempt would fail identically
			// and the entry still has to land somewhere.
			break
		}
	}

	if spoolErr := w.spool(body); spoolErr != nil {
		// Both, because they are two different failures and an operator needs
		// both: the receiver is down AND the fallback did not work, which is
		// the only combination in which an entry is actually lost.
		return fmt.Errorf("forwarding to %s failed (%v) and the dead letter file could not be "+
			"written either: %w", w.Name(), last, spoolErr)
	}
	return fmt.Errorf("forwarding to %s failed after %d attempts and the entry was written to %s: %w",
		w.Name(), webhookAttempts, w.cfg.DeadLetterFile, last)
}

// post makes one attempt.
func (w *Webhook) post(ctx context.Context, body []byte) error {
	reqCtx, cancel := context.WithTimeout(ctx, webhookTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, w.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if w.headerName != "" {
		req.Header.Set(w.headerName, w.headerVal)
	}
	if w.cfg.Secret != "" {
		mac := hmac.New(sha256.New, []byte(w.cfg.Secret))
		mac.Write(body)
		req.Header.Set(SignatureHeader, "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	// Drained and closed, so the connection is reused for the next entry rather
	// than a new one being opened per action.
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// The status and nothing else. A receiver's error body can echo the
		// request, and the request is the audit entry, so quoting it here would
		// put the entry into an error message that is printed to a terminal and
		// into a CI log.
		return fmt.Errorf("the receiver answered %s", resp.Status)
	}
	return nil
}

// spool appends one entry to the dead letter file.
//
// Opened, written, synced and closed per entry rather than held open. A handle
// held across a run is a handle that loses the last entry when the process is
// killed, which is exactly the run somebody will be asking about, and this is
// at most a few entries per command.
//
// 0600, because the file holds audit records and the directory it is in is
// frequently a CI workspace that later steps can read.
func (w *Webhook) spool(body []byte) error {
	f, err := os.OpenFile(w.cfg.DeadLetterFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	line := append(append([]byte(nil), body...), '\n')
	if _, err := f.Write(line); err != nil {
		return err
	}
	// Synced before returning. The case this covers is the ordinary one: the
	// SIEM is down, the entry is spooled, and the CI runner is destroyed the
	// instant the command exits. An unsynced write in a container that is about
	// to be deleted is a write that did not happen.
	return f.Sync()
}

// intPow is the backoff multiplier, in integers, because math.Pow on a
// duration is a float conversion that rounds.
func intPow(base, exp int) int {
	out := 1
	for range exp {
		out *= base
	}
	return out
}
