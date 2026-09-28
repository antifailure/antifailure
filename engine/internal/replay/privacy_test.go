package replay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEvidenceWriterRefusesEncodedCredentialsBeforePersistence(t *testing.T) {
	s := Store{Root: t.TempDir()}
	key := "sk_live_" + strings.Repeat("z", 24)
	body, _ := json.Marshal(map[string]any{"output": key})
	_, err := s.PutBlob(body)
	require.Error(t, err)
	_, err = os.Stat(filepath.Join(s.Root, "blobs"))
	require.True(t, os.IsNotExist(err))
	encoded := []byte(`{"output":"\u0073` + key[1:] + `"}`)
	_, err = s.PutBlob(encoded)
	require.Error(t, err)
	_, err = s.PutBlob([]byte("authorization: Bearer opaque-private-value"))
	require.Error(t, err)
}

func TestDynamicEvidenceRejectsCredentialFieldsAndSignedURLs(t *testing.T) {
	for _, body := range []string{`{"password":"short"}`, `{"client_secret":"opaque"}`, `{"headers":{"x-api-key":"opaque"}}`, `{"url":"https://api.example.test/?api_key=opaque"}`, `{"url":"https://api.example.test/?sig=opaque"}`} {
		require.Error(t, SafePayload(json.RawMessage(body)))
	}
	for _, body := range []string{`{"password":"[redacted]"}`, `{"token":"AF_FAKE_demo"}`, `{"inputTokens":15}`, `{"url":"https://api.example.test/?q=billing"}`} {
		require.NoError(t, SafePayload(json.RawMessage(body)))
	}
	i := validIncident()
	i.Exchanges = []Exchange{{Seq: 0, Name: "tool", Version: "1", Kind: "tool", Key: strings.Repeat("a", 64), Provenance: "recorded", Request: json.RawMessage(`{"input":{"password":"short"}}`)}}
	require.Error(t, i.Validate())
}

func TestNestedJSONTextIsInspectedBeforeAnyArtifactIsWritten(t *testing.T) {
	for _, nested := range []string{`{"access_token":"opaque-private-value"}`, `{"password":"short"}`, `{"\u0070assword":"short"}`, `{"password":"short"`} {
		body, err := json.Marshal(map[string]string{"body": nested})
		require.NoError(t, err)
		require.Error(t, SafePayload(body))
		_, err = (Store{Root: t.TempDir()}).PutBlob(body)
		require.Error(t, err)
	}
}
