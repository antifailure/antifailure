// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package cloudauth

// The Google assertion, signed with a real key and verified with its public
// half, so RS256, the base64url encoding of each segment and the exact bytes
// that are signed are proved rather than assumed. A signature Google refuses
// looks identical to a wrong service account from the outside.

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGCPAssertionCarriesTheScopeItIsAskedFor(t *testing.T) {
	// The scope is a parameter because a database provider asks for a different
	// one from a secret store. A signer that ignored it and signed the constant
	// would produce an assertion Google accepts and a token that cannot reach
	// the thing the caller asked about, which is a permission error somebody
	// would look for in IAM.
	key, keyJSON := serviceAccountKey(t)
	account, err := ParseGCPServiceAccount(keyJSON)
	require.NoError(t, err)

	const scope = "https://www.googleapis.com/auth/sqlservice.admin"
	assertion, err := account.SignAssertion(time.Now(), scope)
	require.NoError(t, err)

	parts := strings.Split(assertion, ".")
	require.Len(t, parts, 3, "a JWT is three segments")

	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	require.NoError(t, err)
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	require.NoError(t,
		rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], signature),
		"the assertion does not verify against its own key, so Google would refuse it")

	var claims struct {
		Iss   string `json:"iss"`
		Aud   string `json:"aud"`
		Scope string `json:"scope"`
		Exp   int64  `json:"exp"`
		Iat   int64  `json:"iat"`
	}
	requireSegment(t, parts[1], &claims)
	require.Equal(t, scope, claims.Scope)
	require.Equal(t, "af-test@example.iam.gserviceaccount.com", claims.Iss)
	// The audience is the token endpoint, which is what stops an assertion
	// minted for one service being replayed against another.
	require.Equal(t, "https://oauth2.googleapis.com/token", claims.Aud)
	require.Equal(t, int64(3600), claims.Exp-claims.Iat)
}

func TestGCPAssertionFallsBackToTheCloudPlatformScope(t *testing.T) {
	// An empty scope is the common case and must not sign an empty claim, which
	// Google refuses with a message about the assertion rather than about the
	// scope.
	_, keyJSON := serviceAccountKey(t)
	account, err := ParseGCPServiceAccount(keyJSON)
	require.NoError(t, err)

	assertion, err := account.SignAssertion(time.Now(), "")
	require.NoError(t, err)
	var claims struct {
		Scope string `json:"scope"`
	}
	requireSegment(t, strings.Split(assertion, ".")[1], &claims)
	require.Equal(t, ScopeGoogleCloudPlatform, claims.Scope)
}

func TestGCPDropsThePrivateKeyPEMOnceItIsParsed(t *testing.T) {
	// The plaintext of a private key should not sit in a struct for the life of
	// the process when the parsed form is what is used.
	_, keyJSON := serviceAccountKey(t)
	account, err := ParseGCPServiceAccount(keyJSON)
	require.NoError(t, err)
	require.Empty(t, account.PrivateKey)
}

// serviceAccountKey builds a real key at run time.
//
// Generated rather than written into the repository. A private key in a test
// fixture is a private key in a repository, and the rule about that has no
// exception for keys that guard nothing: a scanner cannot tell, and a person
// skimming a diff cannot either.
func serviceAccountKey(t *testing.T) (*rsa.PrivateKey, []byte) {
	t.Helper()
	// 2048 rather than 4096, because this is generated on every run and the
	// difference is a second of test time for no additional proof.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	encoded := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	document, err := json.Marshal(map[string]string{
		"type":         "service_account",
		"project_id":   "af-test",
		"client_email": "af-test@example.iam.gserviceaccount.com",
		"private_key":  string(encoded),
		"token_uri":    "https://oauth2.googleapis.com/token",
	})
	require.NoError(t, err)
	return key, document
}

func requireSegment(t *testing.T, segment string, into any) {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, into))
}
