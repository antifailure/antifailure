package secrets_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/antifailure/antifailure/engine/internal/secrets"
)

const plaintext = "sk_live_51NotARealKeyAtAll"

// numericVerb is assembled at run time so that the vet printf check does not
// reject the deliberate type mismatch the test needs.
var numericVerb = "%" + "d"

// Every path the standard library offers for turning a value into text must
// produce the marker. A gap in this table is a leak.
func TestValue_NeverRendersThePlaintext(t *testing.T) {
	t.Parallel()
	v := secrets.New(plaintext)

	render := map[string]func() string{
		"String":   func() string { return v.String() },
		"GoString": func() string { return v.GoString() },
		"fmt %s":   func() string { return fmt.Sprintf("%s", v) },
		"fmt %q":   func() string { return fmt.Sprintf("%q", v) },
		"fmt %v":   func() string { return fmt.Sprintf("%v", v) },
		"fmt %+v":  func() string { return fmt.Sprintf("%+v", v) },
		"fmt %#v":  func() string { return fmt.Sprintf("%#v", v) },
		"fmt %x":   func() string { return fmt.Sprintf("%x", v) },
		// Built dynamically so that vet does not reject a deliberately
		// mismatched verb. Formatter must cover every verb, including wrong ones.
		"fmt %d":     func() string { return fmt.Sprintf(numericVerb, v) },
		"fmt Print":  func() string { return fmt.Sprint(v) },
		"in a slice": func() string { return fmt.Sprintf("%v", []secrets.Value{v}) },
		"in a map":   func() string { return fmt.Sprintf("%v", map[string]secrets.Value{"k": v}) },
		"in a struct": func() string {
			return fmt.Sprintf("%+v", struct{ Token secrets.Value }{v})
		},
		"pointer": func() string { return fmt.Sprintf("%v", &v) },
	}
	for name, f := range render {
		out := f()
		require.NotContains(t, out, plaintext, "%s leaked the secret", name)
		require.Contains(t, out, secrets.Redacted, "%s did not render the marker", name)
	}
}

func TestValue_QuotedVerbRendersAQuotedMarker(t *testing.T) {
	t.Parallel()
	require.Equal(t, `"`+secrets.Redacted+`"`, fmt.Sprintf("%q", secrets.New(plaintext)))
}

func TestValue_MarshallersRenderTheMarker(t *testing.T) {
	t.Parallel()
	v := secrets.New(plaintext)

	j, err := json.Marshal(struct {
		Token secrets.Value `json:"token"`
	}{v})
	require.NoError(t, err)
	require.JSONEq(t, `{"token":"[redacted]"}`, string(j))

	y, err := yaml.Marshal(map[string]secrets.Value{"token": v})
	require.NoError(t, err)
	require.NotContains(t, string(y), plaintext)
	require.Contains(t, string(y), secrets.Redacted)
}

func TestValue_SlogRendersTheMarker(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	log.LogAttrs(context.Background(), slog.LevelInfo, "loaded",
		slog.Any("token", secrets.New(plaintext)))
	require.NotContains(t, buf.String(), plaintext)
	require.Contains(t, buf.String(), secrets.Redacted)
}

func TestValue_RevealReturnsThePlaintext(t *testing.T) {
	t.Parallel()
	require.Equal(t, plaintext, secrets.New(plaintext).Reveal())
}

func TestValue_ZeroValueIsEmptyAndSafe(t *testing.T) {
	t.Parallel()
	var v secrets.Value
	require.True(t, v.IsZero())
	require.Equal(t, "", v.Reveal())
	require.Equal(t, "", v.Fingerprint())
	require.Equal(t, 0, v.Len())
	require.Equal(t, secrets.Redacted, v.String())
	require.Equal(t, "", v.Source())
}

func TestValue_EqualComparesContents(t *testing.T) {
	t.Parallel()
	require.True(t, secrets.New("a").Equal(secrets.New("a")))
	require.False(t, secrets.New("a").Equal(secrets.New("b")))
	require.False(t, secrets.New("a").Equal(secrets.New("ab")))
	require.True(t, secrets.Value{}.Equal(secrets.Value{}))
}

func TestValue_FingerprintIsStableShortAndNotThePlaintext(t *testing.T) {
	t.Parallel()
	v := secrets.New(plaintext)
	fp := v.Fingerprint()
	require.Len(t, fp, 8)
	require.Equal(t, fp, secrets.New(plaintext).Fingerprint())
	require.NotEqual(t, fp, secrets.New(plaintext+"x").Fingerprint())
	require.False(t, strings.Contains(plaintext, fp))
}

func TestValue_SourceIsRecordedAndIsNotTheSecret(t *testing.T) {
	t.Parallel()
	v := secrets.NewFrom(plaintext, "keyring")
	require.Equal(t, "keyring", v.Source())
	require.NotContains(t, v.Source(), plaintext)
}

func TestValue_LenReportsTheByteLength(t *testing.T) {
	t.Parallel()
	require.Equal(t, len(plaintext), secrets.New(plaintext).Len())
}

func TestValue_UnmarshalJSONLoadsAValue(t *testing.T) {
	t.Parallel()
	var got struct {
		Token secrets.Value `json:"token"`
	}
	require.NoError(t, json.Unmarshal([]byte(`{"token":"abc"}`), &got))
	require.Equal(t, "abc", got.Token.Reveal())

	// A bare token with no quotes is taken verbatim.
	var raw secrets.Value
	require.NoError(t, raw.UnmarshalJSON([]byte(`abc`)))
	require.Equal(t, "abc", raw.Reveal())
}
