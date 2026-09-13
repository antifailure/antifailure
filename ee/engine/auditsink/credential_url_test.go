// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package auditsink

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A sink's URL is configuration an operator pastes in, and a webhook URL or an
// object store URL carries a token or a user and password often enough that
// the error saying it is malformed must not be where it gets printed.

func TestAWebhookURLThatDoesNotParseIsRefusedWithoutItsCredential(t *testing.T) {
	t.Parallel()
	_, err := NewWebhook(WebhookConfig{
		URL:            "https://audit:Wh00kPass@siem.example.test:44x3/ingest?token=Tq8Zr",
		DeadLetterFile: filepath.Join(t.TempDir(), "dead.jsonl"),
	})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "Wh00kPass")
	require.NotContains(t, err.Error(), "Tq8Zr")
	require.Contains(t, err.Error(), "invalid port", "the refusal no longer says what is wrong")
}

func TestAWebhookURLWithNoHostIsRefusedWithoutItsToken(t *testing.T) {
	t.Parallel()
	_, err := NewWebhook(WebhookConfig{
		URL:            "https:///ingest?token=Hn6Tok",
		DeadLetterFile: filepath.Join(t.TempDir(), "dead.jsonl"),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "names no host")
	require.NotContains(t, err.Error(), "Hn6Tok")
}

// The constructor parsed the trimmed URL and kept the untrimmed one, and Write
// builds every request from what was kept. A URL pasted with a newline after it
// was accepted here and then failed on every delivery, quoting the whole
// address, credential included, in an error nobody was watching.
func TestAWebhookURLPastedWithSpaceAroundItIsDeliveredTo(t *testing.T) {
	t.Parallel()
	r := recordingHTTPS(t, 202)
	w, err := NewWebhook(WebhookConfig{
		URL:            "  " + r.URL + "/audit\n",
		DeadLetterFile: filepath.Join(t.TempDir(), "spool", "dead.jsonl"),
		Client:         r.Client(),
		Now:            at(forwarded),
		sleep:          func(time.Duration) {},
	})
	require.NoError(t, err)

	require.NoError(t, w.Write(licensed(t), entry()))
	require.Equal(t, 1, r.count(), "the sink accepted the URL and delivered nothing to it")
}

func TestAnObjectStoreURLThatDoesNotParseIsRefusedWithoutItsCredential(t *testing.T) {
	t.Parallel()
	_, err := NewObjectStore(ObjectStoreConfig{
		URL:    "https://AKIDEXAMPLE:Ob7Secret@minio.example.test:9x0/audit",
		Getenv: awsEnv(nil),
	})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "Ob7Secret")
	require.Contains(t, err.Error(), "invalid port", "the refusal no longer says what is wrong")
}
