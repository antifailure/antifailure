package main

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTripwireInspectsEncodedQueryAndBodies(t *testing.T) {
	key := liveKey()
	for _, tc := range []struct{ name, query, contentType, body string }{
		{"encoded query", "token=" + strings.ReplaceAll(url.QueryEscape(key), "_", "%5F"), "", ""},
		{"form", "", "application/x-www-form-urlencoded", "token=" + strings.ReplaceAll(url.QueryEscape(key), "_", "%5F")},
		{"json", "", "application/json", `{"token":"` + strings.ReplaceAll(key, "_", `\u005f`) + `"}`},
		{"multipart", "", "multipart/form-data; boundary=boundary", "--boundary\r\nContent-Disposition: form-data; name=token\r\n\r\n" + key + "\r\n--boundary--\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, "http://example.com/?"+tc.query, strings.NewReader(tc.body))
			require.NoError(t, err)
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}
			found, err := (&proxy{}).tripwire(req, "example.com")
			require.NoError(t, err)
			require.NotEmpty(t, found)
			if tc.body != "" {
				replayed, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.Equal(t, tc.body, string(replayed))
			}
		})
	}
}

func TestTripwireRefusesUninspectableBody(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "http://example.com", strings.NewReader(strings.Repeat("x", (8<<20)+1)))
	require.NoError(t, err)
	_, err = (&proxy{}).tripwire(req, "example.com")
	require.ErrorContains(t, err, "inspection limit")
}
