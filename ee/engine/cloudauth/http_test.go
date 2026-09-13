// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package cloudauth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// An address that does not parse never reaches the transport, so net/http's
// own password redaction never runs, and the error net/url builds quotes the
// address it was given. Every store here takes its address from configuration,
// and a Vault address or a proxied endpoint can carry a user and password.
func TestDoNeverQuotesThePasswordOfAnAddressThatDoesNotParse(t *testing.T) {
	for _, c := range []struct {
		name    string
		url     string
		secrets []string
		said    []string
	}{
		{
			name:    "a port that is not a number",
			url:     "https://svc:Hunter2Vault@vault.example.test:82x0/v1/secret/data/app",
			secrets: []string{"Hunter2Vault", "svc:"},
			said:    []string{"invalid port", "vault.example.test"},
		},
		{
			// net/url's inner error quotes the start of this password as a
			// port, so redacting only the address it reports would still leak.
			name:    "a slash in the password",
			url:     "https://svc:Qx7Kp/Zr9Wm@vault.example.test/v1/secret/data/app",
			secrets: []string{"Qx7Kp", "Zr9Wm"},
			said:    []string{"not quoted", "vault.example.test"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			resp, err := Do(context.Background(), Request{Method: "GET", URL: c.url})
			require.Nil(t, resp)
			require.Error(t, err)
			for _, s := range c.secrets {
				require.NotContains(t, err.Error(), s)
			}
			for _, s := range c.said {
				require.Contains(t, err.Error(), s, "the error no longer says what is wrong")
			}
		})
	}
}
